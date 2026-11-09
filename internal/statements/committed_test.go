package statements_test

// "Comprometido" and installment plans (plan 4.2), and drafts out of
// statements.

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

type periodTotals struct {
	PeriodYM string   `json:"periodYm"`
	Totals   []amount `json:"totals"`
}

type committed struct {
	Months []periodTotals `json:"months"`
	Cards  []struct {
		PaymentMethodID string         `json:"paymentMethodId"`
		Name            string         `json:"name"`
		Alias           string         `json:"alias"`
		OpenPeriodYM    string         `json:"openPeriodYm"`
		ToPay           *statement     `json:"toPay"`
		Months          []periodTotals `json:"months"`
	} `json:"cards"`
}

type plans struct {
	Plans []struct {
		ExpenseID             string  `json:"expenseId"`
		Description           string  `json:"description"`
		Currency              string  `json:"currency"`
		TotalInstallments     int     `json:"totalInstallments"`
		NextInstallmentNumber int     `json:"nextInstallmentNumber"`
		NextInstallmentAmount float64 `json:"nextInstallmentAmount"`
		LastPeriodYM          string  `json:"lastPeriodYm"`
	} `json:"plans"`
}

// openFor: the closing month open today for a card closing on day (no
// override loaded for the current months).
func openFor(day int) time.Time {
	today := dates.Today()
	open := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	if today.Day() > day {
		open = open.AddDate(0, 1, 0)
	}
	return open
}

func ymOf(t time.Time) string { return t.Format("2006-01") }

