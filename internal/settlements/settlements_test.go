package settlements_test

// Characterization tests (item 0.21e) of balances, settlements and split
// rules, which only make sense together: shared expenses create debt,
// settlements pay it, split rules decide the shares.

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type house struct {
	s                  *apitest.Server
	id                 string
	owner, ana, carlos apitest.User
}

func newHouse(t *testing.T, s *apitest.Server) house {
	t.Helper()
	owner := s.Register(t)
	h := s.CreateHousehold(t, owner, "")
	return house{s: s, id: h.ID, owner: owner, ana: s.AddMember(t, owner, h.ID), carlos: s.AddMember(t, owner, h.ID)}
}

func (h house) do(t testing.TB, u apitest.User, method, path string, body any) *apitest.Res {
	t.Helper()
	return h.s.Do(t, apitest.Req{Method: method, Path: path, Token: u.Token, Household: h.id, Body: body})
}

// shared makes payer pay amount for everyone, dated spentAt.
func (h house) shared(t testing.TB, payer apitest.User, amount float64, spentAt string) apitest.ExpenseDetail {
	t.Helper()
	return h.s.CreateExpense(t, payer, h.id, map[string]any{
		"paymentMethodId": h.s.Cash(t, payer).ID, "amount": amount, "spentAt": spentAt, "isShared": true})
}

type row struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Amount float64 `json:"amount"`
}

func (h house) balances(t testing.TB) map[[2]string]float64 {
	t.Helper()
	var out struct {
		HouseholdID string `json:"householdId"`
		Balances    []row  `json:"balances"`
	}
	h.do(t, h.owner, http.MethodGet, "/balances", nil).Status(t, http.StatusOK).JSON(t, &out)
	m := map[[2]string]float64{}
	for _, b := range out.Balances {
		m[[2]string{b.From, b.To}] = b.Amount
	}
	return m
}

type settlementJSON struct {
	ID           string  `json:"id"`
	FromUser     string  `json:"fromUser"`
	ToUser       string  `json:"toUser"`
	Amount       float64 `json:"amount"`
	BaseCurrency string  `json:"baseCurrency"`
	Note         *string `json:"note"`
	PaidAt       string  `json:"paidAt"`
}

func (h house) settle(t testing.TB, by, from, to apitest.User, amount float64, extra map[string]any) *apitest.Res {
	t.Helper()
	body := map[string]any{"fromUser": from.ID, "toUser": to.ID, "amount": amount}
	for k, v := range extra {
		body[k] = v
	}
	return h.do(t, by, http.MethodPost, "/settlements", body)
}

func TestBalances(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("shared expenses become debts to the payer, netted per pair", func(t *testing.T) {
		h := newHouse(t, s)
		h.shared(t, h.owner, 900, "2026-01-10") // each owes owner 300
		h.shared(t, h.ana, 300, "2026-01-11")   // each owes ana 100

		got := h.balances(t)
		want := map[[2]string]float64{
			{h.ana.ID, h.owner.ID}:    200, // 300 - 100
			{h.carlos.ID, h.owner.ID}: 300,
			{h.carlos.ID, h.ana.ID}:   100,
		}
		if len(got) != len(want) {
			t.Errorf("balances = %v, want %v", got, want)
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s owes %s: %v, want %v", k[0], k[1], got[k], v)
			}
		}

		var me struct {
			UserID   string  `json:"userId"`
			Owe      []row   `json:"owe"`
			OwedToMe []row   `json:"owedToMe"`
			Net      float64 `json:"net"`
		}
		h.do(t, h.ana, http.MethodGet, "/balances/me", nil).Status(t, http.StatusOK).JSON(t, &me)
		if me.UserID != h.ana.ID || me.Net != -100 || len(me.Owe) != 1 || me.Owe[0].Amount != 200 || len(me.OwedToMe) != 1 || me.OwedToMe[0].Amount != 100 {
			t.Errorf("ana's view = %+v", me)
		}
	})

	t.Run("installments billed in the future do not count yet", func(t *testing.T) {
		h := newHouse(t, s)
		future := time.Now().AddDate(0, 3, 0).Format("2006-01-02")
		h.shared(t, h.owner, 900, future)
		if got := h.balances(t); len(got) != 0 {
			t.Errorf("balances with only future expenses = %v", got)
		}
	})

	t.Run("deleting the expense removes the debt", func(t *testing.T) {
		h := newHouse(t, s)
		e := h.shared(t, h.owner, 300, "2026-01-10")
		h.do(t, h.owner, http.MethodDelete, "/expenses/"+e.Expense.ID, nil).Status(t, http.StatusNoContent)
		if got := h.balances(t); len(got) != 0 {
			t.Errorf("balances after delete = %v", got)
		}
	})

	t.Run("empty household: empty lists, net 0", func(t *testing.T) {
		h := newHouse(t, s)
		res := h.do(t, h.owner, http.MethodGet, "/balances/me", nil).Status(t, http.StatusOK)
		if !strings.Contains(string(res.Body), `"owe":[]`) || !strings.Contains(string(res.Body), `"net":0`) {
			t.Errorf("empty view = %s", res.Body)
		}
	})
}

