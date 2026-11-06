package insights_test

// Characterization tests of insights: the daily generators (goal alerts,
// weekly review, credit period reminders, installments due soon), the
// endpoints and the SSE stream (item 0.21f).

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/config"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type insightJSON struct {
	ID          string         `json:"id"`
	HouseholdID string         `json:"householdId"`
	UserID      *string        `json:"userId"`
	InsightDate string         `json:"insightDate"`
	InsightType string         `json:"insightType"`
	Title       string         `json:"title"`
	Body        string         `json:"body"`
	Severity    string         `json:"severity"`
	IsRead      bool           `json:"isRead"`
	Metadata    map[string]any `json:"metadata"`
}

type house struct {
	s             *apitest.Server
	id            string
	owner, member apitest.User
}

func newHouse(t *testing.T, s *apitest.Server) house {
	t.Helper()
	owner := s.Register(t, apitest.RegisterOpts{FirstName: "Owner"})
	h := s.CreateHousehold(t, owner, "")
	return house{s: s, id: h.ID, owner: owner, member: s.AddMember(t, owner, h.ID)}
}

func (h house) do(t testing.TB, u apitest.User, method, path string, body any) *apitest.Res {
	t.Helper()
	return h.s.Do(t, apitest.Req{Method: method, Path: path, Token: u.Token, Household: h.id, Body: body})
}

func (h house) spend(t testing.TB, u apitest.User, amount float64, date string, extra map[string]any) {
	t.Helper()
	body := map[string]any{"paymentMethodId": h.s.Cash(t, u).ID, "amount": amount, "spentAt": date}
	for k, v := range extra {
		body[k] = v
	}
	h.s.CreateExpense(t, u, h.id, body)
}

func (h house) goal(t testing.TB, body map[string]any) {
	t.Helper()
	h.do(t, h.owner, http.MethodPost, "/goals", body).Status(t, http.StatusCreated)
}

// generate runs the daily job for every household at date (UTC midnight).
func (h house) generate(t testing.TB, date string) {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.s.Jobs.Insights(context.Background(), d); err != nil {
		t.Fatal(err)
	}
}

func (h house) list(t testing.TB, u apitest.User, query string) []insightJSON {
	t.Helper()
	var page struct {
		Items []insightJSON `json:"items"`
	}
	h.do(t, u, http.MethodGet, "/insights"+query, nil).Status(t, http.StatusOK).JSON(t, &page)
	return page.Items
}

func ofType(list []insightJSON, typ string) []insightJSON {
	var out []insightJSON
	for _, i := range list {
		if i.InsightType == typ {
			out = append(out, i)
		}
	}
	return out
}

