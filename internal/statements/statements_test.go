package statements_test

// Credit card statements (plan 4.1).

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type amount struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

type statement struct {
	PeriodYM         string   `json:"periodYm"`
	ClosingDate      string   `json:"closingDate"`
	DueDate          string   `json:"dueDate"`
	Status           string   `json:"status"`
	Totals           []amount `json:"totals"`
	InstallmentCount int      `json:"installmentCount"`
	MinimumPayment   *float64 `json:"minimumPayment"`
	PaidAt           *string  `json:"paidAt"`
	PaidAmount       *float64 `json:"paidAmount"`
	PaidCurrency     *string  `json:"paidCurrency"`
}

type detail struct {
	statement
	Installments []struct {
		ExpenseID         string  `json:"expenseId"`
		HouseholdID       string  `json:"householdId"`
		Description       string  `json:"description"`
		InstallmentNumber int     `json:"installmentNumber"`
		TotalInstallments int     `json:"totalInstallments"`
		Amount            float64 `json:"amount"`
		IsPaid            bool    `json:"isPaid"`
	} `json:"installments"`
	Adjustments []struct {
		ID          string  `json:"id"`
		Kind        string  `json:"kind"`
		Description string  `json:"description"`
		Amount      float64 `json:"amount"`
		Currency    string  `json:"currency"`
	} `json:"adjustments"`
}

type list struct {
	OpenPeriodYM string      `json:"openPeriodYm"`
	Statements   []statement `json:"statements"`
}

// fixture: a user with a Visa (closing 24, due 3) used in Mi plata and in a
// Hogar. All dates are in early 2026, so every statement is long due.
type fixture struct {
	s        *apitest.Server
	u        apitest.User
	card     string
	personal string
	hogar    string
	base     string
}

func newFixture(t *testing.T, s *apitest.Server) fixture {
	t.Helper()
	u := s.Register(t)
	card := s.CreateCreditCard(t, u, "Visa", 24, 3, "2026-01-24", "2026-02-03")
	var spaces []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/households", Token: u.Token}).Status(t, http.StatusOK).JSON(t, &spaces)
	f := fixture{s: s, u: u, card: card.ID, base: "/payment-methods/" + card.ID + "/credit-card/statements"}
	for _, sp := range spaces {
		if sp.Kind == "personal" {
			f.personal = sp.ID
		}
	}
	if f.personal == "" {
		t.Fatal("no personal space")
	}
	f.hogar = s.CreateHousehold(t, u, "Casa").ID
	return f
}

func (f fixture) expense(t *testing.T, household string, amount float64, spentAt string, installments int, desc string) string {
	t.Helper()
	return f.s.CreateExpense(t, f.u, household, map[string]any{
		"paymentMethodId": f.card, "amount": amount, "spentAt": spentAt, "installments": installments, "description": desc,
	}).Expense.ID
}

func (f fixture) do(t *testing.T, u apitest.User, method, path string, body any) *apitest.Res {
	t.Helper()
	return f.s.Do(t, apitest.Req{Method: method, Path: f.base + path, Token: u.Token, Body: body})
}

func (f fixture) detail(t *testing.T, ym string) detail {
	t.Helper()
	var d detail
	f.do(t, f.u, http.MethodGet, "/"+ym, nil).Status(t, http.StatusOK).JSON(t, &d)
	return d
}

func wantTotals(t *testing.T, got []amount, want ...amount) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("totals = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("totals = %+v, want %+v", got, want)
		}
	}
}

