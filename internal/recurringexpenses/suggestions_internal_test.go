package recurringexpenses

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Subscription detection rule (plan 7.3).

func cand(desc, cur string, amount int64, date string) candidate {
	d, _ := time.Parse("2006-01-02", date)
	return candidate{Description: desc, Currency: cur, Amount: decimal.NewFromInt(amount), SpentAt: d, PaymentMethodID: uuid.Nil}
}

func TestNormalizeDescription(t *testing.T) {
	for in, want := range map[string]string{
		"Netflix":            "netflix",
		"  NETFLIX. ":        "netflix",
		"Gimnasio  Megatlón": "gimnasio megatlon",
		"Personal 5GB":       "personal 5gb",
		"--":                 "",
	} {
		if got := normalizeDescription(in); got != want {
			t.Errorf("normalizeDescription(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSuggestionWindow(t *testing.T) {
	from, to := suggestionWindow(time.Date(2026, 1, 11, 0, 0, 0, 0, time.UTC))
	if from.Format("2006-01-02") != "2025-10-01" || to.Format("2006-01-02") != "2026-02-01" {
		t.Fatalf("window = %s..%s", from, to)
	}
}

func TestDetectSuggestions(t *testing.T) {
	cands := []candidate{
		// Three months, a price increase within 25%: suggested, last values win.
		cand("Netflix", "ARS", 8900, "2026-08-05"),
		cand("netflix", "ARS", 8900, "2026-09-05"),
		cand("NETFLIX ", "ARS", 10500, "2026-10-06"),
		// Same name in dollars is another key; only two months: not suggested.
		cand("Netflix", "USD", 10, "2026-09-05"),
		cand("Netflix", "USD", 10, "2026-10-05"),
		// Several times a month: groceries, not a subscription.
		cand("Súper", "ARS", 30000, "2026-08-02"),
		cand("Súper", "ARS", 31000, "2026-08-20"),
		cand("Súper", "ARS", 29000, "2026-09-03"),
		cand("Súper", "ARS", 32000, "2026-10-03"),
		// Amounts too far apart (over 25%).
		cand("Farmacia", "ARS", 5000, "2026-08-10"),
		cand("Farmacia", "ARS", 9000, "2026-09-10"),
		cand("Farmacia", "ARS", 5200, "2026-10-10"),
		// Already a recurring expense.
		cand("Alquiler", "ARS", 500000, "2026-08-01"),
		cand("Alquiler", "ARS", 500000, "2026-09-01"),
		cand("Alquiler", "ARS", 500000, "2026-10-01"),
		// Dismissed.
		cand("Gimnasio", "ARS", 40000, "2026-08-04"),
		cand("Gimnasio", "ARS", 40000, "2026-09-04"),
		cand("Gimnasio", "ARS", 40000, "2026-10-04"),
		// Four months, more recent: listed first.
		cand("Spotify", "ARS", 4500, "2026-08-12"),
		cand("Spotify", "ARS", 4500, "2026-09-12"),
		cand("Spotify", "ARS", 4500, "2026-10-12"),
		cand("Spotify", "ARS", 5200, "2026-11-10"),
	}
	recurring := []domain.RecurringExpense{{Description: "alquiler"}}
	dismissed := map[string]bool{"ARS:gimnasio": true}

	got := detectSuggestions(cands, recurring, dismissed)
	if len(got) != 2 {
		t.Fatalf("got %d suggestions: %+v", len(got), got)
	}
	sp, nf := got[0], got[1]
	if sp.Key != "ARS:spotify" || sp.Months != 4 || !sp.Amount.Equal(decimal.NewFromInt(5200)) || sp.DayOfMonth != 10 {
		t.Errorf("spotify = %+v", sp)
	}
	if nf.Key != "ARS:netflix" || nf.Description != "NETFLIX" || nf.Months != 3 ||
		!nf.MinAmount.Equal(decimal.NewFromInt(8900)) || !nf.MaxAmount.Equal(decimal.NewFromInt(10500)) || nf.DayOfMonth != 6 {
		t.Errorf("netflix = %+v", nf)
	}
}