func TestInsightGenerators(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("goal alert: warning from 80%, critical from 100%", func(t *testing.T) {
		h := newHouse(t, s)
		h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 1000})
		h.spend(t, h.owner, 700, "2026-03-05", nil)
		h.generate(t, "2026-03-10")
		if got := ofType(h.list(t, h.owner, ""), "alert"); len(got) != 0 {
			t.Errorf("alert at 70%%: %+v", got)
		}

		h.spend(t, h.owner, 150, "2026-03-11", nil)
		h.generate(t, "2026-03-12")
		alerts := ofType(h.list(t, h.owner, ""), "alert")
		if len(alerts) != 1 || alerts[0].Severity != "warning" || alerts[0].Title != "Objetivo al 85%" || alerts[0].UserID != nil ||
			alerts[0].InsightDate != "2026-03-12" {
			t.Fatalf("alerts at 85%% = %+v", alerts)
		}

		h.spend(t, h.owner, 200, "2026-03-13", nil)
		h.generate(t, "2026-03-14")
		var critical int
		for _, a := range ofType(h.list(t, h.owner, ""), "alert") {
			if a.Severity == "critical" {
				critical++
			}
		}
		if critical != 1 {
			t.Errorf("critical alerts at 105%% = %d", critical)
		}
	})

	t.Run("household alerts are not duplicated when the job runs twice in a day", func(t *testing.T) {
		h := newHouse(t, s)
		h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 1000})
		h.spend(t, h.owner, 900, "2026-03-05", nil)
		h.generate(t, "2026-03-10")
		h.generate(t, "2026-03-10")
		alerts := ofType(h.list(t, h.owner, ""), "alert")
		if len(alerts) != 1 {
			t.Errorf("alerts after running the job twice = %d, want 1", len(alerts))
		}
	})

	t.Run("two personal goals over their limits get one alert each the same day", func(t *testing.T) {
		h := newHouse(t, s)
		var cats []struct {
			ID string `json:"id"`
		}
		h.do(t, h.owner, http.MethodGet, "/categories", nil).Status(t, http.StatusOK).JSON(t, &cats)
		h.goal(t, map[string]any{"scope": "user", "userId": h.member.ID, "goalType": "total_limit", "targetAmount": 1000})
		h.goal(t, map[string]any{"scope": "user", "userId": h.member.ID, "goalType": "category_limit", "categoryId": cats[0].ID, "targetAmount": 500})
		h.spend(t, h.member, 950, "2026-03-05", map[string]any{"categoryId": cats[0].ID})
		h.generate(t, "2026-03-10")
		alerts := ofType(h.list(t, h.member, "?userId="+h.member.ID), "alert")
		if len(alerts) != 2 {
			t.Errorf("alerts for two goals over their limits = %d, want 2", len(alerts))
		}
	})

	t.Run("savings alert only from day 20 and under half the target", func(t *testing.T) {
		h := newHouse(t, s)
		h.goal(t, map[string]any{"goalType": "savings", "targetAmount": 1000})
		h.generate(t, "2026-03-19")
		if n := len(ofType(h.list(t, h.owner, ""), "alert")); n != 0 {
			t.Errorf("savings alert before day 20: %d", n)
		}
		h.generate(t, "2026-03-20")
		alerts := ofType(h.list(t, h.owner, ""), "alert")
		if len(alerts) != 1 || alerts[0].Title != "Vas atrasado con tu ahorro" {
			t.Errorf("savings alerts = %+v", alerts)
		}
	})

	t.Run("weekly review on Sundays compares with the previous week", func(t *testing.T) {
		h := newHouse(t, s)
		h.spend(t, h.owner, 1000, "2026-03-03", nil) // previous week (Mon 2 - Sun 8)
		h.spend(t, h.owner, 1500, "2026-03-10", nil) // this week (Mon 9 - Sun 15)
		h.generate(t, "2026-03-14")                  // Saturday: nothing
		if n := len(ofType(h.list(t, h.owner, ""), "weekly_review")); n != 0 {
			t.Errorf("weekly review on a Saturday: %d", n)
		}
		h.generate(t, "2026-03-15")
		got := ofType(h.list(t, h.owner, ""), "weekly_review")
		if len(got) != 1 || got[0].Severity != "warning" || got[0].Title != "Semana cerrada: $1500.00" ||
			!strings.Contains(got[0].Body, "50% más que la anterior") {
			t.Errorf("weekly review = %+v", got)
		}
	})

	t.Run("credit period reminder on the closing day, to the card owner", func(t *testing.T) {
		h := newHouse(t, s)
		card := s.CreateCreditCard(t, h.member, "Visa Galicia", 25, 5, "2026-03-25", "2026-04-06")
		h.generate(t, "2026-03-24")
		if n := len(ofType(h.list(t, h.owner, ""), "credit_period_reminder")); n != 0 {
			t.Errorf("reminder before closing: %d", n)
		}
		h.generate(t, "2026-03-25")
		got := ofType(h.list(t, h.owner, ""), "credit_period_reminder")
		if len(got) != 1 || got[0].UserID == nil || *got[0].UserID != h.member.ID || got[0].Title != "Cargá el próximo período de Visa Galicia" {
			t.Errorf("reminders = %+v", got)
		}
		h.generate(t, "2026-03-25") // same day again: still one

		// Next month: the member loads April's period and gets April's reminder
		// too (before 000023 a card could only ever get one).
		s.Do(t, apitest.Req{Method: http.MethodPut, Path: "/payment-methods/" + card.ID + "/credit-card/periods/2026-04", Token: h.member.Token,
			Body: map[string]string{"closingDate": "2026-04-24", "dueDate": "2026-05-06"}}).Status(t, http.StatusOK)
		h.generate(t, "2026-04-24")
		if n := len(ofType(h.list(t, h.owner, ""), "credit_period_reminder")); n != 2 {
			t.Errorf("reminders after two closings = %d, want 2", n)
		}
	})

	t.Run("installment due in 3 days, then the next one", func(t *testing.T) {
		h := newHouse(t, s)
		card := s.CreateCreditCard(t, h.owner, "Master", 25, 5, "2026-02-26", "2026-03-07")
		s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": card.ID, "amount": 600, "spentAt": "2026-02-10",
			"installments": 2, "description": "Heladera"})
		h.generate(t, "2026-03-04") // installment 1 due 2026-03-07
		due := ofType(h.list(t, h.owner, ""), "alert")
		if len(due) != 1 || due[0].Title != "Cuota próxima a vencer" || !strings.Contains(due[0].Body, "1/2") {
			t.Fatalf("first due-soon = %+v", due)
		}
		h.generate(t, "2026-04-02") // installment 2 due 2026-04-05
		due = ofType(h.list(t, h.owner, ""), "alert")
		if len(due) != 2 {
			t.Errorf("due-soon alerts after two installments = %d, want 2", len(due))
		}
	})
}

