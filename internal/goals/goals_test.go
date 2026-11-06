package goals_test

// Characterization tests of budget goals and their progress (item 0.21f).

import (
	"net/http"
	"os"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type goalJSON struct {
	ID           string  `json:"id"`
	Scope        string  `json:"scope"`
	UserID       *string `json:"userId"`
	CategoryID   *string `json:"categoryId"`
	GoalType     string  `json:"goalType"`
	TargetAmount float64 `json:"targetAmount"`
	Currency     string  `json:"currency"`
	Period       string  `json:"period"`
	IsActive     bool    `json:"isActive"`
}

type progressJSON struct {
	Goal             goalJSON `json:"goal"`
	PeriodStart      string   `json:"periodStart"`
	PeriodEnd        string   `json:"periodEnd"`
	CurrentAmount    float64  `json:"currentAmount"`
	TargetAmount     float64  `json:"targetAmount"`
	TargetAmountBase float64  `json:"targetAmountBase"`
	BaseCurrency     string   `json:"baseCurrency"`
	FxRate           *float64 `json:"fxRate"`
	Percent          float64  `json:"percent"`
	Status           string   `json:"status"`
}

type house struct {
	s             *apitest.Server
	id            string
	owner, member apitest.User
	category      string
}

func newHouse(t *testing.T, s *apitest.Server) house {
	t.Helper()
	owner := s.Register(t)
	h := s.CreateHousehold(t, owner, "")
	var cats []struct {
		ID string `json:"id"`
	}
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: owner.Token, Household: h.ID}).Status(t, http.StatusOK).JSON(t, &cats)
	return house{s: s, id: h.ID, owner: owner, member: s.AddMember(t, owner, h.ID), category: cats[0].ID}
}

func (h house) do(t testing.TB, u apitest.User, method, path string, body any) *apitest.Res {
	t.Helper()
	return h.s.Do(t, apitest.Req{Method: method, Path: path, Token: u.Token, Household: h.id, Body: body})
}

func (h house) goal(t testing.TB, body map[string]any) goalJSON {
	t.Helper()
	var g goalJSON
	h.do(t, h.owner, http.MethodPost, "/goals", body).Status(t, http.StatusCreated).JSON(t, &g)
	return g
}

func (h house) spend(t testing.TB, u apitest.User, amount float64, date string, extra map[string]any) {
	t.Helper()
	body := map[string]any{"paymentMethodId": h.s.Cash(t, u).ID, "amount": amount, "spentAt": date}
	for k, v := range extra {
		body[k] = v
	}
	h.s.CreateExpense(t, u, h.id, body)
}

func (h house) progress(t testing.TB, g goalJSON, at string) progressJSON {
	t.Helper()
	var p progressJSON
	h.do(t, h.owner, http.MethodGet, "/goals/"+g.ID+"/progress?at="+at, nil).Status(t, http.StatusOK).JSON(t, &p)
	return p
}

