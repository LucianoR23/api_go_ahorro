package reports_test

// Characterization tests of the reports endpoints and the monthly email job
// (item 0.21f).

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type monthlyJSON struct {
	BaseCurrency string  `json:"baseCurrency"`
	Month        string  `json:"month"`
	From         string  `json:"from"`
	To           string  `json:"to"`
	Spent        float64 `json:"spentThisMonth"`
	Billed       float64 `json:"billedThisMonth"`
	Due          float64 `json:"dueThisMonth"`
	ByCategory   []struct {
		CategoryID   *string `json:"categoryId"`
		CategoryName string  `json:"categoryName"`
		Total        float64 `json:"total"`
		Pct          float64 `json:"pct"`
		TxCount      int64   `json:"txCount"`
	} `json:"byCategory"`
	FixedVariable struct {
		FixedTotal    float64 `json:"fixedTotal"`
		VariableTotal float64 `json:"variableTotal"`
		FixedPct      float64 `json:"fixedPct"`
		FixedCount    int64   `json:"fixedCount"`
		VariableCount int64   `json:"variableCount"`
	} `json:"fixedVariable"`
}

type trendsJSON struct {
	Months int `json:"months"`
	Points []struct {
		Month      string  `json:"month"`
		SpentTotal float64 `json:"spentTotal"`
		DueTotal   float64 `json:"dueTotal"`
		Income     float64 `json:"income"`
		Net        float64 `json:"net"`
	} `json:"points"`
}

type house struct {
	s             *apitest.Server
	id            string
	owner, member apitest.User
	category      struct{ ID, Name string }
	// sub is a seeded subcategory; parent is its parent (plan 1.4).
	sub, parent struct{ ID, Name string }
}

func newHouse(t *testing.T, s *apitest.Server) house {
	t.Helper()
	owner := s.Register(t)
	hh := s.CreateHousehold(t, owner, "Casa Reportes")
	h := house{s: s, id: hh.ID, owner: owner, member: s.AddMember(t, owner, hh.ID)}
	var cats []struct {
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		ParentID *string `json:"parentId"`
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: owner.Token, Household: hh.ID}).Status(t, http.StatusOK).JSON(t, &cats)
	byID := map[string]string{}
	for _, c := range cats {
		byID[c.ID] = c.Name
	}
	for _, c := range cats {
		switch {
		case c.ParentID == nil && h.category.ID == "":
			h.category.ID, h.category.Name = c.ID, c.Name
		case c.ParentID != nil && h.sub.ID == "":
			h.sub.ID, h.sub.Name = c.ID, c.Name
			h.parent.ID, h.parent.Name = *c.ParentID, byID[*c.ParentID]
		}
	}
	if h.category.ID == "" || h.sub.ID == "" {
		t.Fatal("seeded categories missing")
	}
	return h
}

func (h house) get(t testing.TB, path string, v any) {
	t.Helper()
	h.s.Do(t, apitest.Req{Method: http.MethodGet, Path: path, Token: h.member.Token, Household: h.id}).Status(t, http.StatusOK).JSON(t, v)
}

func (h house) spend(t testing.TB, amount float64, date string, extra map[string]any) {
	t.Helper()
	body := map[string]any{"paymentMethodId": h.s.Cash(t, h.owner).ID, "amount": amount, "spentAt": date}
	for k, v := range extra {
		body[k] = v
	}
	h.s.CreateExpense(t, h.owner, h.id, body)
}

func (h house) income(t testing.TB, amount float64, date string) {
	t.Helper()
	h.s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/incomes", Token: h.owner.Token, Household: h.id, Body: map[string]any{
		"amount": amount, "currency": "ARS", "source": "salary", "description": "Sueldo", "receivedAt": date}}).Status(t, http.StatusCreated)
}