func TestInsightEndpoints(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	// The member's personal insight: a shared expense paid by the owner.
	setup := func(t *testing.T) (house, insightJSON) {
		h := newHouse(t, s)
		h.spend(t, h.owner, 1000, "2026-03-05", map[string]any{"isShared": true, "description": "Luz"})
		mine := ofType(h.list(t, h.member, "?userId="+h.member.ID), "shared_expense")
		if len(mine) != 1 {
			t.Fatalf("member's shared_expense insights = %+v", mine)
		}
		return h, mine[0]
	}

	t.Run("get, unread count, mark read, mark all, delete", func(t *testing.T) {
		h, ins := setup(t)
		if ins.UserID == nil || *ins.UserID != h.member.ID || ins.IsRead || !strings.Contains(ins.Body, "Owner cargó \"Luz\"") {
			t.Errorf("insight = %+v", ins)
		}
		var got insightJSON
		h.do(t, h.member, http.MethodGet, "/insights/"+ins.ID, nil).Status(t, http.StatusOK).JSON(t, &got)
		if got.ID != ins.ID || got.Metadata["expenseId"] == nil {
			t.Errorf("get = %+v", got)
		}

		unread := func(q string) int64 {
			var u struct {
				Unread int64 `json:"unread"`
			}
			h.do(t, h.member, http.MethodGet, "/insights/unread-count"+q, nil).Status(t, http.StatusOK).JSON(t, &u)
			return u.Unread
		}
		if unread("?userId="+h.member.ID) != 1 {
			t.Errorf("unread = %d", unread("?userId="+h.member.ID))
		}
		h.do(t, h.member, http.MethodPost, "/insights/"+ins.ID+"/read", nil).Status(t, http.StatusNoContent)
		if unread("?userId="+h.member.ID) != 0 || len(h.list(t, h.member, "?unread=true&userId="+h.member.ID)) != 0 {
			t.Errorf("still unread after marking it read")
		}

		h.spend(t, h.owner, 500, "2026-03-06", map[string]any{"isShared": true, "description": "Gas"})
		h.do(t, h.member, http.MethodPost, "/insights/mark-all-read?userId="+h.member.ID, nil).Status(t, http.StatusNoContent)
		if unread("?userId="+h.member.ID) != 0 {
			t.Errorf("mark-all-read left %d", unread("?userId="+h.member.ID))
		}

		h.do(t, h.member, http.MethodDelete, "/insights/"+ins.ID, nil).Status(t, http.StatusNoContent)
		h.do(t, h.member, http.MethodGet, "/insights/"+ins.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("list filters; malformed filters are ignored", func(t *testing.T) {
		h, _ := setup(t)
		h.spend(t, h.member, 300, "2026-03-07", map[string]any{"isShared": true, "description": "Super"}) // owner's insight
		if n := len(h.list(t, h.owner, "?type=shared_expense")); n != 2 {
			t.Errorf("type filter = %d, want 2", n)
		}
		if n := len(h.list(t, h.owner, "?q=Super")); n != 1 {
			t.Errorf("text filter = %d, want 1", n)
		}
		if n := len(h.list(t, h.owner, "?limit=1")); n != 1 {
			t.Errorf("limit = %d", n)
		}
		// Bad values do not error: they are dropped (the owner also has the
		// "invite accepted" insight from adding the member).
		if n := len(ofType(h.list(t, h.owner, "?userId=x&from=x&limit=0&offset=-1"), "shared_expense")); n != 2 {
			t.Errorf("list with malformed filters = %d shared_expense, want 2", n)
		}
		h.do(t, h.owner, http.MethodGet, "/insights/nope", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
		page := h.do(t, h.owner, http.MethodGet, "/insights?limit=1", nil)
		if !strings.Contains(string(page.Body), `"totalCount"`) {
			t.Errorf("page = %s", page.Body)
		}
	})

	t.Run("another member reads, marks and deletes my personal insight", func(t *testing.T) {
		h, ins := setup(t)
		listed := false
		for _, i := range h.list(t, h.owner, "") {
			listed = listed || i.ID == ins.ID
		}
		read := h.do(t, h.owner, http.MethodGet, "/insights/"+ins.ID, nil)
		mark := h.do(t, h.owner, http.MethodPost, "/insights/"+ins.ID+"/read", nil)
		del := h.do(t, h.owner, http.MethodDelete, "/insights/"+ins.ID, nil)
		testutil.KnownBug(t, "0.6", "personal insights are visible and editable by every household member", func(t testing.TB) {
			if listed || read.Code == http.StatusOK || mark.Code == http.StatusNoContent || del.Code == http.StatusNoContent {
				t.Errorf("owner reached the member's insight: listed %v, get %d, read %d, delete %d", listed, read.Code, mark.Code, del.Code)
			}
		})
	})

	t.Run("manual generation is for superadmins only", func(t *testing.T) {
		h, _ := setup(t)
		h.do(t, h.member, http.MethodPost, "/insights/generate?at=2026-03-15", nil).
			WantError(t, http.StatusForbidden, "forbidden", "")
		h.s.MakeSuperadmin(t, h.member)
		var r struct {
			Created int `json:"created"`
			Failed  int `json:"failed"`
		}
		// ?at= is honored here because apitest runs with ENV=dev.
		h.do(t, h.member, http.MethodPost, "/insights/generate?at=2026-03-15", nil).Status(t, http.StatusOK).JSON(t, &r)
		if r.Created != 1 || r.Failed != 0 { // the Sunday weekly review
			t.Errorf("generate = %+v", r)
		}
	})

	t.Run("household scoped", func(t *testing.T) {
		h, ins := setup(t)
		other := newHouse(t, s)
		for _, r := range []apitest.Req{
			{Method: http.MethodGet, Path: "/insights/" + ins.ID},
			{Method: http.MethodPost, Path: "/insights/" + ins.ID + "/read"},
			{Method: http.MethodDelete, Path: "/insights/" + ins.ID},
		} {
			r.Token, r.Household = other.owner.Token, other.id
			s.Do(t, r).WantError(t, http.StatusNotFound, "not_found", "")
		}
		for _, i := range other.list(t, other.owner, "") {
			if i.HouseholdID != other.id {
				t.Errorf("other household sees %+v", i)
			}
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/insights", Token: other.owner.Token, Household: h.id}).
			WantError(t, http.StatusNotFound, "not_found", "")
	})
}

// sse opens the stream and returns a reader of its events.
type sse struct {
	events chan [2]string // event name, data
	resp   *http.Response
}

func openStream(t *testing.T, s *apitest.Server, query string) (*http.Response, *sse) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL+"/insights/stream"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	st := &sse{events: make(chan [2]string, 16), resp: resp}
	if resp.StatusCode != http.StatusOK {
		return resp, st
	}
	go func() {
		defer close(st.events)
		sc := bufio.NewScanner(resp.Body)
		var name string
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				st.events <- [2]string{name, strings.TrimPrefix(line, "data: ")}
			}
		}
	}()
	return resp, st
}

func (st *sse) next(t *testing.T, within time.Duration) ([2]string, bool) {
	t.Helper()
	select {
	case ev, ok := <-st.events:
		return ev, ok
	case <-time.After(within):
		t.Fatalf("no SSE event within %v", within)
		return [2]string{}, false
	}
}

// ticket asks the API for a single-use stream ticket, as the app does.
func ticket(t *testing.T, h house, u apitest.User) string {
	t.Helper()
	var out struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expiresIn"`
	}
	h.do(t, u, http.MethodPost, "/insights/stream-ticket", nil).Status(t, http.StatusOK).JSON(t, &out)
	if out.Ticket == "" || out.ExpiresIn != 60 {
		t.Fatalf("ticket = %+v", out)
	}
	return out.Ticket
}

func TestInsightStreamInDev(t *testing.T) {
	t.Parallel()
	s := apitest.New(t, apitest.WithRealtime()) // ENV=dev: DevRequestLogger wraps the writer
	h := newHouse(t, s)
	// Fixed in 0.9: it answered 500 (the dev logger's writer has no Flush).
	resp, st := openStream(t, s, "?ticket="+ticket(t, h, h.member))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream in dev = %d", resp.StatusCode)
	}
	if ev, _ := st.next(t, 5*time.Second); ev[0] != "ready" {
		t.Errorf("first event = %v", ev)
	}
}