func TestGoalsCRUD(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("defaults, list filters, update, deactivate, delete", func(t *testing.T) {
		h := newHouse(t, s)
		g := h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 100000})
		if g.Scope != "household" || g.Period != "monthly" || g.Currency != "ARS" || !g.IsActive || g.UserID != nil {
			t.Errorf("defaults = %+v", g)
		}
		mine := h.goal(t, map[string]any{"scope": "user", "userId": h.member.ID, "goalType": "savings", "targetAmount": 50000, "period": "weekly"})

		list := func(q string) int {
			var l []goalJSON
			h.do(t, h.member, http.MethodGet, "/goals"+q, nil).Status(t, http.StatusOK).JSON(t, &l)
			return len(l)
		}
		if list("") != 2 || list("?scope=user") != 1 || list("?userId="+h.member.ID) != 1 {
			t.Errorf("list filters: all %d, user %d, member %d", list(""), list("?scope=user"), list("?userId="+h.member.ID))
		}

		var upd goalJSON
		h.do(t, h.owner, http.MethodPatch, "/goals/"+g.ID, map[string]any{"targetAmount": 120000, "period": "weekly"}).Status(t, http.StatusOK).JSON(t, &upd)
		if upd.TargetAmount != 120000 || upd.Period != "weekly" || upd.Currency != "ARS" {
			t.Errorf("updated = %+v (omitted currency keeps the old one)", upd)
		}

		h.do(t, h.owner, http.MethodPatch, "/goals/"+mine.ID+"/active", map[string]any{"isActive": false}).Status(t, http.StatusNoContent)
		if list("?active=true") != 1 || list("?active=false") != 1 {
			t.Errorf("active filter: true %d, false %d", list("?active=true"), list("?active=false"))
		}

		h.do(t, h.owner, http.MethodDelete, "/goals/"+g.ID, nil).Status(t, http.StatusNoContent)
		h.do(t, h.owner, http.MethodGet, "/goals/"+g.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("validation", func(t *testing.T) {
		h := newHouse(t, s)
		stranger := s.Register(t)
		for name, tc := range map[string]struct {
			body  map[string]any
			field string
		}{
			"bad scope":             {map[string]any{"scope": "world", "goalType": "total_limit", "targetAmount": 1}, "scope"},
			"household with userId": {map[string]any{"userId": h.member.ID, "goalType": "total_limit", "targetAmount": 1}, "userId"},
			"user without userId":   {map[string]any{"scope": "user", "goalType": "total_limit", "targetAmount": 1}, "userId"},
			"user not a member":     {map[string]any{"scope": "user", "userId": stranger.ID, "goalType": "total_limit", "targetAmount": 1}, "userId"},
			"category without id":   {map[string]any{"goalType": "category_limit", "targetAmount": 1}, "categoryId"},
			"bad type":              {map[string]any{"goalType": "debt", "targetAmount": 1}, "goalType"},
			"yearly period":         {map[string]any{"goalType": "total_limit", "targetAmount": 1, "period": "yearly"}, "period"},
			"zero target":           {map[string]any{"goalType": "total_limit", "targetAmount": 0}, "targetAmount"},
			"bad currency":          {map[string]any{"goalType": "total_limit", "targetAmount": 1, "currency": "BRL"}, "currency"},
			"unknown field":         {map[string]any{"goalType": "total_limit", "targetAmount": 1, "name": "x"}, "body"},
		} {
			t.Run(name, func(t *testing.T) {
				h.do(t, h.owner, http.MethodPost, "/goals", tc.body).WantError(t, http.StatusUnprocessableEntity, "validation", tc.field)
			})
		}
	})

	t.Run("a category from another household is accepted", func(t *testing.T) {
		h := newHouse(t, s)
		other := newHouse(t, s)
		res := h.do(t, h.owner, http.MethodPost, "/goals", map[string]any{"goalType": "category_limit", "categoryId": other.category, "targetAmount": 1})
		testutil.KnownBug(t, "0.6", "a goal's category_id is not checked against the household", func(t testing.TB) {
			res.Status(t, http.StatusUnprocessableEntity)
		})
	})

	t.Run("household scoped", func(t *testing.T) {
		h := newHouse(t, s)
		g := h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 1})
		other := newHouse(t, s)
		for _, r := range []apitest.Req{
			{Method: http.MethodGet, Path: "/goals/" + g.ID},
			{Method: http.MethodGet, Path: "/goals/" + g.ID + "/progress"},
			{Method: http.MethodPatch, Path: "/goals/" + g.ID, Body: map[string]any{"targetAmount": 5}},
			{Method: http.MethodPatch, Path: "/goals/" + g.ID + "/active", Body: map[string]any{"isActive": false}},
			{Method: http.MethodDelete, Path: "/goals/" + g.ID},
		} {
			r.Token, r.Household = other.owner.Token, other.id
			s.Do(t, r).WantError(t, http.StatusNotFound, "not_found", "")
		}
		h.do(t, h.owner, http.MethodGet, "/goals/nope", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
	})
}

