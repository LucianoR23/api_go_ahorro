package reports_test

import (
	"net/http"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

type realMonthlyJSON struct {
	Spent      float64  `json:"spentThisMonth"`
	SpentReal  *float64 `json:"spentRealThisMonth"`
	CPIThrough string   `json:"cpiThrough"`
}

type realTrendsJSON struct {
	trendsJSON
	Adjust     string `json:"adjust"`
	CPIThrough string `json:"cpiThrough"`
}

// Plan 5.4: spending in today's pesos. Each month's pesos times the last
// published index over the month's; months after the last published one
// count as that one.
func TestReportsInRealTerms(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	// Index through March: Jan 100, Feb 110, Mar 125.
	if _, err := s.Pool.Exec(t.Context(), `INSERT INTO cpi_index (month, value) VALUES
		('2026-01-01', 100), ('2026-02-01', 110), ('2026-03-01', 125)`); err != nil {
		t.Fatal(err)
	}
	h.spend(t, 10000, "2026-01-15", nil) // $ 12.500 of today
	h.income(t, 50000, "2026-01-05")     // $ 62.500 of today
	h.spend(t, 20000, "2026-02-10", nil) // 20.000 x 125 / 110 = $ 22.727,27
	h.spend(t, 5000, "2026-03-05", nil)  // the last published month: as is
	h.spend(t, 7000, "2026-04-02", nil)  // not published yet: as is

	t.Run("trends in today's pesos", func(t *testing.T) {
		var tr realTrendsJSON
		h.get(t, "/reports/trends?months=4&at=2026-04-15&adjust=cpi", &tr)
		if tr.Adjust != "cpi" || tr.CPIThrough != "2026-03" || len(tr.Points) != 4 {
			t.Fatalf("header = %q %q, %d points", tr.Adjust, tr.CPIThrough, len(tr.Points))
		}
		want := []float64{12500, 22727.27, 5000, 7000}
		for i, p := range tr.Points {
			if p.SpentTotal != want[i] {
				t.Errorf("%s spent = %v, want %v", p.Month, p.SpentTotal, want[i])
			}
		}
		if tr.Points[0].Income != 62500 {
			t.Errorf("January income = %v, want 62500", tr.Points[0].Income)
		}
	})

	t.Run("trends are nominal by default", func(t *testing.T) {
		var tr realTrendsJSON
		h.get(t, "/reports/trends?months=4&at=2026-04-15", &tr)
		if tr.Adjust != "" || tr.CPIThrough != "" || tr.Points[0].SpentTotal != 10000 {
			t.Errorf("nominal trends = %q %q %+v", tr.Adjust, tr.CPIThrough, tr.Points)
		}
	})

	t.Run("a past month says what it is in today's pesos", func(t *testing.T) {
		var m realMonthlyJSON
		h.get(t, "/reports/monthly?month=2026-02", &m)
		if m.Spent != 20000 || m.SpentReal == nil || *m.SpentReal != 22727.27 || m.CPIThrough != "2026-03" {
			t.Errorf("February = %+v", m)
		}
	})

	t.Run("the last published month and later ones do not", func(t *testing.T) {
		for _, month := range []string{"2026-03", "2026-04"} {
			var m realMonthlyJSON
			h.get(t, "/reports/monthly?month="+month, &m)
			if m.SpentReal != nil || m.CPIThrough != "" {
				t.Errorf("%s = %+v", month, m)
			}
		}
	})

	t.Run("not in dollars", func(t *testing.T) {
		if _, err := s.Pool.Exec(t.Context(), `INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell)
			VALUES ('USD', 'bolsa', now(), 1000, 1000, 1000)`); err != nil {
			t.Fatal(err)
		}
		var m realMonthlyJSON
		h.get(t, "/reports/monthly?month=2026-02&currency=USD", &m)
		if m.SpentReal != nil {
			t.Errorf("dollars monthly = %+v", m)
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/reports/trends?adjust=cpi&currency=USD", Token: h.member.Token, Household: h.id}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "adjust")
	})

	t.Run("bad adjust", func(t *testing.T) {
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/reports/trends?adjust=usd", Token: h.member.Token, Household: h.id}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "adjust")
	})

	t.Run("a space kept in dollars", func(t *testing.T) {
		d := newHouse(t, s)
		if _, err := s.Pool.Exec(t.Context(), `UPDATE households SET base_currency = 'USD' WHERE id = $1`, d.id); err != nil {
			t.Fatal(err)
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/reports/trends?adjust=cpi", Token: d.member.Token, Household: d.id}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "adjust")
	})
}

func TestReportsInRealTermsWithoutIndex(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	h.spend(t, 10000, "2026-01-15", nil)

	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/reports/trends?adjust=cpi", Token: h.member.Token, Household: h.id}).
		WantError(t, http.StatusConflict, "conflict", "")
	var m realMonthlyJSON
	h.get(t, "/reports/monthly?month=2026-01", &m)
	if m.Spent != 10000 || m.SpentReal != nil {
		t.Errorf("monthly without index = %+v", m)
	}
}
