package incomes_test

// Characterization tests of incomes, recurring incomes, the daily generation
// job and the income totals (item 0.21f).

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type incomeJSON struct {
	ID                string   `json:"id"`
	HouseholdID       string   `json:"householdId"`
	ReceivedBy        string   `json:"receivedBy"`
	PaymentMethodID   *string  `json:"paymentMethodId"`
	Amount            float64  `json:"amount"`
	Currency          string   `json:"currency"`
	AmountBase        float64  `json:"amountBase"`
	BaseCurrency      string   `json:"baseCurrency"`
	RateUsed          *float64 `json:"rateUsed"`
	RateSource        *string  `json:"rateSource"`
	Source            string   `json:"source"`
	Description       string   `json:"description"`
	ReceivedAt        string   `json:"receivedAt"`
	RecurringIncomeID *string  `json:"recurringIncomeId"`
}

type recurringJSON struct {
	ID            string  `json:"id"`
	ReceivedBy    string  `json:"receivedBy"`
	Amount        float64 `json:"amount"`
	Frequency     string  `json:"frequency"`
	DayOfMonth    *int    `json:"dayOfMonth"`
	IsActive      bool    `json:"isActive"`
	StartsAt      string  `json:"startsAt"`
	EndsAt        *string `json:"endsAt"`
	LastGenerated *string `json:"lastGenerated"`
}

type house struct {
	s             *apitest.Server
	id            string
	owner, member apitest.User
}

func newHouse(t *testing.T, s *apitest.Server) house {
	t.Helper()
	owner := s.Register(t)
	h := s.CreateHousehold(t, owner, "")
	return house{s: s, id: h.ID, owner: owner, member: s.AddMember(t, owner, h.ID)}
}

func (h house) do(t testing.TB, u apitest.User, method, path string, body any) *apitest.Res {
	t.Helper()
	return h.s.Do(t, apitest.Req{Method: method, Path: path, Token: u.Token, Household: h.id, Body: body})
}

