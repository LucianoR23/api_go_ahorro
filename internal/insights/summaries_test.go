package insights_test

// AI summaries (plan 8.2): the weekly and monthly reviews, end to end with
// a fake Anthropic API.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/config"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

// fakeClaude answers like the Messages API. It reads the facts the server
// sent and writes a paragraph from them, or one with an invented number.
type fakeClaude struct {
	mu     sync.Mutex
	invent bool
	facts  []map[string]any
}

func (f *fakeClaude) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.facts)
}

func (f *fakeClaude) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &req)
	text := req.Messages[0].Content[0].Text
	text = strings.TrimSuffix(strings.TrimPrefix(text, "<facts>"), "</facts>")
	var facts map[string]any
	_ = json.Unmarshal([]byte(text), &facts)

	f.mu.Lock()
	f.facts = append(f.facts, facts)
	invent := f.invent
	f.mu.Unlock()

	var body string
	switch {
	case invent:
		body = "Gastaste $ 999.999 esta semana."
	case facts["spentThisWeek"] != nil:
		top := facts["topCategories"].([]any)[0].(map[string]any)
		body = "Gastaste " + facts["spentThisWeek"].(string) + ", un " + facts["changeVsLastWeek"].(string) +
			" que la semana pasada, sobre todo en " + top["name"].(string) + " (" + top["amount"].(string) + ")."
	default:
		body = "En " + facts["month"].(string) + " gastaste " + facts["spent"].(string) + ", con " +
			facts["topCategories"].([]any)[0].(map[string]any)["name"].(string) + " a la cabeza."
	}
	answer, _ := json.Marshal(map[string]string{"body": body})
	out, _ := json.Marshal(map[string]any{
		"content":     []map[string]string{{"type": "text", "text": string(answer)}},
		"stop_reason": "end_turn",
		"usage":       map[string]int{"input_tokens": 600, "output_tokens": 60},
	})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

func withAI(t *testing.T, f *fakeClaude) apitest.Option {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return apitest.WithConfig(func(c *config.Config) {
		c.AIEnabled = true
		c.AnthropicAPIKey = "sk-test"
		c.AnthropicBaseURL = srv.URL
	})
}

// aiHouse is a hogar whose owner verified the email (AI needs it).
func aiHouse(t *testing.T, s *apitest.Server, verified bool) (house, string) {
	t.Helper()
	owner := s.Register(t, apitest.RegisterOpts{FirstName: "Owner", Verify: verified})
	hh := s.CreateHousehold(t, owner, "")
	h := house{s: s, id: hh.ID, owner: owner, member: s.AddMember(t, owner, hh.ID)}
	var cats []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	h.do(t, owner, http.MethodGet, "/categories", nil).Status(t, http.StatusOK).JSON(t, &cats)
	return h, cats[0].ID
}

func aiUsage(t *testing.T, s *apitest.Server, h house) (rows int, task, user string) {
	t.Helper()
	err := s.Pool.QueryRow(context.Background(),
		`SELECT COUNT(*), COALESCE(MAX(task), ''), COALESCE(MAX(user_id::text), '') FROM ai_usage WHERE household_id = $1`, h.id).
		Scan(&rows, &task, &user)
	if err != nil {
		t.Fatal(err)
	}
	return rows, task, user
}

func TestWeeklyReviewWithAI(t *testing.T) {
	t.Parallel()
	f := &fakeClaude{}
	s := apitest.New(t, withAI(t, f))
	h, cat := aiHouse(t, s, true)
	h.spend(t, h.owner, 1000, "2026-03-03", nil)
	h.spend(t, h.member, 1500, "2026-03-10", map[string]any{"categoryId": cat})

	h.generate(t, "2026-03-15")
	got := ofType(h.list(t, h.member, ""), "weekly_review")
	if len(got) != 1 || got[0].Title != "Semana cerrada: $ 1.500" || got[0].Metadata["written_by"] != "ai" ||
		!strings.HasPrefix(got[0].Body, "Gastaste $ 1.500, un 50% más que la semana pasada, sobre todo en ") {
		t.Fatalf("weekly review = %+v", got)
	}
	// Only aggregates reach the provider: no descriptions, names or emails.
	facts := f.facts[0]
	if facts["spentLastWeek"] != "$ 1.000" || facts["movements"] != float64(1) {
		t.Errorf("facts = %v", facts)
	}
	raw, _ := json.Marshal(facts)
	for _, leak := range []string{h.owner.Email, h.member.Email, "Owner"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("facts leak %q: %s", leak, raw)
		}
	}
	if rows, task, user := aiUsage(t, s, h); rows != 1 || task != "summarize" || user != h.owner.ID {
		t.Errorf("ai_usage = %d rows, task %q, user %q (want the owner)", rows, task, user)
	}

	// A rerun the same day pays nothing.
	h.generate(t, "2026-03-15")
	if f.calls() != 1 {
		t.Errorf("provider calls after a rerun = %d", f.calls())
	}
}