func TestGoalProgress(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("household limits: monthly range, category filter, statuses", func(t *testing.T) {
		h := newHouse(t, s)
		total := h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 1000})
		cat := h.goal(t, map[string]any{"goalType": "category_limit", "categoryId": h.category, "targetAmount": 500})

		h.spend(t, h.owner, 300, "2026-03-10", map[string]any{"categoryId": h.category})
		h.spend(t, h.member, 450, "2026-03-20", nil)
		h.spend(t, h.owner, 999, "2026-02-28", nil) // previous month: outside

		p := h.progress(t, total, "2026-03-15")
		if p.PeriodStart != "2026-03-01" || p.PeriodEnd != "2026-03-31" || p.CurrentAmount != 750 || p.Percent != 75 || p.Status != "ok" {
			t.Errorf("total progress = %+v", p)
		}
		if p := h.progress(t, cat, "2026-03-15"); p.CurrentAmount != 300 || p.Status != "ok" {
			t.Errorf("category progress = %+v", p)
		}

		h.spend(t, h.owner, 100, "2026-03-25", nil) // 850/1000 = 85%: warning
		if p := h.progress(t, total, "2026-03-15"); p.Status != "warning" {
			t.Errorf("at 85%%: %+v", p)
		}
		h.spend(t, h.owner, 200, "2026-03-26", nil) // 1050: exceeded
		if p := h.progress(t, total, "2026-03-15"); p.Status != "exceeded" || p.Percent != 105 {
			t.Errorf("over the limit: %+v", p)
		}
	})

	t.Run("weekly period runs Monday to Sunday", func(t *testing.T) {
		h := newHouse(t, s)
		g := h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 1000, "period": "weekly"})
		for _, at := range []string{"2026-03-09", "2026-03-12", "2026-03-15"} { // Mon, Thu, Sun
			if p := h.progress(t, g, at); p.PeriodStart != "2026-03-09" || p.PeriodEnd != "2026-03-15" {
				t.Errorf("at %s: %s..%s", at, p.PeriodStart, p.PeriodEnd)
			}
		}
	})

	t.Run("a user goal counts their own expenses and their share of shared ones", func(t *testing.T) {
		h := newHouse(t, s)
		g := h.goal(t, map[string]any{"scope": "user", "userId": h.member.ID, "goalType": "total_limit", "targetAmount": 1000})
		h.spend(t, h.member, 100, "2026-03-05", nil)                             // own
		h.spend(t, h.owner, 300, "2026-03-06", map[string]any{"isShared": true}) // member's share: 150
		h.spend(t, h.owner, 500, "2026-03-07", nil)                              // owner's own: not counted
		if p := h.progress(t, g, "2026-03-15"); p.CurrentAmount != 250 {
			t.Errorf("member's spending = %v, want 250", p.CurrentAmount)
		}
	})

	t.Run("savings: income minus spending", func(t *testing.T) {
		h := newHouse(t, s)
		g := h.goal(t, map[string]any{"goalType": "savings", "targetAmount": 1000})
		h.do(t, h.owner, http.MethodPost, "/incomes", map[string]any{"amount": 1500, "currency": "ARS", "source": "salary",
			"description": "Sueldo", "receivedAt": "2026-03-01"}).Status(t, http.StatusCreated)
		h.spend(t, h.owner, 900, "2026-03-10", nil)
		p := h.progress(t, g, "2026-03-15")
		if p.CurrentAmount != 600 || p.Percent != 60 || p.Status != "warning" {
			t.Errorf("savings = %+v", p)
		}
		h.do(t, h.owner, http.MethodPost, "/incomes", map[string]any{"amount": 500, "currency": "ARS", "source": "gift",
			"description": "Regalo", "receivedAt": "2026-03-02"}).Status(t, http.StatusCreated)
		if p := h.progress(t, g, "2026-03-15"); p.Status != "ok" {
			t.Errorf("savings reached: %+v", p)
		}
	})

	t.Run("credit installments count in the month they are due", func(t *testing.T) {
		h := newHouse(t, s)
		g := h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 1000})
		card := s.CreateCreditCard(t, h.owner, "Visa", 25, 5, "2026-02-26", "2026-03-07")
		s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": card.ID, "amount": 600, "spentAt": "2026-02-10", "installments": 3})
		// 200 due 2026-03-07, then 2026-04-05, then 2026-05-05.
		for at, want := range map[string]float64{"2026-02-15": 0, "2026-03-15": 200, "2026-04-15": 200} {
			if p := h.progress(t, g, at); p.CurrentAmount != want {
				t.Errorf("at %s: %v, want %v", at, p.CurrentAmount, want)
			}
		}
	})

	t.Run("progress list and an invalid at", func(t *testing.T) {
		h := newHouse(t, s)
		h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 1000})
		h.goal(t, map[string]any{"goalType": "savings", "targetAmount": 1000})
		var list []progressJSON
		h.do(t, h.owner, http.MethodGet, "/goals/progress?at=2026-03-15", nil).Status(t, http.StatusOK).JSON(t, &list)
		if len(list) != 2 || list[0].PeriodStart != "2026-03-01" {
			t.Errorf("progress list = %+v", list)
		}
		// An unparseable "at" is ignored and today is used instead.
		h.do(t, h.owner, http.MethodGet, "/goals/progress?at=mañana", nil).Status(t, http.StatusOK)
	})

	t.Run("a target in another currency is converted with today's rate", func(t *testing.T) {
		h := newHouse(t, s)
		if _, err := s.Pool.Exec(t.Context(), `INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell)
			VALUES ('USD', 'blue', now(), 1500, 1490, 1510)`); err != nil {
			t.Fatal(err)
		}
		g := h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 100, "currency": "USD"})
		h.spend(t, h.owner, 90000, "2026-03-10", nil) // ARS
		p := h.progress(t, g, "2026-03-15")
		// USD 100 x 1500 = ARS 150000; 90000 spent is 60%.
		if p.TargetAmount != 100 || p.TargetAmountBase != 150000 || p.BaseCurrency != "ARS" ||
			p.FxRate == nil || *p.FxRate != 1500 || p.Percent != 60 || p.Status != "ok" {
			t.Errorf("USD goal progress = %+v", p)
		}

		ars := h.goal(t, map[string]any{"goalType": "total_limit", "targetAmount": 100000})
		pa := h.progress(t, ars, "2026-03-15")
		if pa.TargetAmountBase != 100000 || pa.FxRate != nil || pa.Percent != 90 {
			t.Errorf("ARS goal progress = %+v", pa)
		}
	})
}