// dayOf: day d of month m, as YYYY-MM-DD.
func dayOf(m time.Time, d int) string {
	return time.Date(m.Year(), m.Month(), d, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
}

func TestCommitted(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	today := dates.Today().Format(time.DateOnly)

	t.Run("each card counts from its open period; the total adds them per month and currency", func(t *testing.T) {
		f := newFixture(t, s)
		master := s.CreateCreditCard(t, f.u, "Master", 1, 10, "2026-01-01", "2026-01-10")
		visaOpen, masterOpen := openFor(24), openFor(1)
		start := visaOpen
		if masterOpen.Before(start) {
			start = masterOpen
		}

		f.expense(t, f.personal, 3000, today, 3, "Tele")                                // 1000 × 3 from Visa's open period
		f.expense(t, f.personal, 200, dayOf(visaOpen.AddDate(0, -1, 0), 24), 1, "Café") // Visa's statement to pay
		s.CreateExpense(t, f.u, f.personal, map[string]any{
			"paymentMethodId": master.ID, "amount": 600, "spentAt": today, "installments": 2, "description": "Zapas",
		})
		f.do(t, f.u, http.MethodPost, "/"+ymOf(visaOpen)+"/adjustments",
			map[string]any{"kind": "fee", "description": "Seguro", "amount": 10, "currency": "USD"}).Status(t, http.StatusCreated)

		var c committed
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/credit-cards/committed?months=4", Token: f.u.Token}).
			Status(t, http.StatusOK).JSON(t, &c)
		if len(c.Months) != 4 || c.Months[0].PeriodYM != ymOf(start) || len(c.Cards) != 2 {
			t.Fatalf("committed = %+v", c)
		}

		want := map[string]map[string]float64{}
		add := func(m time.Time, cur string, v float64) {
			if want[ymOf(m)] == nil {
				want[ymOf(m)] = map[string]float64{}
			}
			want[ymOf(m)][cur] += v
		}
		for i := range 3 {
			add(visaOpen.AddDate(0, i, 0), "ARS", 1000)
		}
		add(visaOpen, "USD", 10)
		add(masterOpen, "ARS", 300)
		add(masterOpen.AddDate(0, 1, 0), "ARS", 300)
		for _, m := range c.Months {
			got := map[string]float64{}
			for _, tot := range m.Totals {
				got[tot.Currency] = tot.Amount
			}
			if fmt.Sprint(got) != fmt.Sprint(nonNil(want[m.PeriodYM])) {
				t.Errorf("%s = %v, want %v", m.PeriodYM, got, want[m.PeriodYM])
			}
		}

		for _, card := range c.Cards {
			switch card.Name {
			case "Visa":
				if card.OpenPeriodYM != ymOf(visaOpen) || card.ToPay == nil || card.ToPay.PeriodYM != ymOf(visaOpen.AddDate(0, -1, 0)) {
					t.Errorf("visa = %+v", card)
				} else {
					wantTotals(t, card.ToPay.Totals, amount{"ARS", 200})
				}
				for _, m := range card.Months {
					if m.PeriodYM < ymOf(visaOpen) && len(m.Totals) > 0 {
						t.Errorf("visa counts a month before its open period: %+v", m)
					}
				}
			case "Master":
				if card.OpenPeriodYM != ymOf(masterOpen) || card.ToPay != nil {
					t.Errorf("master = %+v", card)
				}
			default:
				t.Errorf("unexpected card %q", card.Name)
			}
		}

		// Paying the statement takes it off "to pay".
		f.do(t, f.u, http.MethodPost, "/"+ymOf(visaOpen.AddDate(0, -1, 0))+"/pay", map[string]any{}).Status(t, http.StatusOK)
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/credit-cards/committed", Token: f.u.Token}).
			Status(t, http.StatusOK).JSON(t, &c)
		if len(c.Months) != 6 {
			t.Errorf("default months = %d, want 6", len(c.Months))
		}
		for _, card := range c.Cards {
			if card.ToPay != nil {
				t.Errorf("%s still has a statement to pay: %+v", card.Name, card.ToPay)
			}
		}
	})

	t.Run("months is 1 to 12; a user without cards gets empty months", func(t *testing.T) {
		u := s.Register(t)
		for _, q := range []string{"0", "13", "x"} {
			s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/credit-cards/committed?months=" + q, Token: u.Token}).
				WantError(t, http.StatusUnprocessableEntity, "validation", "months")
		}
		var c committed
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/credit-cards/committed?months=12", Token: u.Token}).
			Status(t, http.StatusOK).JSON(t, &c)
		if len(c.Cards) != 0 || len(c.Months) != 12 || len(c.Months[0].Totals) != 0 {
			t.Errorf("committed = %+v", c)
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/credit-cards/committed"}).Status(t, http.StatusUnauthorized)
	})

	t.Run("installment plans: expenses in installments still being billed", func(t *testing.T) {
		f := newFixture(t, s)
		open := openFor(24)
		tv := f.expense(t, f.personal, 3000, today, 3, "Tele")
		heladera := f.expense(t, f.hogar, 4000, dayOf(open.AddDate(0, -1, 0), 10), 4, "Heladera")
		f.expense(t, f.personal, 900, "2026-01-10", 3, "Viejo") // fully billed
		f.expense(t, f.personal, 100, today, 1, "Café")         // one installment

		var p plans
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/payment-methods/" + f.card + "/credit-card/installment-plans", Token: f.u.Token}).
			Status(t, http.StatusOK).JSON(t, &p)
		if len(p.Plans) != 2 {
			t.Fatalf("plans = %+v", p.Plans)
		}
		byID := map[string]int{}
		for i, pl := range p.Plans {
			byID[pl.ExpenseID] = i
		}
		got := p.Plans[byID[tv]]
		if got.Description != "Tele" || got.NextInstallmentNumber != 1 || got.NextInstallmentAmount != 1000 ||
			got.TotalInstallments != 3 || got.LastPeriodYM != ymOf(open.AddDate(0, 2, 0)) || got.Currency != "ARS" {
			t.Errorf("tele = %+v", got)
		}
		got = p.Plans[byID[heladera]]
		if got.NextInstallmentNumber != 2 || got.NextInstallmentAmount != 1000 || got.LastPeriodYM != ymOf(open.AddDate(0, 2, 0)) {
			t.Errorf("heladera = %+v", got)
		}

		member := s.AddMember(t, f.u, f.hogar)
		res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/payment-methods/" + f.card + "/credit-card/installment-plans", Token: member.Token})
		if res.Code != http.StatusNotFound && res.Code != http.StatusForbidden {
			t.Errorf("plans by a member = %d", res.Code)
		}
		var c committed
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/credit-cards/committed", Token: member.Token}).
			Status(t, http.StatusOK).JSON(t, &c)
		if len(c.Cards) != 0 {
			t.Errorf("a member sees the owner's cards: %+v", c.Cards)
		}
	})

	t.Run("drafts stay out of statements until confirmed", func(t *testing.T) {
		f := newFixture(t, s)
		f.expense(t, f.personal, 1000, "2026-01-10", 1, "Tele")
		draft := f.expense(t, f.personal, 500, "2026-01-12", 1, "Luz (borrador)")
		if _, err := s.Pool.Exec(t.Context(), `UPDATE expenses SET status = 'draft' WHERE id = $1`, draft); err != nil {
			t.Fatal(err)
		}

		jan := f.detail(t, "2026-01")
		wantTotals(t, jan.Totals, amount{"ARS", 1000})
		if len(jan.Installments) != 1 || jan.InstallmentCount != 1 {
			t.Errorf("january = %+v", jan)
		}

		f.do(t, f.u, http.MethodPost, "/2026-01/pay", map[string]any{}).Status(t, http.StatusOK)
		var paid bool
		if err := s.Pool.QueryRow(t.Context(), `SELECT is_paid FROM expense_installments WHERE expense_id = $1`, draft).Scan(&paid); err != nil {
			t.Fatal(err)
		}
		if paid {
			t.Error("paying the statement marked a draft's installment paid")
		}
	})
}

func nonNil(m map[string]float64) map[string]float64 {
	if m == nil {
		return map[string]float64{}
	}
	return m
}