// fixed creates a fixed-amount recurring expense and books its March occurrence.
func (h house) fixed(t testing.TB, amount float64) {
	t.Helper()
	var r struct {
		ID string `json:"id"`
	}
	h.s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/recurring-expenses", Token: h.owner.Token, Household: h.id, Body: map[string]any{
		"paymentMethodId": h.s.Cash(t, h.owner).ID, "amount": amount, "currency": "ARS", "description": "Alquiler",
		"installments": 1, "frequency": "monthly", "dayOfMonth": 1, "startsAt": "2099-01-01"}}).Status(t, http.StatusCreated).JSON(t, &r)
	if _, err := h.s.Pool.Exec(t.Context(), `UPDATE recurring_expenses SET starts_at = '2026-01-01' WHERE id = $1`, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.s.Jobs.RecurringExpenses(context.Background(), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

func TestMonthlyReport(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("spent, billed, due, categories, fixed vs variable", func(t *testing.T) {
		h := newHouse(t, s)
		h.fixed(t, 3000)                                                            // fixed, spent 2026-03-01
		h.spend(t, 1000, "2026-03-10", map[string]any{"categoryId": h.category.ID}) // variable
		h.spend(t, 999, "2026-02-28", nil)                                          // previous month
		card := s.CreateCreditCard(t, h.owner, "Visa", 25, 5, "2026-03-26", "2026-04-07")
		s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": card.ID, "amount": 600, "spentAt": "2026-03-15", "installments": 3})

		var m monthlyJSON
		h.get(t, "/reports/monthly?month=2026-03", &m)
		if m.Month != "2026-03" || m.From != "2026-03-01" || m.To != "2026-03-31" || m.BaseCurrency != "ARS" {
			t.Errorf("header = %+v", m)
		}
		// Spent counts whole expenses by date; billed and due count installments.
		if m.Spent != 4600 || m.Billed != 4200 || m.Due != 4000 {
			t.Errorf("spent %v (want 4600), billed %v (want 4200), due %v (want 4000)", m.Spent, m.Billed, m.Due)
		}
		if m.FixedVariable.FixedTotal != 3000 || m.FixedVariable.VariableTotal != 1600 || m.FixedVariable.FixedCount != 1 || m.FixedVariable.VariableCount != 2 {
			t.Errorf("fixed/variable = %+v", m.FixedVariable)
		}
		var cat, none bool
		for _, c := range m.ByCategory {
			if c.CategoryID != nil && *c.CategoryID == h.category.ID {
				cat = c.Total == 1000 && c.CategoryName == h.category.Name && c.TxCount == 1
			}
			if c.CategoryID == nil {
				none = c.CategoryName == "Sin categoría" && c.Total == 3600
			}
		}
		if !cat || !none {
			t.Errorf("byCategory = %+v", m.ByCategory)
		}
	})

	t.Run("a subcategory adds up in its parent", func(t *testing.T) {
		h := newHouse(t, s)
		h.spend(t, 700, "2026-03-10", map[string]any{"categoryId": h.sub.ID})
		h.spend(t, 300, "2026-03-11", map[string]any{"categoryId": h.parent.ID})

		var m monthlyJSON
		h.get(t, "/reports/monthly?month=2026-03", &m)
		if len(m.ByCategory) != 1 {
			t.Fatalf("byCategory = %+v, want only %s", m.ByCategory, h.parent.Name)
		}
		c := m.ByCategory[0]
		if c.CategoryID == nil || *c.CategoryID != h.parent.ID || c.CategoryName != h.parent.Name || c.Total != 1000 || c.TxCount != 2 {
			t.Errorf("byCategory = %+v, want %s with 1000 in 2", c, h.parent.Name)
		}
	})

	t.Run("empty month and validation", func(t *testing.T) {
		h := newHouse(t, s)
		var m monthlyJSON
		h.get(t, "/reports/monthly?month=2020-01", &m)
		if m.Spent != 0 || len(m.ByCategory) != 0 {
			t.Errorf("empty month = %+v", m)
		}
		h.get(t, "/reports/monthly", &m) // default: this month
		if m.Month != time.Now().Format("2006-01") {
			t.Errorf("default month = %s", m.Month)
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/reports/monthly?month=03-2026", Token: h.owner.Token, Household: h.id}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "month")
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/reports/ai-export?month=x", Token: h.owner.Token, Household: h.id}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "month")
	})

	t.Run("drafts are counted as spending", func(t *testing.T) {
		h := newHouse(t, s)
		var r struct {
			ID string `json:"id"`
		}
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/recurring-expenses", Token: h.owner.Token, Household: h.id, Body: map[string]any{
			"paymentMethodId": s.Cash(t, h.owner).ID, "amount": 5000, "currency": "ARS", "description": "Luz", "installments": 1,
			"frequency": "monthly", "dayOfMonth": 10, "startsAt": "2099-01-01", "amountIsVariable": true}}).Status(t, http.StatusCreated).JSON(t, &r)
		if _, err := s.Pool.Exec(t.Context(), `UPDATE recurring_expenses SET starts_at = '2026-01-01' WHERE id = $1`, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Jobs.RecurringExpenses(context.Background(), time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
		var m monthlyJSON
		h.get(t, "/reports/monthly?month=2026-03", &m)
		if m.Spent != 0 {
			t.Errorf("spent with only a draft = %v", m.Spent)
		}
	})

	t.Run("household scoped", func(t *testing.T) {
		h := newHouse(t, s)
		h.spend(t, 1000, "2026-03-10", nil)
		other := newHouse(t, s)
		var m monthlyJSON
		other.get(t, "/reports/monthly?month=2026-03", &m)
		if m.Spent != 0 {
			t.Errorf("other household sees %v spent", m.Spent)
		}
		for _, p := range []string{"/reports/monthly", "/reports/trends", "/reports/ai-export"} {
			s.Do(t, apitest.Req{Method: http.MethodGet, Path: p, Token: other.owner.Token, Household: h.id}).WantError(t, http.StatusNotFound, "not_found", "")
		}
	})
}

func TestTrendsAndAIExport(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	h.spend(t, 1000, "2026-01-10", nil)
	h.spend(t, 2000, "2026-03-10", map[string]any{"categoryId": h.category.ID})
	h.income(t, 5000, "2026-03-01")

	t.Run("trends: one point per month, net = income - due", func(t *testing.T) {
		var tr trendsJSON
		h.get(t, "/reports/trends?months=3&at=2026-03-15", &tr)
		if tr.Months != 3 || len(tr.Points) != 3 {
			t.Fatalf("trends = %+v", tr)
		}
		want := []struct {
			month              string
			spent, income, net float64
		}{{"2026-01", 1000, 0, -1000}, {"2026-02", 0, 0, 0}, {"2026-03", 2000, 5000, 3000}}
		for i, w := range want {
			p := tr.Points[i]
			if p.Month != w.month || p.SpentTotal != w.spent || p.Income != w.income || p.Net != w.net {
				t.Errorf("point %d = %+v, want %+v", i, p, w)
			}
		}
	})

	t.Run("trends: months default to 6 and cap at 24", func(t *testing.T) {
		var tr trendsJSON
		h.get(t, "/reports/trends?at=2026-03-15", &tr)
		if tr.Months != 6 || len(tr.Points) != 6 || tr.Points[0].Month != "2025-10" {
			t.Errorf("default trends = %d points from %s", len(tr.Points), tr.Points[0].Month)
		}
		h.get(t, "/reports/trends?months=100&at=2026-03-15", &tr)
		if tr.Months != 24 {
			t.Errorf("months capped at %d", tr.Months)
		}
		h.get(t, "/reports/trends?months=x&at=nope", &tr) // ignored: defaults
		if tr.Months != 6 {
			t.Errorf("bad params: months %d", tr.Months)
		}
	})

	t.Run("AI export bundles the month, top categories, trends and a prompt", func(t *testing.T) {
		var ex struct {
			HouseholdName string  `json:"householdName"`
			Month         string  `json:"month"`
			Spent         float64 `json:"spent"`
			TopCategories []struct {
				Name  string  `json:"name"`
				Total float64 `json:"total"`
			} `json:"topCategories"`
			TrendsLast6 []any  `json:"trendsLast6"`
			Prompt      string `json:"prompt"`
		}
		h.get(t, "/reports/ai-export?month=2026-03", &ex)
		if ex.HouseholdName != "Casa Reportes" || ex.Month != "2026-03" || ex.Spent != 2000 || len(ex.TrendsLast6) != 6 ||
			len(ex.TopCategories) != 1 || ex.TopCategories[0].Name != h.category.Name {
			t.Errorf("export = %+v", ex)
		}
		if !strings.Contains(ex.Prompt, `"Casa Reportes"`) || !strings.Contains(ex.Prompt, "2026-03") || !strings.Contains(ex.Prompt, "2000.00 ARS") {
			t.Errorf("prompt = %q", ex.Prompt)
		}
	})
}

func TestMonthlyEmail(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	h.spend(t, 1234, "2026-02-10", nil)

	if err := s.Jobs.MonthlyReports(context.Background(), time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}

	for _, u := range []apitest.User{h.owner, h.member} {
		var mail *apitest.Mail
		for _, m := range s.Mail.Sent() {
			if m.Subject == "TestBrand — Resumen 2026-02 · Casa Reportes" {
				for _, to := range m.To {
					if to == u.Email {
						mm := m
						mail = &mm
					}
				}
			}
		}
		if mail == nil {
			t.Fatalf("no February summary for %s", u.Email)
		}
		if !strings.Contains(mail.HTML, "TestBrand — Resumen mensual") || !strings.Contains(mail.HTML, "Generado automáticamente por TestBrand") {
			t.Errorf("summary mail does not use the configured brand")
		}
		if mail.From != s.Config.ReportFromEmail || !strings.Contains(mail.HTML, "1234") {
			t.Errorf("summary mail from %q, html contains the total: %v", mail.From, strings.Contains(mail.HTML, "1234"))
		}
	}
}
