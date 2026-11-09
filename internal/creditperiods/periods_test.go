package creditperiods_test

// Characterization tests of credit card period overrides (item 0.21d).

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

type periodJSON struct {
	CreditCardID string `json:"creditCardId"`
	PeriodYM     string `json:"periodYm"`
	ClosingDate  string `json:"closingDate"`
	DueDate      string `json:"dueDate"`
}

type statusJSON struct {
	NoPeriodsLoaded bool        `json:"noPeriodsLoaded"`
	DueDatePassed   bool        `json:"dueDatePassed"`
	LatestPeriod    *periodJSON `json:"latestPeriod"`
	OpenPeriod      struct {
		PeriodYM    string `json:"periodYm"`
		ClosingDate string `json:"closingDate"`
		DueDate     string `json:"dueDate"`
		Loaded      bool   `json:"loaded"`
	} `json:"openPeriod"`
}

func put(t testing.TB, s *apitest.Server, u apitest.User, pmID, ym, closing, due string) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodPut, Path: "/payment-methods/" + pmID + "/credit-card/periods/" + ym,
		Token: u.Token, Body: map[string]string{"closingDate": closing, "dueDate": due}})
}

func list(t testing.TB, s *apitest.Server, u apitest.User, pmID string) []periodJSON {
	t.Helper()
	var out []periodJSON
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/payment-methods/" + pmID + "/credit-card/periods", Token: u.Token}).
		Status(t, http.StatusOK).JSON(t, &out)
	return out
}

func status(t testing.TB, s *apitest.Server, u apitest.User, pmID string) statusJSON {
	t.Helper()
	var st statusJSON
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/payment-methods/" + pmID + "/credit-card/periods/status", Token: u.Token}).
		Status(t, http.StatusOK).JSON(t, &st)
	return st
}

