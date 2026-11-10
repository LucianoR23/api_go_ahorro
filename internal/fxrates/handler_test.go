package fxrates_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

func TestCurrentRatesEndpoint(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	u := s.Register(t)

	// No rates fetched yet (background jobs are off in apitest): empty list, not null.
	res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/exchange-rates/current", Token: u.Token}).Status(t, http.StatusOK)
	if strings.TrimSpace(string(res.Body)) != "[]" {
		t.Errorf("body = %s", res.Body)
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/exchange-rates/current"}).WantError(t, http.StatusUnauthorized, "unauthorized", "")
}

// Plan 5.2: one quote per day, the day's last, in the app's timezone.
func TestRateHistoryEndpoint(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	u := s.Register(t)
	if _, err := s.Pool.Exec(t.Context(), `INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell) VALUES
		('USD', 'bolsa', now() - interval '3 days', 1490, 1480, 1500),
		('USD', 'bolsa', now() - interval '1 day' - interval '2 hours', 1540, 1530, 1550),
		('USD', 'bolsa', now() - interval '1 day', 1560, 1550, 1570),
		('USD', 'bolsa', now() - interval '40 days', 1300, 1290, 1310),
		('USD', 'blue',  now() - interval '1 day', 1600, 1590, 1610)`); err != nil {
		t.Fatal(err)
	}
	get := func(query string) *apitest.Res {
		t.Helper()
		return s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/exchange-rates/history" + query, Token: u.Token})
	}

	var h struct {
		Currency string `json:"currency"`
		Source   string `json:"source"`
		Points   []struct {
			Day      string  `json:"day"`
			RateSell float64 `json:"rateSell"`
		} `json:"points"`
	}
	// Defaults: USD, MEP, 30 days. The 40-day-old quote is out; yesterday's
	// two quotes collapse into the later one (unless they straddle midnight,
	// so only the last point is asserted).
	get("").Status(t, http.StatusOK).JSON(t, &h)
	if h.Currency != "USD" || h.Source != "bolsa" || len(h.Points) < 2 || len(h.Points) > 3 {
		t.Fatalf("history = %+v", h)
	}
	if h.Points[0].RateSell != 1500 || h.Points[len(h.Points)-1].RateSell != 1570 || h.Points[0].Day >= h.Points[len(h.Points)-1].Day {
		t.Errorf("points = %+v, want oldest first ending at 1570", h.Points)
	}

	get("?source=blue&days=365").Status(t, http.StatusOK).JSON(t, &h)
	if len(h.Points) != 1 || h.Points[0].RateSell != 1610 {
		t.Errorf("blue history = %+v", h.Points)
	}
	get("?currency=EUR&source=tarjeta").Status(t, http.StatusOK).JSON(t, &h)
	if len(h.Points) != 0 {
		t.Errorf("a source without quotes = %+v, want empty", h.Points)
	}

	get("?days=0").Status(t, http.StatusOK)
	get("?days=366").WantError(t, http.StatusUnprocessableEntity, "validation", "days")
	get("?days=x").WantError(t, http.StatusUnprocessableEntity, "validation", "days")
	get("?currency=BRL").WantError(t, http.StatusUnprocessableEntity, "validation", "currency")
	get("?source=futuro").WantError(t, http.StatusUnprocessableEntity, "validation", "source")
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/exchange-rates/history"}).WantError(t, http.StatusUnauthorized, "unauthorized", "")
}