func TestInsightStream(t *testing.T) {
	t.Parallel()
	// ENV=prod uses httplog, whose writer supports streaming (see the dev test).
	s := apitest.New(t, apitest.WithRealtime(), apitest.WithConfig(func(c *config.Config) { c.Env = "prod" }))
	h := newHouse(t, s)

	t.Run("only a fresh single-use ticket opens it", func(t *testing.T) {
		used := ticket(t, h, h.member)
		if resp, _ := openStream(t, s, "?ticket="+used); resp.StatusCode != http.StatusOK {
			t.Fatalf("stream with a ticket = %d", resp.StatusCode)
		}
		for _, q := range []string{
			"",                                // nothing
			"?ticket=made-up",                 // unknown
			"?ticket=" + used,                 // already used
			"?access_token=" + h.member.Token, // the access token in the URL is no longer accepted
			"?householdId=" + h.id,            // household alone
		} {
			if resp, _ := openStream(t, s, q); resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("stream%s = %d, want 401", q, resp.StatusCode)
			}
		}
		// A ticket needs a household the user belongs to (404: the household
		// middleware does not reveal households to outsiders).
		outsider := s.Register(t)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/insights/stream-ticket", Token: outsider.Token, Household: h.id}).
			WantError(t, http.StatusNotFound, "not_found", "")
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/insights/stream-ticket", Household: h.id}).
			WantError(t, http.StatusUnauthorized, "unauthorized", "")
	})

	t.Run("a new personal insight reaches the member's stream", func(t *testing.T) {
		resp, st := openStream(t, s, "?ticket="+ticket(t, h, h.member))
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("stream = %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
		}
		if ev, _ := st.next(t, 5*time.Second); ev[0] != "ready" {
			t.Fatalf("first event = %v", ev)
		}
		h.spend(t, h.owner, 800, "2026-03-05", map[string]any{"isShared": true, "description": "Internet"})
		ev, _ := st.next(t, 10*time.Second)
		if ev[0] != "insight.created" || !strings.Contains(ev[1], `"insightType":"shared_expense"`) || !strings.Contains(ev[1], "Internet") {
			t.Errorf("event = %v", ev)
		}
	})

	t.Run("the stream outlives the 30s request timeout", func(t *testing.T) {
		t.Parallel()
		resp, st := openStream(t, s, "?ticket="+ticket(t, h, h.owner))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream = %d", resp.StatusCode)
		}
		st.next(t, 5*time.Second) // ready
		closed := make(chan bool, 1)
		go func() {
			for range st.events {
			}
			closed <- true
		}()
		var gotClosed bool
		select {
		case <-closed:
			gotClosed = true
		case <-time.After(33 * time.Second):
		}
		if gotClosed {
			t.Errorf("stream closed by the server within 33s")
		}
	})
}