func TestSettlements(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("paying part of a debt", func(t *testing.T) {
		h := newHouse(t, s)
		h.shared(t, h.owner, 900, "2026-01-10") // ana owes owner 300

		var sp settlementJSON
		h.settle(t, h.ana, h.ana, h.owner, 120, map[string]any{"note": "  transferencia ", "paidAt": "2026-01-15"}).
			Status(t, http.StatusCreated).JSON(t, &sp)
		if sp.FromUser != h.ana.ID || sp.ToUser != h.owner.ID || sp.Amount != 120 || sp.BaseCurrency != "ARS" ||
			sp.Note == nil || *sp.Note != "transferencia" || sp.PaidAt != "2026-01-15" {
			t.Errorf("settlement = %+v", sp)
		}
		if got := h.balances(t)[[2]string{h.ana.ID, h.owner.ID}]; got != 180 {
			t.Errorf("remaining debt = %v, want 180", got)
		}

		var got settlementJSON
		h.do(t, h.owner, http.MethodGet, "/settlements/"+sp.ID, nil).Status(t, http.StatusOK).JSON(t, &got)
		if got.ID != sp.ID {
			t.Errorf("get = %+v", got)
		}

		h.do(t, h.ana, http.MethodDelete, "/settlements/"+sp.ID, nil).Status(t, http.StatusNoContent)
		if got := h.balances(t)[[2]string{h.ana.ID, h.owner.ID}]; got != 300 {
			t.Errorf("debt after deleting the payment = %v, want 300", got)
		}
		h.do(t, h.ana, http.MethodGet, "/settlements/"+sp.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("paying it all clears the pair; empty note and default date", func(t *testing.T) {
		h := newHouse(t, s)
		h.shared(t, h.owner, 900, "2026-01-10")
		var sp settlementJSON
		h.settle(t, h.ana, h.ana, h.owner, 300, map[string]any{"note": "   "}).Status(t, http.StatusCreated).JSON(t, &sp)
		if sp.Note != nil || sp.PaidAt == "" {
			t.Errorf("settlement = %+v, want no note and today's date", sp)
		}
		if _, still := h.balances(t)[[2]string{h.ana.ID, h.owner.ID}]; still {
			t.Errorf("pair still has a balance")
		}
	})

	t.Run("validation", func(t *testing.T) {
		h := newHouse(t, s)
		h.shared(t, h.owner, 900, "2026-01-10")
		stranger := s.Register(t)
		h.settle(t, h.ana, h.ana, h.ana, 10, nil).WantError(t, http.StatusUnprocessableEntity, "validation", "toUser")
		h.settle(t, h.ana, h.ana, h.owner, 0, nil).WantError(t, http.StatusUnprocessableEntity, "validation", "amount")
		h.settle(t, h.ana, h.ana, h.owner, 300.02, nil).WantError(t, http.StatusUnprocessableEntity, "validation", "amount")
		h.settle(t, h.ana, h.owner, h.ana, 10, nil).WantError(t, http.StatusUnprocessableEntity, "validation", "amount") // no debt that way
		h.settle(t, h.ana, stranger, h.owner, 10, nil).WantError(t, http.StatusUnprocessableEntity, "validation", "fromUser")
		h.settle(t, h.ana, h.ana, stranger, 10, nil).WantError(t, http.StatusUnprocessableEntity, "validation", "toUser")
		h.settle(t, h.ana, h.ana, h.owner, 10, map[string]any{"paidAt": "15/01/2026"}).WantError(t, http.StatusUnprocessableEntity, "validation", "paidAt")
		// One cent over is tolerated.
		h.settle(t, h.ana, h.ana, h.owner, 300.01, nil).Status(t, http.StatusCreated)
	})

	t.Run("list and filters", func(t *testing.T) {
		h := newHouse(t, s)
		h.shared(t, h.owner, 900, "2026-01-10")
		h.shared(t, h.ana, 300, "2026-01-10")
		a := h.settle(t, h.ana, h.ana, h.owner, 50, map[string]any{"paidAt": "2026-02-01"})
		b := h.settle(t, h.carlos, h.carlos, h.owner, 60, map[string]any{"paidAt": "2026-03-01"})
		c := h.settle(t, h.carlos, h.carlos, h.ana, 70, map[string]any{"paidAt": "2026-04-01"})
		for _, r := range []*apitest.Res{a, b, c} {
			r.Status(t, http.StatusCreated)
		}

		amounts := func(query string) []float64 {
			var l struct {
				Items []settlementJSON `json:"items"`
			}
			h.do(t, h.owner, http.MethodGet, "/settlements"+query, nil).Status(t, http.StatusOK).JSON(t, &l)
			var out []float64
			for _, it := range l.Items {
				out = append(out, it.Amount)
			}
			return out
		}
		for _, tc := range []struct {
			query string
			want  int
		}{
			{"", 3},
			{"?fromUser=" + h.carlos.ID, 2},
			{"?toUser=" + h.owner.ID, 2},
			{"?withUser=" + h.ana.ID, 2},
			{"?from=2026-03-01", 2},
			{"?to=2026-02-15", 1},
			{"?limit=1", 1},
			{"?limit=2&offset=2", 1},
		} {
			if got := amounts(tc.query); len(got) != tc.want {
				t.Errorf("list%s = %v, want %d items", tc.query, got, tc.want)
			}
		}
		for q, field := range map[string]string{
			"?fromUser=x": "fromUser", "?toUser=x": "toUser", "?withUser=x": "withUser",
			"?from=x": "from", "?to=x": "to", "?limit=0": "limit", "?offset=-1": "offset",
		} {
			h.do(t, h.owner, http.MethodGet, "/settlements"+q, nil).WantError(t, http.StatusUnprocessableEntity, "validation", field)
		}

		page := h.do(t, h.owner, http.MethodGet, "/settlements?limit=1", nil)
		if !strings.Contains(string(page.Body), `"totalCount":3`) {
			t.Errorf("page = %s", page.Body)
		}

		res := h.do(t, h.owner, http.MethodGet, "/settlements?offset=3000000000", nil)
		if res.Code == http.StatusInternalServerError {
			t.Errorf("offset 3000000000 answered 500")
		}
	})

	t.Run("a third member deletes a payment between two others", func(t *testing.T) {
		h := newHouse(t, s)
		h.shared(t, h.owner, 900, "2026-01-10")
		var sp settlementJSON
		h.settle(t, h.ana, h.ana, h.owner, 100, nil).Status(t, http.StatusCreated).JSON(t, &sp)
		res := h.do(t, h.carlos, http.MethodDelete, "/settlements/"+sp.ID, nil)
		testutil.KnownBug(t, "0.6", "any household member can delete a settlement between two other members", func(t testing.TB) {
			res.Status(t, http.StatusForbidden)
		})
	})

	t.Run("household scoped", func(t *testing.T) {
		h := newHouse(t, s)
		h.shared(t, h.owner, 900, "2026-01-10")
		var sp settlementJSON
		h.settle(t, h.ana, h.ana, h.owner, 100, nil).Status(t, http.StatusCreated).JSON(t, &sp)

		other := s.Register(t)
		oh := s.CreateHousehold(t, other, "")
		for _, m := range []string{http.MethodGet, http.MethodDelete} {
			s.Do(t, apitest.Req{Method: m, Path: "/settlements/" + sp.ID, Token: other.Token, Household: oh.ID}).
				WantError(t, http.StatusNotFound, "not_found", "")
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/settlements", Token: other.Token, Household: h.id}).
			WantError(t, http.StatusNotFound, "not_found", "")
		h.do(t, h.owner, http.MethodGet, "/settlements/nope", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
	})
}

func TestSplitRules(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)

	type rulesJSON struct {
		HouseholdID string `json:"householdId"`
		Rules       []struct {
			UserID string  `json:"userId"`
			Weight float64 `json:"weight"`
		} `json:"rules"`
	}
	weights := func(t *testing.T, u apitest.User) map[string]float64 {
		t.Helper()
		var r rulesJSON
		h.do(t, u, http.MethodGet, "/split", nil).Status(t, http.StatusOK).JSON(t, &r)
		out := map[string]float64{}
		for _, x := range r.Rules {
			out[x.UserID] = x.Weight
		}
		return out
	}

	t.Run("every member starts at weight 1 and anyone can read", func(t *testing.T) {
		got := weights(t, h.carlos)
		if len(got) != 3 || got[h.owner.ID] != 1 || got[h.ana.ID] != 1 || got[h.carlos.ID] != 1 {
			t.Errorf("weights = %v", got)
		}
	})

	t.Run("owner updates; partial updates keep the rest", func(t *testing.T) {
		var r rulesJSON
		h.do(t, h.owner, http.MethodPatch, "/split", map[string]any{"items": []map[string]any{{"userId": h.ana.ID, "weight": 2.5}}}).
			Status(t, http.StatusOK).JSON(t, &r)
		if len(r.Rules) != 1 || r.Rules[0].Weight != 2.5 {
			t.Errorf("update response = %+v (only the changed rules)", r)
		}
		if got := weights(t, h.owner); got[h.ana.ID] != 2.5 || got[h.carlos.ID] != 1 {
			t.Errorf("weights = %v", got)
		}
	})

	t.Run("only the owner can change weights", func(t *testing.T) {
		h.do(t, h.ana, http.MethodPatch, "/split", map[string]any{"items": []map[string]any{{"userId": h.ana.ID, "weight": 10}}}).
			WantError(t, http.StatusForbidden, "forbidden", "")
	})

	t.Run("validation", func(t *testing.T) {
		stranger := s.Register(t)
		h.do(t, h.owner, http.MethodPatch, "/split", map[string]any{"items": []map[string]any{}}).WantError(t, http.StatusUnprocessableEntity, "validation", "items")
		h.do(t, h.owner, http.MethodPatch, "/split", map[string]any{"items": []map[string]any{{"userId": h.ana.ID, "weight": -1}}}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "weight")
		h.do(t, h.owner, http.MethodPatch, "/split", map[string]any{"items": []map[string]any{{"userId": stranger.ID, "weight": 1}}}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "userId")
		h.do(t, h.owner, http.MethodPatch, "/split", map[string]any{"rules": []any{}}).WantError(t, http.StatusUnprocessableEntity, "validation", "body")
	})

	t.Run("an outsider cannot read", func(t *testing.T) {
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/split", Token: s.Register(t).Token, Household: h.id}).
			WantError(t, http.StatusNotFound, "not_found", "")
	})
}
