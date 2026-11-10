package simulations_test

// "¿Me lo puedo comprar?" (plan 4.8) through the API.

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

type result struct {
	BaseCurrency string `json:"baseCurrency"`
	Purchase     struct {
		IsCredit     bool    `json:"isCredit"`
		AmountBase   float64 `json:"amountBase"`
		Installments []struct {
			Number     int     `json:"number"`
			AmountBase float64 `json:"amountBase"`
			PeriodYM   string  `json:"periodYm"`
			DueDate    *string `json:"dueDate"`
		} `json:"installments"`
	} `json:"purchase"`
	Months []struct {
		PeriodYM    string   `json:"periodYm"`
		CardsBefore float64  `json:"cardsBefore"`
		Purchase    float64  `json:"purchase"`
		CardsAfter  float64  `json:"cardsAfter"`
		Fixed       float64  `json:"fixed"`
		Income      float64  `json:"income"`
		Left        float64  `json:"left"`
		PctOfIncome *float64 `json:"pctOfIncome"`
	} `json:"months"`
	Card []struct {
		PeriodYM string  `json:"periodYm"`
		Before   float64 `json:"before"`
		After    float64 `json:"after"`
	} `json:"card"`
	Goals []struct {
		GoalType          string  `json:"goalType"`
		CategoryID        *string `json:"categoryId"`
		PerInstallment    float64 `json:"perInstallment"`
		PctPerInstallment float64 `json:"pctPerInstallment"`
	} `json:"goals"`
	HasIncome bool `json:"hasIncome"`
	TodayRate bool `json:"todayRate"`
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

func TestPurchase(t *testing.T) {
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
	do := func(method, path string, body any) *apitest.Res {
		t.Helper()
		return s.Do(t, apitest.Req{Method: method, Path: path, Body: body, Token: u.Token, Household: personal})
	}
	simulate := func(body map[string]any) *apitest.Res {
		t.Helper()
		return do(http.MethodPost, "/simulations/purchase", body)
	}
	var cats []struct {
		ID string `json:"id"`
	}
	do(http.MethodGet, "/categories", nil).Status(t, http.StatusOK).JSON(t, &cats)

	today := dates.Today().Format(time.DateOnly)
	s.CreateExpense(t, u, personal, map[string]any{
		"paymentMethodId": visa.ID, "amount": 3000, "spentAt": today, "installments": 3, "description": "Tele",
	})
	do(http.MethodPost, "/recurring-incomes", map[string]any{
		"amount": 500000, "currency": "ARS", "source": "salary", "description": "Sueldo",
		"frequency": "monthly", "dayOfMonth": 5, "startsAt": "2026-01-01",
	}).Status(t, http.StatusCreated)
	do(http.MethodPost, "/recurring-expenses", map[string]any{
		"paymentMethodId": cash.ID, "amount": 100000, "currency": "ARS", "description": "Alquiler", "installments": 1,
		"frequency": "monthly", "dayOfMonth": 10, "startsAt": "2026-01-01",
	}).Status(t, http.StatusCreated)
	for _, g := range []map[string]any{
		{"goalType": "total_limit", "targetAmount": 1000000},
		{"goalType": "category_limit", "categoryId": cats[0].ID, "targetAmount": 40000},
		{"goalType": "category_limit", "categoryId": cats[1].ID, "targetAmount": 40000},
	} {
		g["currency"], g["period"] = "ARS", "monthly"
		do(http.MethodPost, "/goals", g).Status(t, http.StatusCreated)
	}

	countExpenses := func() int {
		t.Helper()
		var list struct {
			TotalCount int `json:"totalCount"`
		}
		do(http.MethodGet, "/expenses", nil).Status(t, http.StatusOK).JSON(t, &list)
		return list.TotalCount
	}
	before := countExpenses() // the card purchase and the rent's first occurrence

	t.Run("three installments on the card over four months", func(t *testing.T) {
		var r result
		simulate(map[string]any{
			"paymentMethodId": visa.ID, "categoryId": cats[0].ID, "amount": 12000, "currency": "ARS", "installments": 3, "months": 4,
		}).Status(t, http.StatusOK).JSON(t, &r)

		open := openFor(24)
		if r.BaseCurrency != "ARS" || !r.Purchase.IsCredit || len(r.Purchase.Installments) != 3 || len(r.Months) != 4 ||
			len(r.Card) != 4 || r.Months[0].PeriodYM != open.Format("2006-01") || !r.HasIncome || r.TodayRate {
			t.Fatalf("result = %+v", r)
		}
		pcts := []float64{1, 1, 1, 0}
		for i, m := range r.Months {
			before, purchase := 1000.0, 4000.0
			if i == 3 {
				before, purchase = 0, 0
			}
			if m.PeriodYM != open.AddDate(0, i, 0).Format("2006-01") || m.CardsBefore != before || m.Purchase != purchase ||
				m.CardsAfter != before+purchase || m.Income != 500000 || m.Fixed != 100000 ||
				m.Left != 500000-before-purchase-100000 || m.PctOfIncome == nil || *m.PctOfIncome != pcts[i] {
				t.Errorf("month %d = %+v", i, m)
			}
			if c := r.Card[i]; c.Before != before || c.After != before+purchase {
				t.Errorf("card %d = %+v", i, c)
			}
		}
		if len(r.Goals) != 2 {
			t.Fatalf("goals = %+v, want the total limit and the purchase's category", r.Goals)
		}
		for _, g := range r.Goals {
			if g.GoalType == "category_limit" && (g.CategoryID == nil || *g.CategoryID != cats[0].ID || g.PctPerInstallment != 10) {
				t.Errorf("category goal = %+v", g)
			}
			if g.GoalType == "total_limit" && (g.PerInstallment != 4000 || g.PctPerInstallment != 0.4) {
				t.Errorf("total goal = %+v", g)
			}
		}
	})

	t.Run("saves nothing", func(t *testing.T) {
		if after := countExpenses(); after != before {
			t.Errorf("expenses after simulating = %d, want %d", after, before)
		}
	})

	t.Run("cash is one payment and no card", func(t *testing.T) {
		var r result
		simulate(map[string]any{"paymentMethodId": cash.ID, "amount": 5000, "currency": "ARS", "installments": 6}).
			Status(t, http.StatusOK).JSON(t, &r)
		if r.Purchase.IsCredit || len(r.Purchase.Installments) != 1 || r.Purchase.Installments[0].DueDate != nil ||
			len(r.Card) != 0 || len(r.Months) != 6 {
			t.Errorf("cash = %+v", r)
		}
	})

	t.Run("invalid input", func(t *testing.T) {
		other := s.Register(t)
		theirs := s.CreateCreditCard(t, other, "Ajena", 10, 20, "2026-01-10", "2026-01-20")
		simulate(map[string]any{"paymentMethodId": theirs.ID, "amount": 100, "currency": "ARS", "installments": 1}).
			WantError(t, http.StatusNotFound, "not_found", "")
		simulate(map[string]any{"paymentMethodId": visa.ID, "amount": 100, "currency": "ARS", "installments": 1, "months": 13}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "months")
		simulate(map[string]any{"paymentMethodId": visa.ID, "amount": 0, "currency": "ARS", "installments": 1}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "amount")
		simulate(map[string]any{"paymentMethodId": visa.ID, "amount": 100, "currency": "ARS", "installments": 1, "spentAt": "2030-01-01"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "spentAt")
	})
}