func TestCreditCardPeriods(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("upsert, list, overwrite, delete", func(t *testing.T) {
		u := s.Register(t)
		pm := s.CreateCreditCard(t, u, "Visa", 25, 5, "2026-11-26", "2026-12-07")

		var p periodJSON
		put(t, s, u, pm.ID, "2026-12", "2026-12-23", "2027-01-04").Status(t, http.StatusOK).JSON(t, &p)
		if p.PeriodYM != "2026-12" || p.ClosingDate != "2026-12-23" || p.DueDate != "2027-01-04" || p.CreditCardID != pm.CreditCard.ID {
			t.Errorf("period = %+v", p)
		}
		put(t, s, u, pm.ID, "2026-12", "2026-12-22", "2027-01-05").Status(t, http.StatusOK)

		got := list(t, s, u, pm.ID)
		byYM := map[string]periodJSON{}
		for _, p := range got {
			byYM[p.PeriodYM] = p
		}
		if len(got) != 2 || byYM["2026-11"].DueDate != "2026-12-07" || byYM["2026-12"].ClosingDate != "2026-12-22" {
			t.Errorf("periods = %+v", got)
		}

		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/payment-methods/" + pm.ID + "/credit-card/periods/2026-12", Token: u.Token}).
			Status(t, http.StatusNoContent)
		if got := list(t, s, u, pm.ID); len(got) != 1 {
			t.Errorf("after delete = %+v", got)
		}
		// Deleting a period that does not exist is a silent 204.
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/payment-methods/" + pm.ID + "/credit-card/periods/2030-01", Token: u.Token}).
			Status(t, http.StatusNoContent)
	})

	t.Run("status", func(t *testing.T) {
		u := s.Register(t)
		past := s.CreateCreditCard(t, u, "Vieja", 1, 10, "2020-01-01", "2020-01-10")
		st := status(t, s, u, past.ID)
		if st.NoPeriodsLoaded || !st.DueDatePassed || st.LatestPeriod == nil || st.LatestPeriod.PeriodYM != "2020-01" {
			t.Errorf("past status = %+v", st)
		}

		next := time.Now().AddDate(0, 2, 0)
		closing := time.Date(next.Year(), next.Month(), 5, 0, 0, 0, 0, time.UTC)
		future := s.CreateCreditCard(t, u, "Nueva", 5, 15, closing.Format("2006-01-02"), closing.AddDate(0, 0, 10).Format("2006-01-02"))
		if st := status(t, s, u, future.ID); st.DueDatePassed || st.NoPeriodsLoaded {
			t.Errorf("future status = %+v", st)
		}

		// The only way to have no periods: delete the one created with the card.
		ym := closing.Format("2006-01")
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/payment-methods/" + future.ID + "/credit-card/periods/" + ym, Token: u.Token}).
			Status(t, http.StatusNoContent)
		if st := status(t, s, u, future.ID); !st.NoPeriodsLoaded || st.LatestPeriod != nil {
			t.Errorf("empty status = %+v", st)
		}
	})

	t.Run("status has the open period, loaded or from the default days", func(t *testing.T) {
		u := s.Register(t)
		card := s.CreateCreditCard(t, u, "Visa", 24, 3, "2020-01-24", "2020-02-03")
		today := dates.Today()
		open := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
		if today.Day() > 24 {
			open = open.AddDate(0, 1, 0)
		}
		day := func(m time.Time, d int) string {
			return time.Date(m.Year(), m.Month(), d, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
		}

		st := status(t, s, u, card.ID)
		if st.OpenPeriod.PeriodYM != open.Format("2006-01") || st.OpenPeriod.Loaded ||
			st.OpenPeriod.ClosingDate != day(open, 24) || st.OpenPeriod.DueDate != day(open.AddDate(0, 1, 0), 3) {
			t.Errorf("open period from the defaults = %+v", st.OpenPeriod)
		}

		put(t, s, u, card.ID, open.Format("2006-01"), day(open, 28), day(open.AddDate(0, 1, 0), 6)).Status(t, http.StatusOK)
		st = status(t, s, u, card.ID)
		if !st.OpenPeriod.Loaded || st.OpenPeriod.ClosingDate != day(open, 28) || st.OpenPeriod.DueDate != day(open.AddDate(0, 1, 0), 6) {
			t.Errorf("loaded open period = %+v", st.OpenPeriod)
		}
	})

	t.Run("validation", func(t *testing.T) {
		u := s.Register(t)
		pm := s.CreateCreditCard(t, u, "Master", 25, 5, "2026-11-26", "2026-12-07")
		cases := []struct {
			name, ym, closing, due, field string
		}{
			{"bad ym", "2026-1", "2026-01-25", "2026-02-05", "periodYm"},
			{"closing not a date", "2026-01", "25/01/2026", "2026-02-05", "closingDate"},
			{"due not a date", "2026-01", "2026-01-25", "", "dueDate"},
			{"due before closing", "2026-01", "2026-01-25", "2026-01-20", "dueDate"},
			{"closing outside ym", "2026-01", "2026-02-25", "2026-03-05", "closingDate"},
		}
		for _, tc := range cases {
			put(t, s, u, pm.ID, tc.ym, tc.closing, tc.due).WantError(t, http.StatusUnprocessableEntity, "validation", tc.field)
		}
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/payment-methods/" + pm.ID + "/credit-card/periods/xx", Token: u.Token}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "periodYm")
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/payment-methods/nope/credit-card/periods", Token: u.Token}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "id")
	})

	t.Run("only credit cards of the caller", func(t *testing.T) {
		a, b := s.Register(t), s.Register(t)
		aCard := s.CreateCreditCard(t, a, "Visa A", 25, 5, "2026-11-26", "2026-12-07")
		cash := s.Cash(t, a)

		for _, r := range []apitest.Req{
			{Method: http.MethodGet, Path: "/payment-methods/" + aCard.ID + "/credit-card/periods"},
			{Method: http.MethodGet, Path: "/payment-methods/" + aCard.ID + "/credit-card/periods/status"},
			{Method: http.MethodPut, Path: "/payment-methods/" + aCard.ID + "/credit-card/periods/2026-12",
				Body: map[string]string{"closingDate": "2026-12-23", "dueDate": "2027-01-04"}},
			{Method: http.MethodDelete, Path: "/payment-methods/" + aCard.ID + "/credit-card/periods/2026-11"},
		} {
			r.Token = b.Token
			s.Do(t, r).WantError(t, http.StatusNotFound, "not_found", "")
			r.Token = ""
			s.Do(t, r).WantError(t, http.StatusUnauthorized, "unauthorized", "")
		}
		if got := list(t, s, a, aCard.ID); len(got) != 1 || got[0].DueDate != "2026-12-07" {
			t.Errorf("a's periods changed: %+v", got)
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/payment-methods/" + cash.ID + "/credit-card/periods", Token: a.Token}).
			WantError(t, http.StatusNotFound, "not_found", "")
	})
}
