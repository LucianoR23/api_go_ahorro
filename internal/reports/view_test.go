package reports_test

import (
	"net/http"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

type viewMonthlyJSON struct {
	usdMonthlyJSON
	Rate  *float64 `json:"rate"`
	Scale int      `json:"scale"`
}

// Plan 5.6: reports in other currencies and crypto, at today's rate. A fiat
// currency goes through the space's dollar; a crypto has its market price.
// A movement loaded in the currency shown keeps its amount.
func TestReportsInOtherCurrencies(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	// MEP (the space's dollar) $ 1.600, official $ 1.000: the official euro
	// of $ 1.100 is $ 1.760 at MEP. A bitcoin costs more than 99.999.999
	// pesos, which the old rate columns could not hold.
	if _, err := s.Pool.Exec(t.Context(), `INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell) VALUES
		('USD', 'bolsa',   now(), 1590, 1580, 1600),
		('USD', 'oficial', now(), 990, 980, 1000),
		('EUR', 'oficial', now(), 1090, 1080, 1100),
		('BRL', 'oficial', now(), 199, 198, 200),
		('BTC', 'cripto',  now(), 100000000, 100000000, 100000000)`); err != nil {
		t.Fatal(err)
	}
	h.spend(t, 22000, "2026-03-10", nil)                            // € 12,50 at $ 1.760
	h.spend(t, 10, "2026-03-12", map[string]any{"currency": "EUR"}) // € 10, $ 17.600
	h.income(t, 160000, "2026-03-01")                               // € 90,91

	t.Run("euros at today's euro through the space's dollar", func(t *testing.T) {
		var m viewMonthlyJSON
		h.get(t, "/reports/monthly?month=2026-03&currency=EUR", &m)
		if m.Currency != "EUR" || m.RateSource != "bolsa" || m.RateMode != "today" || m.Rate == nil || *m.Rate != 1760 ||
			m.USDRate != nil || m.Scale != 2 {
			t.Fatalf("eur header = %+v", m)
		}
		if m.Spent != 22.5 || m.Income != 90.91 || m.Due != 22.5 {
			t.Errorf("eur totals: spent %v income %v due %v, want 22.5/90.91/22.5", m.Spent, m.Income, m.Due)
		}
	})

	t.Run("reales: no euro movement keeps its amount", func(t *testing.T) {
		var m viewMonthlyJSON
		h.get(t, "/reports/monthly?month=2026-03&currency=BRL", &m)
		// Real at MEP: 200 × 1.600 / 1.000 = 320. (22.000 + 17.600) / 320.
		if m.Currency != "BRL" || m.Rate == nil || *m.Rate != 320 || m.Spent != 123.75 {
			t.Errorf("brl report: rate %v spent %v, want 320 / 123.75", m.Rate, m.Spent)
		}
	})

	t.Run("bitcoin with eight decimals at its market price", func(t *testing.T) {
		var m viewMonthlyJSON
		h.get(t, "/reports/monthly?month=2026-03&currency=BTC", &m)
		if m.Currency != "BTC" || m.RateSource != "cripto" || m.Scale != 8 || m.Rate == nil || *m.Rate != 100000000 {
			t.Fatalf("btc header = %+v", m)
		}
		if m.Spent != 0.000396 || m.Income != 0.0016 {
			t.Errorf("btc totals: spent %v income %v, want 0.000396 / 0.0016", m.Spent, m.Income)
		}
	})

	t.Run("trends in euros", func(t *testing.T) {
		var tr trendsJSON
		h.get(t, "/reports/trends?months=1&at=2026-03-15&currency=EUR", &tr)
		if len(tr.Points) != 1 || tr.Points[0].SpentTotal != 22.5 || tr.Points[0].Income != 90.91 {
			t.Errorf("eur trends = %+v", tr.Points)
		}
	})

	t.Run("bad requests", func(t *testing.T) {
		do := func(path string) *apitest.Res {
			return s.Do(t, apitest.Req{Method: http.MethodGet, Path: path, Token: h.member.Token, Household: h.id})
		}
		do("/reports/monthly?currency=usd").WantError(t, http.StatusUnprocessableEntity, "validation", "currency")
		do("/reports/trends?currency=EUR&adjust=cpi").WantError(t, http.StatusUnprocessableEntity, "validation", "adjust")
		// No ether quote yet (no CoinGecko key, say).
		do("/reports/monthly?currency=ETH").WantError(t, http.StatusConflict, "conflict", "")
	})
}
