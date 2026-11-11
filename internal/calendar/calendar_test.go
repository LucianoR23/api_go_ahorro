package calendar_test

// GET /calendar against a real database (plan 7.2): the user's card dates
// and the space's recurring expenses and incomes.

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

type monthJSON struct {
	Month  string `json:"month"`
	Events []struct {
		Date   string   `json:"date"`
		Kind   string   `json:"kind"`
		Title  string   `json:"title"`
		Amount *float64 `json:"amount"`
		Totals []struct {
			Currency string  `json:"currency"`
			Amount   float64 `json:"amount"`
		} `json:"totals"`
		Status           string `json:"status"`
		InstallmentCount int    `json:"installmentCount"`
		PaymentMethodID  string `json:"paymentMethodId"`
		PeriodYM         string `json:"periodYm"`
		Variable         bool   `json:"variable"`
	} `json:"events"`
}

// openFor: the closing month open today for a card closing on day.
func openFor(day int) time.Time {
	today := dates.Today()
	open := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	if today.Day() > day {
		open = open.AddDate(0, 1, 0)
	}
	return open
}

func TestCalendar(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	u := s.Register(t)
	visa := s.CreateCreditCard(t, u, "Visa", 24, 3, "2026-01-24", "2026-02-03")
	cash := s.Cash(t, u)
	var spaces []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/households", Token: u.Token}).Status(t, http.StatusOK).JSON(t, &spaces)
	var personal string
	for _, sp := range spaces {
		if sp.Kind == "personal" {
			personal = sp.ID
		}
	}
	hogar := s.CreateHousehold(t, u, "Casa").ID
	today := dates.Today().Format(time.DateOnly)

	// 12 installments of $ 1.000 from Visa's open period: three statements
	// on, one is due on the 3rd and the next closes on the 24th.
	s.CreateExpense(t, u, personal, map[string]any{
		"paymentMethodId": visa.ID, "amount": 12000, "spentAt": today, "installments": 12, "description": "Tele",
	})
	month := openFor(24).AddDate(0, 3, 0)
	ym := month.Format("2006-01")
	post := func(path, household string, body map[string]any) {
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: path, Token: u.Token, Household: household, Body: body}).Status(t, http.StatusCreated)
	}
	post("/recurring-expenses", personal, map[string]any{
		"paymentMethodId": cash.ID, "amount": 30000, "currency": "ARS", "description": "Luz", "installments": 1,
		"frequency": "monthly", "dayOfMonth": 10, "startsAt": today, "amountIsVariable": true,
	})
	post("/recurring-incomes", personal, map[string]any{
		"amount": 1500, "currency": "USD", "description": "Sueldo", "source": "salary",
		"frequency": "monthly", "dayOfMonth": 1, "startsAt": today,
	})
	post("/recurring-expenses", hogar, map[string]any{
		"paymentMethodId": cash.ID, "amount": 250000, "currency": "ARS", "description": "Alquiler", "installments": 1,
		"frequency": "monthly", "dayOfMonth": 5, "startsAt": today,
	})

	get := func(household, query string) monthJSON {
		var m monthJSON
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/calendar" + query, Token: u.Token, Household: household}).
			Status(t, http.StatusOK).JSON(t, &m)
		return m
	}

	t.Run("Mi plata: salary, card due, recurring expense, card closing", func(t *testing.T) {
		m := get(personal, "?month="+ym)
		if m.Month != ym || len(m.Events) != 4 {
			t.Fatalf("month %s events = %+v", m.Month, m.Events)
		}
		day := func(d int) string {
			return time.Date(month.Year(), month.Month(), d, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
		}
		salary, due, luz, closing := m.Events[0], m.Events[1], m.Events[2], m.Events[3]
		if salary.Kind != "recurring_income" || salary.Date != day(1) || salary.Title != "Sueldo" || salary.Amount == nil || *salary.Amount != 1500 {
			t.Errorf("salary = %+v", salary)
		}
		if due.Kind != "card_due" || due.Date != day(3) || due.Title != "Visa" || due.PaymentMethodID != visa.ID ||
			due.PeriodYM != month.AddDate(0, -1, 0).Format("2006-01") || len(due.Totals) != 1 || due.Totals[0].Amount != 1000 ||
			due.Status != "closed" && due.Status != "open" || due.InstallmentCount != 1 {
			t.Errorf("card due = %+v", due)
		}
		if luz.Kind != "recurring_expense" || luz.Date != day(10) || !luz.Variable || luz.Amount == nil || *luz.Amount != 30000 {
			t.Errorf("recurring expense = %+v", luz)
		}
		if closing.Kind != "card_closing" || closing.Date != day(24) || closing.PeriodYM != ym || len(closing.Totals) != 0 {
			t.Errorf("card closing = %+v", closing)
		}
	})

	t.Run("Hogar: the space's recurring series and the same user's cards", func(t *testing.T) {
		m := get(hogar, "?month="+ym)
		var kinds []string
		for _, e := range m.Events {
			kinds = append(kinds, e.Kind+":"+e.Title)
		}
		want := []string{"card_due:Visa", "recurring_expense:Alquiler", "card_closing:Visa"}
		if len(kinds) != len(want) {
			t.Fatalf("hogar events = %v, want %v", kinds, want)
		}
		for i := range want {
			if kinds[i] != want[i] {
				t.Errorf("hogar events = %v, want %v", kinds, want)
				break
			}
		}
	})

	t.Run("this month by default", func(t *testing.T) {
		m := get(personal, "")
		if m.Month != dates.Today().Format("2006-01") || m.Events == nil {
			t.Errorf("default month = %+v", m)
		}
	})

	t.Run("bad month and other people's spaces", func(t *testing.T) {
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/calendar?month=2027-13", Token: u.Token, Household: personal}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "month")
		other := s.Register(t)
		res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/calendar", Token: other.Token, Household: personal})
		if res.Code == http.StatusOK {
			t.Error("a stranger read someone else's calendar")
		}
	})
}