func TestStatements(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("a statement adds the card's installments of its closing month, from every space", func(t *testing.T) {
		f := newFixture(t, s)
		tv := f.expense(t, f.personal, 3000, "2026-01-10", 3, "Tele")
		f.expense(t, f.hogar, 500, "2026-01-20", 1, "Súper")
		f.expense(t, f.personal, 100, "2026-01-28", 1, "Café") // after the closing: next statement

		jan := f.detail(t, "2026-01")
		if jan.ClosingDate != "2026-01-24" || jan.DueDate != "2026-02-03" || jan.Status != "overdue" || jan.InstallmentCount != 2 {
			t.Errorf("january = %+v", jan.statement)
		}
		wantTotals(t, jan.Totals, amount{"ARS", 1500})
		if len(jan.Installments) != 2 || jan.Installments[0].ExpenseID != tv || jan.Installments[0].HouseholdID != f.personal ||
			jan.Installments[0].InstallmentNumber != 1 || jan.Installments[0].TotalInstallments != 3 ||
			jan.Installments[1].HouseholdID != f.hogar || jan.Installments[1].Amount != 500 {
			t.Errorf("january installments = %+v", jan.Installments)
		}

		feb := f.detail(t, "2026-02")
		wantTotals(t, feb.Totals, amount{"ARS", 1100})
		if feb.ClosingDate != "2026-02-24" || feb.DueDate != "2026-03-03" {
			t.Errorf("february dates = %s / %s", feb.ClosingDate, feb.DueDate)
		}

		empty := f.detail(t, "2025-06")
		if len(empty.Totals) != 0 || len(empty.Installments) != 0 || empty.InstallmentCount != 0 {
			t.Errorf("a month without activity = %+v", empty)
		}
	})

	t.Run("the list has months with activity plus the open period and the one to pay", func(t *testing.T) {
		f := newFixture(t, s)
		f.expense(t, f.personal, 3000, "2026-01-10", 3, "Tele")

		var l list
		f.do(t, f.u, http.MethodGet, "?from=2026-01&to=2026-06", nil).Status(t, http.StatusOK).JSON(t, &l)
		var months []string
		for _, st := range l.Statements {
			months = append(months, st.PeriodYM)
		}
		if len(months) != 3 || months[0] != "2026-01" || months[2] != "2026-03" {
			t.Errorf("months = %v", months)
		}

		today := dates.Today()
		open := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		if today.Day() > 24 {
			open = open.AddDate(0, 1, 0)
		}
		if l.OpenPeriodYM != open.Format("2006-01") {
			t.Errorf("openPeriodYm = %s, want %s", l.OpenPeriodYM, open.Format("2006-01"))
		}

		f.do(t, f.u, http.MethodGet, "", nil).Status(t, http.StatusOK).JSON(t, &l)
		var sawOpen, sawToPay bool
		for _, st := range l.Statements {
			sawOpen = sawOpen || (st.PeriodYM == l.OpenPeriodYM && st.Status == "open")
			sawToPay = sawToPay || st.PeriodYM == open.AddDate(0, -1, 0).Format("2006-01")
		}
		if !sawOpen || !sawToPay {
			t.Errorf("default list lacks the open period or the one to pay: %+v", l)
		}

		f.do(t, f.u, http.MethodGet, "?from=2026-05&to=2026-01", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "to")
		f.do(t, f.u, http.MethodGet, "?from=2026-01&to=2029-01", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "to")
		f.do(t, f.u, http.MethodGet, "?from=enero", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "from")
		f.do(t, f.u, http.MethodGet, "/2026-13", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "periodYm")
	})

	t.Run("adjustments add, credits subtract, each currency apart", func(t *testing.T) {
		f := newFixture(t, s)
		f.expense(t, f.personal, 1000, "2026-01-10", 1, "Tele")

		var d detail
		f.do(t, f.u, http.MethodPost, "/2026-01/adjustments", map[string]any{"kind": "interest", "description": " Intereses ", "amount": 200}).
			Status(t, http.StatusCreated).JSON(t, &d)
		f.do(t, f.u, http.MethodPost, "/2026-01/adjustments", map[string]any{"kind": "credit", "description": "Bonificación", "amount": 50}).
			Status(t, http.StatusCreated)
		f.do(t, f.u, http.MethodPost, "/2026-01/adjustments", map[string]any{"kind": "tax", "description": "Impuesto", "amount": 10, "currency": "USD"}).
			Status(t, http.StatusCreated).JSON(t, &d)
		wantTotals(t, d.Totals, amount{"ARS", 1150}, amount{"USD", 10})
		if len(d.Adjustments) != 3 || d.Adjustments[0].Description != "Intereses" || d.Adjustments[0].Currency != "ARS" {
			t.Errorf("adjustments = %+v", d.Adjustments)
		}

		id := d.Adjustments[0].ID
		f.do(t, f.u, http.MethodPatch, "/2026-01/adjustments/"+id, map[string]any{"kind": "interest", "description": "Intereses", "amount": 300}).
			Status(t, http.StatusOK).JSON(t, &d)
		wantTotals(t, d.Totals, amount{"ARS", 1250}, amount{"USD", 10})
		f.do(t, f.u, http.MethodDelete, "/2026-01/adjustments/"+id, nil).Status(t, http.StatusOK).JSON(t, &d)
		wantTotals(t, d.Totals, amount{"ARS", 950}, amount{"USD", 10})
		f.do(t, f.u, http.MethodDelete, "/2026-01/adjustments/"+id, nil).WantError(t, http.StatusNotFound, "not_found", "")
		// An adjustment belongs to its statement.
		f.do(t, f.u, http.MethodDelete, "/2026-02/adjustments/"+d.Adjustments[0].ID, nil).WantError(t, http.StatusNotFound, "not_found", "")

		// An adjustment alone makes a month appear.
		f.do(t, f.u, http.MethodPost, "/2025-03/adjustments", map[string]any{"kind": "fee", "description": "Mantenimiento", "amount": 900}).
			Status(t, http.StatusCreated)
		var l list
		f.do(t, f.u, http.MethodGet, "?from=2025-01&to=2025-12", nil).Status(t, http.StatusOK).JSON(t, &l)
		if len(l.Statements) != 1 || l.Statements[0].PeriodYM != "2025-03" || l.Statements[0].InstallmentCount != 0 {
			t.Errorf("list = %+v", l.Statements)
		}

		for _, bad := range []struct {
			body  map[string]any
			field string
		}{
			{map[string]any{"kind": "bonus", "description": "x", "amount": 1}, "kind"},
			{map[string]any{"kind": "fee", "description": "  ", "amount": 1}, "description"},
			{map[string]any{"kind": "fee", "description": "x", "amount": 0}, "amount"},
			{map[string]any{"kind": "fee", "description": "x", "amount": -5}, "amount"},
			{map[string]any{"kind": "fee", "description": "x", "amount": 1, "currency": "BRL"}, "currency"},
			{map[string]any{"kind": "fee", "description": string(make([]rune, 81)), "amount": 1}, "description"},
		} {
			f.do(t, f.u, http.MethodPost, "/2026-01/adjustments", bad.body).WantError(t, http.StatusUnprocessableEntity, "validation", bad.field)
		}
	})

	t.Run("paying marks every installment paid; unpaying reverts it", func(t *testing.T) {
		f := newFixture(t, s)
		tv := f.expense(t, f.personal, 3000, "2026-01-10", 3, "Tele")
		f.expense(t, f.hogar, 500, "2026-01-20", 1, "Súper")
		f.do(t, f.u, http.MethodPost, "/2026-01/adjustments", map[string]any{"kind": "tax", "description": "Impuesto", "amount": 10, "currency": "USD"}).
			Status(t, http.StatusCreated)

		var d detail
		f.do(t, f.u, http.MethodPost, "/2026-01/pay", map[string]any{}).Status(t, http.StatusOK).JSON(t, &d)
		if d.Status != "paid" || d.PaidAt == nil || d.PaidAmount == nil || *d.PaidAmount != 1500 || *d.PaidCurrency != "ARS" {
			t.Errorf("paid = %+v", d.statement)
		}
		for _, in := range d.Installments {
			if !in.IsPaid {
				t.Errorf("installment %+v not paid", in)
			}
		}
		// Only that statement: the TV's second installment is still due.
		var exp apitest.ExpenseDetail
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/expenses/" + tv, Token: f.u.Token, Household: f.personal}).
			Status(t, http.StatusOK).JSON(t, &exp)
		if !exp.Installments[0].IsPaid || exp.Installments[1].IsPaid {
			t.Errorf("tv installments = %+v", exp.Installments)
		}

		// A partial payment is recorded as is.
		f.do(t, f.u, http.MethodPost, "/2026-01/pay", map[string]any{"amount": 400}).Status(t, http.StatusOK).JSON(t, &d)
		if *d.PaidAmount != 400 || d.Status != "paid" {
			t.Errorf("partial = %+v", d.statement)
		}

		f.do(t, f.u, http.MethodPost, "/2026-01/unpay", nil).Status(t, http.StatusOK).JSON(t, &d)
		if d.Status != "overdue" || d.PaidAt != nil || d.PaidAmount != nil || d.PaidCurrency != nil {
			t.Errorf("unpaid = %+v", d.statement)
		}
		for _, in := range d.Installments {
			if in.IsPaid {
				t.Errorf("installment %+v still paid", in)
			}
		}

		f.do(t, f.u, http.MethodPost, "/2026-01/pay", map[string]any{"amount": 0}).WantError(t, http.StatusUnprocessableEntity, "validation", "amount")
		f.do(t, f.u, http.MethodPost, "/2026-01/pay", map[string]any{"currency": "BRL"}).WantError(t, http.StatusUnprocessableEntity, "validation", "currency")
		// Nothing billed in pesos in this month: the amount must be given.
		f.do(t, f.u, http.MethodPost, "/2025-03/adjustments", map[string]any{"kind": "fee", "description": "x", "amount": 5, "currency": "USD"}).
			Status(t, http.StatusCreated)
		f.do(t, f.u, http.MethodPost, "/2025-03/pay", map[string]any{"currency": "ARS"}).WantError(t, http.StatusUnprocessableEntity, "validation", "amount")
		// Its only currency is the default.
		f.do(t, f.u, http.MethodPost, "/2025-03/pay", map[string]any{}).Status(t, http.StatusOK).JSON(t, &d)
		if *d.PaidCurrency != "USD" || *d.PaidAmount != 5 {
			t.Errorf("single currency = %+v", d.statement)
		}

		var l list
		f.do(t, f.u, http.MethodGet, "", nil).Status(t, http.StatusOK).JSON(t, &l)
		f.do(t, f.u, http.MethodPost, "/"+l.OpenPeriodYM+"/pay", map[string]any{"amount": 10}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "periodYm")
	})

	t.Run("minimum payment", func(t *testing.T) {
		f := newFixture(t, s)
		var d detail
		f.do(t, f.u, http.MethodPatch, "/2026-01", map[string]any{"minimumPayment": 300.5}).Status(t, http.StatusOK).JSON(t, &d)
		if d.MinimumPayment == nil || *d.MinimumPayment != 300.5 {
			t.Errorf("minimum = %v", d.MinimumPayment)
		}
		f.do(t, f.u, http.MethodPatch, "/2026-01", map[string]any{}).Status(t, http.StatusOK).JSON(t, &d)
		if d.MinimumPayment == nil {
			t.Error("an absent field cleared the minimum")
		}
		f.do(t, f.u, http.MethodPatch, "/2026-01", map[string]any{"minimumPayment": nil}).Status(t, http.StatusOK).JSON(t, &d)
		if d.MinimumPayment != nil {
			t.Errorf("minimum after null = %v", *d.MinimumPayment)
		}
		f.do(t, f.u, http.MethodPatch, "/2026-01", map[string]any{"minimumPayment": -1}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "minimumPayment")
	})

	t.Run("only the card's owner sees or touches its statements", func(t *testing.T) {
		f := newFixture(t, s)
		member := s.AddMember(t, f.u, f.hogar)
		adj := map[string]any{"kind": "fee", "description": "x", "amount": 1}
		for _, r := range []struct {
			method, path string
			body         any
		}{
			{http.MethodGet, "", nil},
			{http.MethodGet, "/2026-01", nil},
			{http.MethodPatch, "/2026-01", map[string]any{"minimumPayment": 1}},
			{http.MethodPost, "/2026-01/pay", map[string]any{"amount": 1}},
			{http.MethodPost, "/2026-01/unpay", nil},
			{http.MethodPost, "/2026-01/adjustments", adj},
		} {
			res := f.do(t, member, r.method, r.path, r.body)
			if res.Code != http.StatusNotFound && res.Code != http.StatusForbidden {
				t.Errorf("%s %s by a member = %d", r.method, r.path, res.Code)
			}
		}
		f.do(t, f.u, http.MethodGet, "/2026-01", nil).Status(t, http.StatusOK)
	})
}
