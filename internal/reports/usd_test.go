package reports_test

import (
	"net/http"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

type usdMonthlyJSON struct {
	monthlyJSON
	Currency   string   `json:"currency"`
	RateSource string   `json:"rateSource"`
	RateMode   string   `json:"rateMode"`
	USDRate    *float64 `json:"usdRate"`
	Income     float64  `json:"incomeThisMonth"`
}

// Plan 5.3: reports in dollars. Pesos go at the space's dollar of their day
// (before the first quote, the oldest one) or at today's; a movement loaded
// in dollars keeps its amount.
func TestReportsInUSD(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	// MEP (the default): $ 1.000 from Mar 5, $ 1.250 from Mar 12, $ 1.600 now.
	// A blue quote shows the space's dollar is the one used.
	if _, err := s.Pool.Exec(t.Context(), `INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell) VALUES
		('USD', 'bolsa', '2026-03-05 12:00-03', 990, 980, 1000),
		('USD', 'bolsa', '2026-03-12 12:00-03', 1240, 1230, 1250),
		('USD', 'bolsa', now(), 1590, 1580, 1600),
		('USD', 'blue',  '2026-03-01 12:00-03', 2000, 1990, 2010)`); err != nil {
		t.Fatal(err)
	}
	h.spend(t, 10000, "2026-03-10", nil)                            // US$ 10 at Mar 10's $ 1.000
	h.spend(t, 25000, "2026-03-12", nil)                            // US$ 20 at Mar 12's $ 1.250
	h.spend(t, 50, "2026-03-12", map[string]any{"currency": "USD"}) // US$ 50, $ 80.000 at today's
	h.spend(t, 3000, "2026-02-01", nil)                             // before any quote: US$ 3
	h.income(t, 160000, "2026-03-01")                               // US$ 160

	t.Run("pesos by default", func(t *testing.T) {
		var m usdMonthlyJSON
		h.get(t, "/reports/monthly?month=2026-03", &m)
		if m.Currency != "ARS" || m.Spent != 115000 || m.Income != 160000 || m.RateSource != "" || m.USDRate != nil {
			t.Errorf("pesos report = %+v", m)
		}
	})

	t.Run("dollars at each movement's day", func(t *testing.T) {
		var m usdMonthlyJSON
		h.get(t, "/reports/monthly?month=2026-03&currency=USD", &m)
		if m.Currency != "USD" || m.RateSource != "bolsa" || m.RateMode != "day" || m.USDRate == nil || *m.USDRate != 1600 {
			t.Fatalf("usd header = %+v", m)
		}
		if m.Spent != 80 || m.Income != 160 || m.Billed != 80 || m.Due != 80 {
			t.Errorf("usd totals: spent %v income %v billed %v due %v, want 80/160/80/80", m.Spent, m.Income, m.Billed, m.Due)
		}
		if len(m.ByCategory) == 0 || m.ByCategory[0].Total != 80 || len(m.ByPayer) != 1 || m.ByPayer[0].Total != 80 {
			t.Errorf("usd breakdowns = %+v / %+v", m.ByCategory, m.ByPayer)
		}
		if m.FixedVariable.VariableTotal != 80 {
			t.Errorf("usd variable = %v", m.FixedVariable.VariableTotal)
		}
	})

	t.Run("dollars at today's", func(t *testing.T) {
		var m usdMonthlyJSON
		h.get(t, "/reports/monthly?month=2026-03&currency=USD&rate=today", &m)
		// 10.000 / 1.600 + 25.000 / 1.600 + 50 = 71,875
		if m.RateMode != "today" || m.Spent != 71.88 {
			t.Errorf("today report: mode %q spent %v, want today 71.88", m.RateMode, m.Spent)
		}
	})

	t.Run("trends in dollars", func(t *testing.T) {
		var tr trendsJSON
		h.get(t, "/reports/trends?months=2&at=2026-03-15&currency=USD", &tr)
		if len(tr.Points) != 2 || tr.Points[0].SpentTotal != 3 || tr.Points[1].SpentTotal != 80 || tr.Points[1].Income != 160 {
			t.Errorf("usd trends = %+v", tr.Points)
		}
	})

	t.Run("bad params", func(t *testing.T) {
		do := func(path string) *apitest.Res {
			return s.Do(t, apitest.Req{Method: http.MethodGet, Path: path, Token: h.member.Token, Household: h.id})
		}
		do("/reports/monthly?currency=XYZ").WantError(t, http.StatusUnprocessableEntity, "validation", "currency")
		do("/reports/trends?currency=USD&rate=ayer").WantError(t, http.StatusUnprocessableEntity, "validation", "rate")
	})

	t.Run("a dollar without quotes", func(t *testing.T) {
		if _, err := s.Pool.Exec(t.Context(), `UPDATE households SET fx_source = 'tarjeta' WHERE id = $1`, h.id); err != nil {
			t.Fatal(err)
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/reports/monthly?currency=USD", Token: h.member.Token, Household: h.id}).
			WantError(t, http.StatusConflict, "conflict", "")
	})
}