func TestWeeklyReviewKeepsRules(t *testing.T) {
	t.Parallel()

	t.Run("an empty week asks nothing", func(t *testing.T) {
		f := &fakeClaude{}
		s := apitest.New(t, withAI(t, f))
		h, _ := aiHouse(t, s, true)
		h.generate(t, "2026-03-15")
		got := ofType(h.list(t, h.owner, ""), "weekly_review")
		if len(got) != 1 || !strings.HasPrefix(got[0].Body, "Esta semana no registraste gastos.") || f.calls() != 0 {
			t.Errorf("empty week = %+v, %d calls", got, f.calls())
		}
	})

	t.Run("an unverified owner gets no AI", func(t *testing.T) {
		f := &fakeClaude{}
		s := apitest.New(t, withAI(t, f))
		h, _ := aiHouse(t, s, false)
		h.spend(t, h.owner, 1500, "2026-03-10", nil)
		h.generate(t, "2026-03-15")
		got := ofType(h.list(t, h.owner, ""), "weekly_review")
		if len(got) != 1 || got[0].Metadata["written_by"] != "rules" || !strings.HasPrefix(got[0].Body, "Gastaste $ 1.500 en 1 transaccion") || f.calls() != 0 {
			t.Errorf("unverified = %+v, %d calls", got, f.calls())
		}
	})

	t.Run("an invented number is thrown away", func(t *testing.T) {
		f := &fakeClaude{invent: true}
		s := apitest.New(t, withAI(t, f))
		h, cat := aiHouse(t, s, true)
		h.spend(t, h.owner, 1500, "2026-03-10", map[string]any{"categoryId": cat})
		h.generate(t, "2026-03-15")
		got := ofType(h.list(t, h.owner, ""), "weekly_review")
		if len(got) != 1 || got[0].Metadata["written_by"] != "rules" || strings.Contains(got[0].Body, "999") || f.calls() != 1 {
			t.Errorf("invented = %+v, %d calls", got, f.calls())
		}
	})
}

func TestMonthlyReview(t *testing.T) {
	t.Parallel()

	t.Run("AI paragraph in the app and in the email", func(t *testing.T) {
		f := &fakeClaude{}
		s := apitest.New(t, withAI(t, f))
		h, cat := aiHouse(t, s, true)
		h.spend(t, h.owner, 1000, "2026-01-20", map[string]any{"categoryId": cat})
		h.spend(t, h.owner, 1234, "2026-02-10", map[string]any{"categoryId": cat})

		h.generate(t, "2026-02-28") // not the 1st: nothing
		h.generate(t, "2026-03-01")
		got := ofType(h.list(t, h.member, ""), "monthly_review")
		if len(got) != 1 || got[0].Title != "Tu febrero: $ 1.234" || got[0].InsightDate != "2026-03-01" ||
			got[0].Metadata["written_by"] != "ai" || !strings.HasPrefix(got[0].Body, "En febrero gastaste $ 1.234, con ") {
			t.Fatalf("monthly review = %+v", got)
		}
		facts := f.facts[0]
		if facts["spentMonthBefore"] != "$ 1.000" || facts["changeVsMonthBefore"] != "23% más" {
			t.Errorf("facts = %v", facts)
		}

		if err := s.Jobs.MonthlyReports(context.Background(), time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, m := range s.Mail.Sent() {
			if strings.Contains(m.Subject, "Resumen 2026-02") {
				found = found || strings.Contains(m.HTML, "En febrero gastaste $ 1.234, con ")
			}
		}
		if !found {
			t.Error("the email has no AI paragraph")
		}
		if f.calls() != 1 {
			t.Errorf("provider calls = %d, want 1 (the email reuses the paragraph)", f.calls())
		}
	})

	t.Run("rules without AI, none for an empty month, and it can be muted", func(t *testing.T) {
		s := apitest.New(t)
		h := newHouse(t, s)
		h.spend(t, h.owner, 1000, "2026-01-20", nil)
		h.spend(t, h.owner, 800, "2026-02-10", nil)
		empty := newHouse(t, s)

		h.generate(t, "2026-03-01")
		got := ofType(h.list(t, h.owner, ""), "monthly_review")
		if len(got) != 1 || got[0].Body != "En febrero gastaste $ 800, 20% menos que en enero." || got[0].Metadata["written_by"] != "rules" {
			t.Fatalf("rule-based monthly review = %+v", got)
		}
		if n := len(ofType(empty.list(t, empty.owner, ""), "monthly_review")); n != 0 {
			t.Errorf("empty space got %d monthly reviews", n)
		}

		if err := s.Jobs.MonthlyReports(context.Background(), time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		for _, m := range s.Mail.Sent() {
			if strings.Contains(m.HTML, "En febrero gastaste") {
				t.Error("the rule-based text was added to the email")
			}
		}

		s.Do(t, apitest.Req{Method: http.MethodPut, Path: "/me/notification-preferences", Token: h.member.Token, Body: map[string]any{
			"kinds":      []map[string]any{{"kind": "monthly_report", "channels": map[string]bool{"in_app": false, "email": true}}},
			"quietHours": map[string]any{"enabled": true, "start": "23:00", "end": "08:00"},
		}}).Status(t, http.StatusOK)
		if n := len(ofType(h.list(t, h.member, ""), "monthly_review")); n != 0 {
			t.Errorf("muted monthly review still listed: %d", n)
		}
		if n := len(ofType(h.list(t, h.owner, ""), "monthly_review")); n != 1 {
			t.Errorf("the owner lost the monthly review: %d", n)
		}
	})
}
