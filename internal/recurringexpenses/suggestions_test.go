package recurringexpenses_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

// Subscription suggestions over HTTP (plan 7.3).

type suggestionJSON struct {
	Key             string  `json:"key"`
	Description     string  `json:"description"`
	Currency        string  `json:"currency"`
	Amount          float64 `json:"amount"`
	MinAmount       float64 `json:"minAmount"`
	MaxAmount       float64 `json:"maxAmount"`
	Months          int     `json:"months"`
	DayOfMonth      int     `json:"dayOfMonth"`
	LastSpentAt     string  `json:"lastSpentAt"`
	PaymentMethodID string  `json:"paymentMethodId"`
}

func TestRecurringSuggestions(t *testing.T) {
	s := apitest.New(t)
	h := newHouse(t, s)
	first := dates.Today()
	first = time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	day := func(monthsAgo int) string { return first.AddDate(0, -monthsAgo, 4).Format("2006-01-02") }

	for i, amount := range []int{8900, 8900, 9900} {
		s.CreateExpense(t, h.owner, h.id, map[string]any{
			"paymentMethodId": h.cash, "amount": amount, "description": "Netflix", "spentAt": day(3 - i),
		})
	}
	// One month only: not suggested.
	s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": h.cash, "amount": 5000, "description": "Farmacia", "spentAt": day(1)})

	list := func(t *testing.T) []suggestionJSON {
		t.Helper()
		var out []suggestionJSON
		h.do(t, h.member, http.MethodGet, "/recurring-expenses/suggestions", nil).Status(t, http.StatusOK).JSON(t, &out)
		return out
	}

	got := list(t)
	if len(got) != 1 {
		t.Fatalf("suggestions = %+v", got)
	}
	nf := got[0]
	if nf.Key != "ARS:netflix" || nf.Description != "Netflix" || nf.Amount != 9900 || nf.MinAmount != 8900 ||
		nf.Months != 3 || nf.DayOfMonth != 5 || nf.LastSpentAt != day(1) || nf.PaymentMethodID != h.cash {
		t.Errorf("netflix = %+v", nf)
	}

	t.Run("a bad key is a validation error", func(t *testing.T) {
		h.do(t, h.owner, http.MethodPost, "/recurring-expenses/suggestions/dismiss", map[string]any{"key": " "}).
			Status(t, http.StatusUnprocessableEntity)
	})

	t.Run("dismissed by one member, gone for the whole space", func(t *testing.T) {
		h.do(t, h.owner, http.MethodPost, "/recurring-expenses/suggestions/dismiss", map[string]any{"key": nf.Key}).
			Status(t, http.StatusNoContent)
		// Twice is fine.
		h.do(t, h.owner, http.MethodPost, "/recurring-expenses/suggestions/dismiss", map[string]any{"key": nf.Key}).
			Status(t, http.StatusNoContent)
		if got := list(t); len(got) != 0 {
			t.Fatalf("after dismissing: %+v", got)
		}
	})
}

func TestRecurringSuggestionsSkipExistingSeries(t *testing.T) {
	s := apitest.New(t)
	h := newHouse(t, s)
	first := dates.Today()
	first = time.Date(first.Year(), first.Month(), 1, 0, 0, 0, 0, time.UTC)
	for m := 3; m >= 1; m-- {
		s.CreateExpense(t, h.owner, h.id, map[string]any{
			"paymentMethodId": h.cash, "amount": 4500, "description": "Spotify", "spentAt": first.AddDate(0, -m, 11).Format("2006-01-02"),
		})
	}
	h.create(t, map[string]any{"description": "spotify", "amount": 4500})

	var out []suggestionJSON
	h.do(t, h.owner, http.MethodGet, "/recurring-expenses/suggestions", nil).Status(t, http.StatusOK).JSON(t, &out)
	if len(out) != 0 {
		t.Fatalf("already a recurring expense, still suggested: %+v", out)
	}
}