func income(extra map[string]any) map[string]any {
	b := map[string]any{"amount": 1000, "currency": "ARS", "source": "salary", "description": "Sueldo", "receivedAt": "2026-01-05"}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func (h house) create(t testing.TB, u apitest.User, extra map[string]any) incomeJSON {
	t.Helper()
	var i incomeJSON
	h.do(t, u, http.MethodPost, "/incomes", income(extra)).Status(t, http.StatusCreated).JSON(t, &i)
	return i
}

func TestIncomes(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("create, read, edit, delete", func(t *testing.T) {
		h := newHouse(t, s)
		cash := s.Cash(t, h.owner)
		i := h.create(t, h.owner, map[string]any{"source": " Freelance ", "description": " Diseño ", "currency": "ars", "paymentMethodId": cash.ID})
		if i.ReceivedBy != h.owner.ID || i.Source != "freelance" || i.Description != "Diseño" || i.AmountBase != 1000 ||
			i.BaseCurrency != "ARS" || i.RateUsed != nil || i.PaymentMethodID == nil || *i.PaymentMethodID != cash.ID {
			t.Errorf("income = %+v", i)
		}

		var got incomeJSON
		h.do(t, h.member, http.MethodGet, "/incomes/"+i.ID, nil).Status(t, http.StatusOK).JSON(t, &got)
		if got.ID != i.ID {
			t.Errorf("get = %+v", got)
		}

		var upd incomeJSON
		h.do(t, h.owner, http.MethodPatch, "/incomes/"+i.ID, map[string]any{"source": "gift", "description": "Regalo", "receivedAt": "2026-01-06"}).
			Status(t, http.StatusOK).JSON(t, &upd)
		if upd.Source != "gift" || upd.Description != "Regalo" || upd.ReceivedAt != "2026-01-06" || upd.Amount != 1000 {
			t.Errorf("updated = %+v (amount is not editable)", upd)
		}

		h.do(t, h.owner, http.MethodDelete, "/incomes/"+i.ID, nil).Status(t, http.StatusNoContent)
		h.do(t, h.owner, http.MethodGet, "/incomes/"+i.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("recorded on behalf of another member", func(t *testing.T) {
		h := newHouse(t, s)
		i := h.create(t, h.owner, map[string]any{"receivedBy": h.member.ID})
		if i.ReceivedBy != h.member.ID {
			t.Errorf("receivedBy = %s", i.ReceivedBy)
		}
		h.do(t, h.owner, http.MethodPost, "/incomes", income(map[string]any{"receivedBy": s.Register(t).ID})).
			WantError(t, http.StatusUnprocessableEntity, "validation", "receivedBy")
		// The receiver's own payment method is fine; the recorder's is not (0.6).
		h.create(t, h.owner, map[string]any{"receivedBy": h.member.ID, "paymentMethodId": s.Cash(t, h.member).ID})
		h.do(t, h.owner, http.MethodPost, "/incomes", income(map[string]any{"receivedBy": h.member.ID, "paymentMethodId": s.Cash(t, h.owner).ID})).
			WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("foreign currency uses the household's rate, or the one picked (plan 5.1)", func(t *testing.T) {
		h := newHouse(t, s)
		if _, err := s.Pool.Exec(t.Context(), `INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell)
			VALUES ('USD', 'bolsa', now(), 1200, 1190, 1210), ('USD', 'contadoconliqui', now(), 1660, 1658, 1665)
			ON CONFLICT DO NOTHING`); err != nil {
			t.Fatal(err)
		}
		i := h.create(t, h.owner, map[string]any{"amount": 100, "currency": "USD"})
		if i.AmountBase != 121000 || i.RateUsed == nil || *i.RateUsed != 1210 || i.RateSource == nil || *i.RateSource != "bolsa" {
			t.Errorf("USD income = %+v", i)
		}
		// A freelancer who sold the dollars through CCL.
		i = h.create(t, h.owner, map[string]any{"amount": 1000, "currency": "USD", "rateSource": "contadoconliqui"})
		if i.AmountBase != 1665000 || *i.RateUsed != 1665 || *i.RateSource != "contadoconliqui" {
			t.Errorf("USD income at CCL = %+v", i)
		}
	})

	t.Run("validation", func(t *testing.T) {
		h := newHouse(t, s)
		for name, tc := range map[string]struct {
			body  map[string]any
			field string
		}{
			"zero amount":    {income(map[string]any{"amount": 0}), "amount"},
			"unknown source": {income(map[string]any{"source": "lottery"}), "source"},
			"empty desc":     {income(map[string]any{"description": " "}), "description"},
			"bad date":       {income(map[string]any{"receivedAt": "05/01/2026"}), "receivedAt"},
			"missing date":   {income(map[string]any{"receivedAt": ""}), "receivedAt"},
			"unknown field":  {income(map[string]any{"tax": 1}), "body"},
			// A malformed UUID in the body reaches the client as the raw Go
			// error on field "body" (0.1 makes these messages readable).
			"receivedBy uuid": {income(map[string]any{"receivedBy": "x"}), "body"},
		} {
			t.Run(name, func(t *testing.T) {
				h.do(t, h.owner, http.MethodPost, "/incomes", tc.body).WantError(t, http.StatusUnprocessableEntity, "validation", tc.field)
			})
		}
		i := h.create(t, h.owner, nil)
		h.do(t, h.owner, http.MethodPatch, "/incomes/"+i.ID, map[string]any{"source": "x", "description": "d", "receivedAt": "2026-01-01"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "source")
		h.do(t, h.owner, http.MethodPatch, "/incomes/"+i.ID, map[string]any{"source": "gift", "description": "", "receivedAt": "2026-01-01"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "description")
	})

	t.Run("list filters and totals", func(t *testing.T) {
		h := newHouse(t, s)
		h.create(t, h.owner, map[string]any{"amount": 100, "receivedAt": "2026-01-05"})
		h.create(t, h.member, map[string]any{"amount": 200, "source": "gift", "receivedAt": "2026-02-05"})
		h.create(t, h.member, map[string]any{"amount": 300, "receivedAt": "2026-03-05"})

		count := func(q string) int64 {
			var l struct {
				Items      []incomeJSON `json:"items"`
				TotalCount int64        `json:"totalCount"`
			}
			h.do(t, h.owner, http.MethodGet, "/incomes"+q, nil).Status(t, http.StatusOK).JSON(t, &l)
			if int64(len(l.Items)) > l.TotalCount {
				t.Errorf("list%s: %d items > totalCount %d", q, len(l.Items), l.TotalCount)
			}
			return l.TotalCount
		}
		for q, want := range map[string]int64{
			"": 3, "?receivedBy=" + h.member.ID: 2, "?source=gift": 1, "?from=2026-02-01": 2, "?to=2026-01-31": 1,
		} {
			if got := count(q); got != want {
				t.Errorf("list%s total = %d, want %d", q, got, want)
			}
		}
		for q, field := range map[string]string{
			"?receivedBy=x": "receivedBy", "?paymentMethodId=x": "paymentMethodId", "?from=x": "from", "?to=x": "to",
			"?limit=0": "limit", "?offset=-1": "offset",
		} {
			h.do(t, h.owner, http.MethodGet, "/incomes"+q, nil).WantError(t, http.StatusUnprocessableEntity, "validation", field)
		}

		var tot struct {
			Total        float64 `json:"total"`
			BaseCurrency string  `json:"baseCurrency"`
			From, To     string
		}
		h.do(t, h.owner, http.MethodGet, "/totals/income?from=2026-01-01&to=2026-02-28", nil).Status(t, http.StatusOK).JSON(t, &tot)
		if tot.Total != 300 || tot.BaseCurrency != "ARS" || tot.From != "2026-01-01" || tot.To != "2026-02-28" {
			t.Errorf("totals = %+v", tot)
		}
		// Default range: the current month (app timezone) up to today.
		h.do(t, h.owner, http.MethodGet, "/totals/income", nil).Status(t, http.StatusOK).JSON(t, &tot)
		if !strings.HasSuffix(tot.From, "-01") {
			t.Errorf("default from = %s", tot.From)
		}
		h.do(t, h.owner, http.MethodGet, "/totals/income?from=x", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "from")

		res := h.do(t, h.owner, http.MethodGet, "/incomes?offset=3000000000", nil)
		if res.Code == http.StatusInternalServerError {
			t.Errorf("offset 3000000000 answered 500")
		}
	})

	t.Run("another member cannot edit or delete my income", func(t *testing.T) {
		h := newHouse(t, s)
		i := h.create(t, h.owner, nil)
		edit := h.do(t, h.member, http.MethodPatch, "/incomes/"+i.ID, map[string]any{"source": "gift", "description": "Ajeno", "receivedAt": "2026-01-05"})
		edit.Status(t, http.StatusForbidden)
		del := h.do(t, h.member, http.MethodDelete, "/incomes/"+i.ID, nil)
		del.Status(t, http.StatusForbidden)
	})

	t.Run("another user's payment method is accepted", func(t *testing.T) {
		h := newHouse(t, s)
		res := h.do(t, h.owner, http.MethodPost, "/incomes", income(map[string]any{"paymentMethodId": s.Cash(t, h.member).ID}))
		if res.Code == http.StatusCreated {
			t.Errorf("income recorded on another user's payment method")
		}
	})

	t.Run("household scoped", func(t *testing.T) {
		h := newHouse(t, s)
		i := h.create(t, h.owner, nil)
		other := s.Register(t)
		oh := s.CreateHousehold(t, other, "")
		for _, m := range []string{http.MethodGet, http.MethodDelete} {
			s.Do(t, apitest.Req{Method: m, Path: "/incomes/" + i.ID, Token: other.Token, Household: oh.ID}).WantError(t, http.StatusNotFound, "not_found", "")
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/incomes", Token: other.Token, Household: h.id}).WantError(t, http.StatusNotFound, "not_found", "")
		h.do(t, h.owner, http.MethodGet, "/incomes/nope", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
	})
}

func TestRecurringIncomes(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	recurring := func(extra map[string]any) map[string]any {
		b := map[string]any{"amount": 500000, "currency": "ARS", "source": "salary", "description": "Sueldo",
			"frequency": "monthly", "dayOfMonth": 5, "startsAt": "2099-01-01"}
		for k, v := range extra {
			b[k] = v
		}
		return b
	}

	t.Run("CRUD and activation", func(t *testing.T) {
		h := newHouse(t, s)
		var r recurringJSON
		h.do(t, h.owner, http.MethodPost, "/recurring-incomes", recurring(nil)).Status(t, http.StatusCreated).JSON(t, &r)
		if !r.IsActive || r.Frequency != "monthly" || r.DayOfMonth == nil || *r.DayOfMonth != 5 || r.LastGenerated != nil || r.ReceivedBy != h.owner.ID {
			t.Errorf("recurring = %+v (future start: nothing generated yet)", r)
		}

		var list []recurringJSON
		h.do(t, h.member, http.MethodGet, "/recurring-incomes", nil).Status(t, http.StatusOK).JSON(t, &list)
		if len(list) != 1 {
			t.Errorf("list = %+v", list)
		}

		var upd recurringJSON
		h.do(t, h.owner, http.MethodPatch, "/recurring-incomes/"+r.ID, map[string]any{"amount": 600000, "currency": "ARS", "source": "salary",
			"description": "Sueldo nuevo", "frequency": "weekly", "dayOfWeek": 1, "endsAt": "2100-01-01"}).Status(t, http.StatusOK).JSON(t, &upd)
		if upd.Amount != 600000 || upd.Frequency != "weekly" || upd.EndsAt == nil {
			t.Errorf("updated = %+v", upd)
		}

		h.do(t, h.owner, http.MethodPatch, "/recurring-incomes/"+r.ID+"/active", map[string]any{"isActive": false}).Status(t, http.StatusNoContent)
		var got recurringJSON
		h.do(t, h.owner, http.MethodGet, "/recurring-incomes/"+r.ID, nil).Status(t, http.StatusOK).JSON(t, &got)
		if got.IsActive {
			t.Errorf("still active")
		}

		h.do(t, h.owner, http.MethodDelete, "/recurring-incomes/"+r.ID, nil).Status(t, http.StatusNoContent)
		h.do(t, h.owner, http.MethodGet, "/recurring-incomes/"+r.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("recurrence validation", func(t *testing.T) {
		h := newHouse(t, s)
		for name, tc := range map[string]struct {
			extra map[string]any
			field string
		}{
			"bad frequency":        {map[string]any{"frequency": "daily"}, "frequency"},
			"monthly without day":  {map[string]any{"dayOfMonth": nil}, "dayOfMonth"},
			"monthly day 32":       {map[string]any{"dayOfMonth": 32}, "dayOfMonth"},
			"weekly without day":   {map[string]any{"frequency": "weekly"}, "dayOfWeek"},
			"weekly day 7":         {map[string]any{"frequency": "weekly", "dayOfWeek": 7}, "dayOfWeek"},
			"yearly without month": {map[string]any{"frequency": "yearly"}, "monthOfYear"},
			"zero amount":          {map[string]any{"amount": 0}, "amount"},
			"bad source":           {map[string]any{"source": "x"}, "source"},
			"empty description":    {map[string]any{"description": ""}, "description"},
			"bad startsAt":         {map[string]any{"startsAt": "x"}, "startsAt"},
			"bad endsAt":           {map[string]any{"endsAt": "x"}, "endsAt"},
		} {
			t.Run(name, func(t *testing.T) {
				body := recurring(tc.extra)
				for k, v := range body {
					if v == nil {
						delete(body, k)
					}
				}
				h.do(t, h.owner, http.MethodPost, "/recurring-incomes", body).WantError(t, http.StatusUnprocessableEntity, "validation", tc.field)
			})
		}
	})

	t.Run("creating one with a past start books the latest occurrence right away", func(t *testing.T) {
		h := newHouse(t, s)
		now := time.Now()
		due := now.AddDate(0, 0, -2)
		var r recurringJSON
		h.do(t, h.owner, http.MethodPost, "/recurring-incomes", recurring(map[string]any{
			"frequency": "weekly", "dayOfMonth": nil, "dayOfWeek": int(due.Weekday()), "startsAt": now.AddDate(0, 0, -20).Format("2006-01-02"),
		})).Status(t, http.StatusCreated).JSON(t, &r)
		if r.LastGenerated == nil || *r.LastGenerated != due.Format("2006-01-02") {
			t.Errorf("lastGenerated = %v, want %s", r.LastGenerated, due.Format("2006-01-02"))
		}
		var l struct {
			Items []incomeJSON `json:"items"`
		}
		h.do(t, h.owner, http.MethodGet, "/incomes", nil).Status(t, http.StatusOK).JSON(t, &l)
		if len(l.Items) != 1 || l.Items[0].ReceivedAt != due.Format("2006-01-02") || l.Items[0].Amount != 500000 {
			t.Errorf("generated incomes = %+v (only the latest occurrence, no backfill)", l.Items)
		}
	})

	t.Run("daily job: generates on matching days, once per day", func(t *testing.T) {
		h := newHouse(t, s)
		var r recurringJSON
		h.do(t, h.owner, http.MethodPost, "/recurring-incomes", recurring(map[string]any{"dayOfMonth": 31})).Status(t, http.StatusCreated).JSON(t, &r)
		if _, err := s.Pool.Exec(t.Context(), `UPDATE recurring_incomes SET starts_at = '2026-01-01' WHERE id = $1`, r.ID); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		day := func(s string) time.Time {
			d, _ := time.Parse("2006-01-02", s)
			return d
		}
		run := func(date string) int {
			n, err := s.Jobs.RecurringIncomes(ctx, day(date))
			if err != nil {
				t.Fatalf("job %s: %v", date, err)
			}
			return n
		}

		// Other households' templates may match too: count only this one's incomes.
		mine := func() int {
			var n int
			if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM incomes WHERE household_id = $1`, h.id).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		run("2026-02-27")
		if mine() != 0 {
			t.Errorf("generated on a non-matching day")
		}
		run("2026-02-28") // day 31 clamps to the last day of February
		run("2026-02-28") // same day again: no duplicate
		if mine() != 1 {
			t.Errorf("incomes after Feb 28 twice = %d, want 1", mine())
		}

		// The generated income points at its series; a manual one does not.
		var list struct {
			Items []incomeJSON `json:"items"`
		}
		h.do(t, h.owner, http.MethodGet, "/incomes?from=2026-02-01&to=2026-02-28", nil).Status(t, http.StatusOK).JSON(t, &list)
		if len(list.Items) != 1 || list.Items[0].RecurringIncomeID == nil || *list.Items[0].RecurringIncomeID != r.ID {
			t.Errorf("generated income = %+v, want recurringIncomeId %s", list.Items, r.ID)
		}
		if manual := h.create(t, h.owner, nil); manual.RecurringIncomeID != nil {
			t.Errorf("manual income has recurringIncomeId %s", *manual.RecurringIncomeID)
		}

		// A run that booked the income but did not mark the series: the retry
		// marks it and books nothing (plan 0.3b).
		if _, err := s.Pool.Exec(ctx, `UPDATE recurring_incomes SET last_generated = '2026-01-31' WHERE id = $1`, r.ID); err != nil {
			t.Fatal(err)
		}
		run("2026-02-28")
		var last string
		if err := s.Pool.QueryRow(ctx, `SELECT last_generated::text FROM recurring_incomes WHERE id = $1`, r.ID).Scan(&last); err != nil {
			t.Fatal(err)
		}
		if mine() != 2 || last != "2026-02-28" { // 2 = the generated one + the manual one
			t.Errorf("after the retried run: %d incomes, last_generated %s", mine(), last)
		}

		h.do(t, h.owner, http.MethodPatch, "/recurring-incomes/"+r.ID+"/active", map[string]any{"isActive": false}).Status(t, http.StatusNoContent)
		run("2026-03-31")
		if mine() != 2 {
			t.Errorf("an inactive template generated an income")
		}
	})

	t.Run("household scoped", func(t *testing.T) {
		h := newHouse(t, s)
		var r recurringJSON
		h.do(t, h.owner, http.MethodPost, "/recurring-incomes", recurring(nil)).Status(t, http.StatusCreated).JSON(t, &r)
		other := s.Register(t)
		oh := s.CreateHousehold(t, other, "")
		for _, req := range []apitest.Req{
			{Method: http.MethodGet, Path: "/recurring-incomes/" + r.ID},
			{Method: http.MethodPatch, Path: "/recurring-incomes/" + r.ID + "/active", Body: map[string]any{"isActive": false}},
			{Method: http.MethodDelete, Path: "/recurring-incomes/" + r.ID},
		} {
			req.Token, req.Household = other.Token, oh.ID
			s.Do(t, req).WantError(t, http.StatusNotFound, "not_found", "")
		}
	})
}
