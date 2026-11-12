package ai_test

// AI platform layer (plan 8.1): guardrails, retries and usage records.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/valuo_api/internal/ai"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type reply struct {
	out ai.Output
	err error
}

// fakeProvider answers from a script; once it runs out it repeats the last
// reply.
type fakeProvider struct {
	name    string
	mu      sync.Mutex
	replies []reply
	calls   []ai.Call
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Generate(_ context.Context, c ai.Call) (ai.Output, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	r := f.replies[0]
	if len(f.replies) > 1 {
		f.replies = f.replies[1:]
	}
	return r.out, r.err
}

func (f *fakeProvider) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// expense is a task answer: it validates itself.
type expense struct {
	Amount float64 `json:"amount"`
}

func (e expense) Validate() error {
	if e.Amount <= 0 {
		return errors.New("amount must be positive")
	}
	return nil
}

func ok(text string, in, out int) reply {
	return reply{out: ai.Output{Text: text, InputTokens: in, OutputTokens: out}}
}

var schema = json.RawMessage(`{"type":"object","properties":{"amount":{"type":"number"}},"required":["amount"],"additionalProperties":false}`)

type env struct {
	pool       *pgxpool.Pool
	claude     *fakeProvider
	gemini     *fakeProvider
	verified   uuid.UUID
	unverified uuid.UUID
	household  uuid.UUID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	pool := testdb.New(t)
	e := &env{pool: pool, claude: &fakeProvider{name: "anthropic"}, gemini: &fakeProvider{name: "gemini"}}
	e.verified = addUser(t, pool, true)
	e.unverified = addUser(t, pool, false)
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO households (name, created_by) VALUES ('Casa', $1) RETURNING id`, e.verified).Scan(&e.household); err != nil {
		t.Fatal(err)
	}
	return e
}

func addUser(t *testing.T, pool *pgxpool.Pool, verified bool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, password_hash, first_name, last_name, email_verified_at)
		VALUES ($1, 'x', 'Ana', 'Test', CASE WHEN $2 THEN NOW() END) RETURNING id`,
		uuid.NewString()+"@example.com", verified).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func defaultLimits() ai.Limits {
	return ai.Limits{PerMinute: 100, PerDay: 1000, UserDailyMicroUSD: ai.MicroUSD(1), DailyMicroUSD: ai.MicroUSD(10)}
}

func (e *env) service(t *testing.T, mod func(*ai.Config)) *ai.Service {
	t.Helper()
	cfg := ai.Config{
		Enabled:   true,
		Routes:    "default=anthropic:claude-haiku-5-5:low,parse_audio=gemini:gemini-3.5-flash-lite:minimal",
		Prices:    "claude-haiku-5-5=0.10/0.50,gemini-3.5-flash-lite=0.30/2.50",
		Limits:    defaultLimits(),
		Providers: []ai.Provider{e.claude, e.gemini},
		Location:  time.UTC,
	}
	if mod != nil {
		mod(&cfg)
	}
	s, err := ai.New(e.pool, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) request(user uuid.UUID, task ai.Task) ai.Request {
	return ai.Request{
		UserID: user, HouseholdID: &e.household, Task: task, Channel: ai.ChannelApp,
		PromptVersion: "v1", System: "Extract the expense.",
		Parts: []ai.Part{ai.TextPart("café 2500")}, Schema: schema, MaxOutputTokens: 300,
	}
}

type usageRow struct {
	Task, Channel, Provider, Model, PromptVersion, Status string
	In, Out, Audio                                        int
	Cost                                                  int64
	Household                                             *uuid.UUID
}

func (e *env) rows(t *testing.T) []usageRow {
	t.Helper()
	rs, err := e.pool.Query(context.Background(), `
		SELECT task, channel, provider, model, prompt_version, status, input_tokens, output_tokens,
		       audio_seconds, cost_micro_usd, household_id
		FROM ai_usage ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []usageRow
	for rs.Next() {
		var r usageRow
		if err := rs.Scan(&r.Task, &r.Channel, &r.Provider, &r.Model, &r.PromptVersion, &r.Status,
			&r.In, &r.Out, &r.Audio, &r.Cost, &r.Household); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func statuses(rows []usageRow) string {
	s := make([]string, len(rows))
	for i, r := range rows {
		s[i] = r.Status
	}
	return strings.Join(s, ",")
}

func TestRunDecodesAndRecords(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.claude.replies = []reply{ok(`{"amount":2500}`, 500, 150)}
	s := e.service(t, nil)

	var got expense
	if err := s.Run(context.Background(), e.request(e.verified, ai.TaskParseExpense), &got); err != nil {
		t.Fatal(err)
	}
	if got.Amount != 2500 {
		t.Errorf("amount = %v", got.Amount)
	}
	c := e.claude.calls[0]
	if c.Model != "claude-haiku-5-5" || c.Effort != "low" || c.MaxOutputTokens != 300 || c.System != "Extract the expense." {
		t.Errorf("call = %+v", c)
	}
	rows := e.rows(t)
	want := usageRow{Task: "parse_expense", Channel: "app", Provider: "anthropic", Model: "claude-haiku-5-5",
		PromptVersion: "v1", Status: "ok", In: 500, Out: 150, Cost: 125, Household: &e.household}
	if len(rows) != 1 || rows[0].Household == nil || *rows[0].Household != e.household {
		t.Fatalf("rows = %+v", rows)
	}
	rows[0].Household = want.Household
	if rows[0] != want {
		t.Errorf("row = %+v\nwant  %+v", rows[0], want)
	}
}

func TestRunRoutesAudioToGemini(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.gemini.replies = []reply{{out: ai.Output{Text: `{"amount":15000}`, InputTokens: 1100, AudioTokens: 960, OutputTokens: 40}}}
	s := e.service(t, nil)
	req := e.request(e.verified, ai.TaskParseAudio)
	req.Parts = []ai.Part{ai.MediaPart("audio/ogg", []byte("OggS"))}
	var got expense
	if err := s.Run(context.Background(), req, &got); err != nil {
		t.Fatal(err)
	}
	if e.claude.count() != 0 || e.gemini.count() != 1 || e.gemini.calls[0].Effort != "minimal" {
		t.Errorf("claude %d gemini %d", e.claude.count(), e.gemini.count())
	}
	if r := e.rows(t)[0]; r.Provider != "gemini" || r.Audio != 30 || r.Cost != 430 {
		// 1100 tokens at 0.30 = 330, 40 at 2.50 = 100.
		t.Errorf("row = %+v", r)
	}
}

func TestRunRetriesAnInvalidAnswerOnce(t *testing.T) {
	t.Parallel()
	t.Run("then valid", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.claude.replies = []reply{ok(`{"amount":2500,"note":"extra"}`, 10, 5), ok(`{"amount":2500}`, 10, 5)}
		var got expense
		if err := e.service(t, nil).Run(context.Background(), e.request(e.verified, ai.TaskParseExpense), &got); err != nil {
			t.Fatal(err)
		}
		if got.Amount != 2500 || statuses(e.rows(t)) != "invalid_output,ok" {
			t.Errorf("amount %v rows %s", got.Amount, statuses(e.rows(t)))
		}
	})
	t.Run("twice invalid", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.claude.replies = []reply{ok(`{"amount":-3}`, 10, 5)}
		got := expense{Amount: 7}
		err := e.service(t, nil).Run(context.Background(), e.request(e.verified, ai.TaskParseExpense), &got)
		if !errors.Is(err, ai.ErrInvalidOutput) {
			t.Fatalf("err = %v", err)
		}
		if got.Amount != 7 || e.claude.count() != 2 || statuses(e.rows(t)) != "invalid_output,invalid_output" {
			t.Errorf("out touched (%v) or calls %d rows %s", got.Amount, e.claude.count(), statuses(e.rows(t)))
		}
	})
	t.Run("cut answer", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.claude.replies = []reply{{out: ai.Output{Text: `{"amount":1}`, Truncated: true, InputTokens: 10, OutputTokens: 300}}}
		var got expense
		err := e.service(t, nil).Run(context.Background(), e.request(e.verified, ai.TaskParseExpense), &got)
		if !errors.Is(err, ai.ErrInvalidOutput) || e.rows(t)[0].Cost == 0 {
			t.Errorf("err %v rows %+v", err, e.rows(t))
		}
	})
}

func TestRunProviderFailures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		calls  int
		status string
	}{
		{"overloaded twice", &ai.ProviderError{Status: 529, Retryable: true, Msg: "overloaded"}, 2, "provider_error,provider_error"},
		{"bad key", &ai.ProviderError{Status: 401, Msg: "auth"}, 1, "provider_error"},
		{"timeout", &ai.ProviderError{Timeout: true, Msg: "deadline"}, 1, "timeout"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.claude.replies = []reply{{err: c.err}}
			var got expense
			err := e.service(t, nil).Run(context.Background(), e.request(e.verified, ai.TaskParseExpense), &got)
			if !errors.Is(err, ai.ErrProvider) || e.claude.count() != c.calls || statuses(e.rows(t)) != c.status {
				t.Errorf("err %v calls %d rows %s", err, e.claude.count(), statuses(e.rows(t)))
			}
		})
	}
	t.Run("transient then ok", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.claude.replies = []reply{{err: &ai.ProviderError{Status: 500, Retryable: true}}, ok(`{"amount":1}`, 1, 1)}
		var got expense
		if err := e.service(t, nil).Run(context.Background(), e.request(e.verified, ai.TaskParseExpense), &got); err != nil {
			t.Fatal(err)
		}
		if statuses(e.rows(t)) != "provider_error,ok" {
			t.Errorf("rows %s", statuses(e.rows(t)))
		}
	})
}

func TestRunGuardrails(t *testing.T) {
	t.Parallel()
	run := func(s *ai.Service, req ai.Request) error {
		var got expense
		return s.Run(context.Background(), req, &got)
	}

	t.Run("kill switch and disabled task", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.claude.replies = []reply{ok(`{"amount":1}`, 1, 1)}
		off := e.service(t, func(c *ai.Config) { c.Enabled = false })
		if err := run(off, e.request(e.verified, ai.TaskParseExpense)); !errors.Is(err, ai.ErrDisabled) {
			t.Errorf("off: err = %v", err)
		}
		some := e.service(t, func(c *ai.Config) { c.DisabledTasks = []string{"parse_receipt"} })
		if err := run(some, e.request(e.verified, ai.TaskParseReceipt)); !errors.Is(err, ai.ErrDisabled) {
			t.Errorf("task off: err = %v", err)
		}
		if err := run(some, e.request(e.verified, ai.TaskParseExpense)); err != nil {
			t.Errorf("other task: err = %v", err)
		}
		if e.claude.count() != 1 || statuses(e.rows(t)) != "disabled,disabled,ok" {
			t.Errorf("calls %d rows %s", e.claude.count(), statuses(e.rows(t)))
		}
	})

	t.Run("unverified email", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.claude.replies = []reply{ok(`{"amount":1}`, 1, 1)}
		s := e.service(t, nil)
		if err := run(s, e.request(e.unverified, ai.TaskParseExpense)); !errors.Is(err, ai.ErrUnverified) {
			t.Errorf("unverified: err = %v", err)
		}
		if err := run(s, e.request(uuid.New(), ai.TaskParseExpense)); !errors.Is(err, ai.ErrUnverified) {
			t.Errorf("unknown user: err = %v", err)
		}
		if e.claude.count() != 0 {
			t.Error("provider called")
		}
	})

	t.Run("per minute and per day", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.claude.replies = []reply{ok(`{"amount":1}`, 1, 1)}
		s := e.service(t, func(c *ai.Config) { c.Limits.PerMinute = 2 })
		for i := range 2 {
			if err := run(s, e.request(e.verified, ai.TaskParseExpense)); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		if err := run(s, e.request(e.verified, ai.TaskParseExpense)); !errors.Is(err, ai.ErrRateLimited) {
			t.Errorf("third in a minute: err = %v", err)
		}
		// Another person is not affected.
		other := addUser(t, e.pool, true)
		if err := run(s, e.request(other, ai.TaskParseExpense)); err != nil {
			t.Errorf("other person: %v", err)
		}
		// Older calls today count for the day, not for the minute.
		if _, err := e.pool.Exec(context.Background(), `UPDATE ai_usage SET created_at = NOW() - INTERVAL '2 minutes'`); err != nil {
			t.Fatal(err)
		}
		daily := e.service(t, func(c *ai.Config) { c.Limits.PerDay = 2 })
		if err := run(daily, e.request(e.verified, ai.TaskParseExpense)); !errors.Is(err, ai.ErrRateLimited) {
			t.Errorf("third in a day: err = %v", err)
		}
		if got := statuses(e.rows(t)); got != "ok,ok,rate_limited,ok,rate_limited" {
			t.Errorf("rows %s", got)
		}
	})

	t.Run("personal daily cost", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.claude.replies = []reply{ok(`{"amount":1}`, 500, 150)} // 125 micro-USD each
		s := e.service(t, func(c *ai.Config) { c.Limits.UserDailyMicroUSD = 200 })
		for i := range 2 {
			if err := run(s, e.request(e.verified, ai.TaskParseExpense)); err != nil {
				t.Fatalf("call %d: %v", i, err)
			}
		}
		if err := run(s, e.request(e.verified, ai.TaskParseExpense)); !errors.Is(err, ai.ErrUserBudget) {
			t.Errorf("over 200: err = %v", err)
		}
		// Yesterday's spend does not count today.
		if _, err := e.pool.Exec(context.Background(), `UPDATE ai_usage SET created_at = NOW() - INTERVAL '2 days'`); err != nil {
			t.Fatal(err)
		}
		if err := run(s, e.request(e.verified, ai.TaskParseExpense)); err != nil {
			t.Errorf("next day: %v", err)
		}
	})
}

func TestBudgetAlertsAndStop(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.claude.replies = []reply{ok(`{"amount":1}`, 3000, 0)} // 300 micro-USD each
	var mu sync.Mutex
	var alerts []string
	s := e.service(t, func(c *ai.Config) {
		c.Limits.DailyMicroUSD = 1000
		c.Alert = func(_ context.Context, pct int, spent, budget int64) {
			mu.Lock()
			alerts = append(alerts, fmt.Sprintf("%d%%:%d/%d", pct, spent, budget))
			mu.Unlock()
		}
	})
	// Spend from other people counts toward the global budget.
	other := addUser(t, e.pool, true)
	users := []uuid.UUID{e.verified, other, e.verified, other}
	for i, u := range users {
		var got expense
		if err := s.Run(context.Background(), e.request(u, ai.TaskParseExpense), &got); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	var got expense
	if err := s.Run(context.Background(), e.request(e.verified, ai.TaskParseExpense), &got); !errors.Is(err, ai.ErrGlobalBudget) {
		t.Errorf("over budget: err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(alerts, " ") != "50%:600/1000 80%:900/1000 100%:1200/1000" {
		t.Errorf("alerts = %v", alerts)
	}
}

func TestRunRejectsBadRequests(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.claude.replies = []reply{ok(`{"amount":1}`, 1, 1)}
	s := e.service(t, nil)
	mods := map[string]func(*ai.Request){
		"no schema":  func(r *ai.Request) { r.Schema = nil },
		"bad schema": func(r *ai.Request) { r.Schema = json.RawMessage(`{`) },
		"no input":   func(r *ai.Request) { r.Parts = nil },
		"two files": func(r *ai.Request) {
			r.Parts = []ai.Part{ai.MediaPart("image/png", []byte{1}), ai.MediaPart("image/png", []byte{2})}
		},
		"huge file":     func(r *ai.Request) { r.Parts = []ai.Part{ai.MediaPart("image/png", make([]byte, ai.MaxMediaBytes+1))} },
		"long text":     func(r *ai.Request) { r.Parts = []ai.Part{ai.TextPart(strings.Repeat("a", ai.MaxTextBytes+1))} },
		"no max tokens": func(r *ai.Request) { r.MaxOutputTokens = 0 },
		"bad task":      func(r *ai.Request) { r.Task = "Chat!" },
		"bad channel":   func(r *ai.Request) { r.Channel = "sms" },
	}
	for name, mod := range mods {
		req := e.request(e.verified, ai.TaskParseExpense)
		mod(&req)
		var got expense
		if err := s.Run(context.Background(), req, &got); !errors.Is(err, ai.ErrBadRequest) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := s.Run(context.Background(), e.request(e.verified, ai.TaskParseExpense), nil); !errors.Is(err, ai.ErrBadRequest) {
		t.Errorf("nil out: err = %v", err)
	}
	if e.claude.count() != 0 || len(e.rows(t)) != 0 {
		t.Errorf("calls %d rows %d", e.claude.count(), len(e.rows(t)))
	}
}

func TestNewChecksRoutes(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	base := ai.Config{
		Routes: "default=anthropic:claude-haiku-5-5", Prices: "claude-haiku-5-5=0.1/0.5",
		Providers: []ai.Provider{&fakeProvider{name: "anthropic"}},
	}
	if _, err := ai.New(pool, base); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	noPrice := base
	noPrice.Prices = "other=1/1"
	if _, err := ai.New(pool, noPrice); err == nil || !strings.Contains(err.Error(), "no price") {
		t.Errorf("missing price: err = %v", err)
	}
	noProvider := base
	noProvider.Routes = "default=gemini:claude-haiku-5-5"
	if _, err := ai.New(pool, noProvider); err == nil {
		t.Error("missing provider accepted")
	}
}
