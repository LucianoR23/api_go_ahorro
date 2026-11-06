package expenses_test

// Characterization tests of the expenses endpoints (item 0.21e): creation
// (cash, shared, weights, overrides, credit installments, FX), reads,
// filters, edits, installments, deletion and household isolation.

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// house is a household with an owner and a member, both with their cash.
type house struct {
	s             *apitest.Server
	id            string
	owner, member apitest.User
	ownerCash     string
	memberCash    string
}

func newHouse(t *testing.T, s *apitest.Server) house {
	t.Helper()
	owner := s.Register(t, apitest.RegisterOpts{FirstName: "Owner"})
	h := s.CreateHousehold(t, owner, "")
	member := s.AddMember(t, owner, h.ID)
	return house{s: s, id: h.ID, owner: owner, member: member,
		ownerCash: s.Cash(t, owner).ID, memberCash: s.Cash(t, member).ID}
}

func (h house) do(t testing.TB, u apitest.User, method, path string, body any) *apitest.Res {
	t.Helper()
	return h.s.Do(t, apitest.Req{Method: method, Path: path, Token: u.Token, Household: h.id, Body: body})
}

func (h house) cashExpense(t testing.TB, u apitest.User, amount float64, extra map[string]any) apitest.ExpenseDetail {
	t.Helper()
	pm := h.ownerCash
	if u.ID == h.member.ID {
		pm = h.memberCash
	}
	body := map[string]any{"paymentMethodId": pm, "amount": amount, "spentAt": "2026-01-10"}
	for k, v := range extra {
		body[k] = v
	}
	return h.s.CreateExpense(t, u, h.id, body)
}

func sharesOf(inst apitest.Installment) map[string]float64 {
	out := map[string]float64{}
	for _, s := range inst.Shares {
		out[s.UserID] = s.AmountBaseOwed
	}
	return out
}

func TestCreateExpense(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("cash: one paid installment, no shares", func(t *testing.T) {
		h := newHouse(t, s)
		d := h.cashExpense(t, h.owner, 1500.5, map[string]any{"description": "  Super ", "currency": "ars", "installments": 3})
		e := d.Expense
		if e.Description != "Super" || e.Currency != "ARS" || e.AmountBase != 1500.5 || e.BaseCurrency != "ARS" ||
			e.RateUsed != nil || e.Installments != 1 || e.Status != "confirmed" || e.CreatedBy != h.owner.ID || e.IsShared {
			t.Errorf("expense = %+v", e)
		}
		if len(d.Installments) != 1 {
			t.Fatalf("installments = %+v", d.Installments)
		}
		i := d.Installments[0]
		if !i.IsPaid || i.BillingDate != "2026-01-10" || i.DueDate != nil || i.InstallmentAmount != 1500.5 || len(i.Shares) != 0 {
			t.Errorf("installment = %+v", i)
		}
	})

	t.Run("shared: equal split by default, members notified in-app", func(t *testing.T) {
		h := newHouse(t, s)
		d := h.cashExpense(t, h.owner, 1000, map[string]any{"isShared": true, "description": "Luz"})
		got := sharesOf(d.Installments[0])
		if len(got) != 2 || got[h.owner.ID] != 500 || got[h.member.ID] != 500 {
			t.Errorf("shares = %v", got)
		}
		var n int
		if err := s.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM daily_insights WHERE user_id = $1 AND insight_type = 'shared_expense'`, h.member.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("shared-expense insights for the member = %d, want 1", n)
		}
	})

	t.Run("shared: split rule weights", func(t *testing.T) {
		h := newHouse(t, s)
		h.do(t, h.owner, http.MethodPatch, "/split", map[string]any{"items": []map[string]any{
			{"userId": h.owner.ID, "weight": 2}, {"userId": h.member.ID, "weight": 1},
		}}).Status(t, http.StatusOK)
		got := sharesOf(h.cashExpense(t, h.owner, 300, map[string]any{"isShared": true}).Installments[0])
		if got[h.owner.ID] != 200 || got[h.member.ID] != 100 {
			t.Errorf("weighted shares = %v", got)
		}

		// A member with weight 0 is left out of the split.
		h.do(t, h.owner, http.MethodPatch, "/split", map[string]any{"items": []map[string]any{{"userId": h.member.ID, "weight": 0}}}).
			Status(t, http.StatusOK)
		got = sharesOf(h.cashExpense(t, h.owner, 300, map[string]any{"isShared": true}).Installments[0])
		if len(got) != 1 || got[h.owner.ID] != 300 {
			t.Errorf("shares with a zero weight = %v", got)
		}
	})

	t.Run("shared: explicit amounts per member", func(t *testing.T) {
		h := newHouse(t, s)
		over := func(a, b float64) []map[string]any {
			return []map[string]any{{"userId": h.owner.ID, "amount": a}, {"userId": h.member.ID, "amount": b}}
		}
		got := sharesOf(h.cashExpense(t, h.owner, 100, map[string]any{"isShared": true, "sharesOverride": over(70, 30)}).Installments[0])
		if got[h.owner.ID] != 70 || got[h.member.ID] != 30 {
			t.Errorf("override shares = %v", got)
		}

		base := func(extra map[string]any) map[string]any {
			b := map[string]any{"paymentMethodId": h.ownerCash, "amount": 100, "currency": "ARS", "description": "x", "spentAt": "2026-01-10", "installments": 1}
			for k, v := range extra {
				b[k] = v
			}
			return b
		}
		stranger := s.Register(t)
		for name, tc := range map[string]struct {
			body  map[string]any
			field string
		}{
			"sum differs":       {base(map[string]any{"isShared": true, "sharesOverride": over(60, 30)}), "sharesOverride"},
			"not shared":        {base(map[string]any{"sharesOverride": over(70, 30)}), "sharesOverride"},
			"negative amount":   {base(map[string]any{"isShared": true, "sharesOverride": over(110, -10)}), "sharesOverride.amount"},
			"duplicated member": {base(map[string]any{"isShared": true, "sharesOverride": []map[string]any{{"userId": h.owner.ID, "amount": 50}, {"userId": h.owner.ID, "amount": 50}}}), "sharesOverride"},
			"not a member":      {base(map[string]any{"isShared": true, "sharesOverride": []map[string]any{{"userId": h.owner.ID, "amount": 50}, {"userId": stranger.ID, "amount": 50}}}), "sharesOverride"},
		} {
			t.Run(name, func(t *testing.T) {
				h.do(t, h.owner, http.MethodPost, "/expenses", tc.body).WantError(t, http.StatusUnprocessableEntity, "validation", tc.field)
			})
		}
	})

	t.Run("credit card: installments follow the card periods", func(t *testing.T) {
		h := newHouse(t, s)
		card := s.CreateCreditCard(t, h.owner, "Visa", 25, 5, "2026-11-26", "2026-12-07")
		d := s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": card.ID, "amount": 1000, "spentAt": "2026-11-10", "installments": 3})

		want := []struct {
			amount       float64
			billing, due string
		}{
			{333.33, "2026-11-26", "2026-12-07"}, // the loaded period override
			{333.33, "2026-12-25", "2027-01-05"}, // card defaults afterwards
			{333.34, "2027-01-25", "2027-02-05"}, // last one absorbs the remainder
		}
		if d.Expense.Installments != 3 || len(d.Installments) != 3 {
			t.Fatalf("detail = %+v", d)
		}
		for i, w := range want {
			got := d.Installments[i]
			if got.InstallmentNumber != i+1 || got.InstallmentAmount != w.amount || got.BillingDate != w.billing ||
				got.DueDate == nil || *got.DueDate != w.due || got.IsPaid {
				t.Errorf("installment %d = %+v, want %+v", i+1, got, w)
			}
		}

		late := s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": card.ID, "amount": 100, "spentAt": "2026-11-28"})
		if b := late.Installments[0].BillingDate; b != "2026-12-25" {
			t.Errorf("spent after the closing date: billed %s, want next period 2026-12-25", b)
		}
	})

	t.Run("foreign currency uses the stored blue rate", func(t *testing.T) {
		h := newHouse(t, s)
		// No rate yet: the FX lookup fails and surfaces as not found.
		h.do(t, h.owner, http.MethodPost, "/expenses", map[string]any{"paymentMethodId": h.ownerCash, "amount": 10, "currency": "USD",
			"description": "x", "spentAt": "2026-01-10", "installments": 1}).WantError(t, http.StatusNotFound, "not_found", "")

		if _, err := s.Pool.Exec(t.Context(), `INSERT INTO exchange_rates (currency, source, last_update, rate_avg, rate_buy, rate_sell)
			VALUES ('USD', 'blue', now(), 1200, 1190, 1210)`); err != nil {
			t.Fatal(err)
		}
		e := h.cashExpense(t, h.owner, 10, map[string]any{"currency": "USD"}).Expense
		if e.Amount != 10 || e.Currency != "USD" || e.AmountBase != 12000 || e.RateUsed == nil || *e.RateUsed != 1200 {
			t.Errorf("USD expense = %+v", e)
		}
	})

	t.Run("validation", func(t *testing.T) {
		h := newHouse(t, s)
		base := func(k string, v any) map[string]any {
			b := map[string]any{"paymentMethodId": h.ownerCash, "amount": 10, "currency": "ARS", "description": "x", "spentAt": "2026-01-10", "installments": 1}
			if v == nil {
				delete(b, k)
			} else {
				b[k] = v
			}
			return b
		}
		for _, tc := range []struct {
			key   string
			value any
			field string
		}{
			{"amount", 0, "amount"},
			{"amount", -5, "amount"},
			{"currency", "BRL", "currency"},
			{"description", "  ", "description"},
			{"spentAt", "10/01/2026", "spentAt"},
			{"spentAt", nil, "spentAt"},
			{"installments", 0, "installments"},
			{"installments", 61, "installments"},
			{"amount", "diez", "amount"},
			{"extra", true, "body"},
		} {
			t.Run(fmt.Sprintf("%s=%v", tc.key, tc.value), func(t *testing.T) {
				h.do(t, h.owner, http.MethodPost, "/expenses", base(tc.key, tc.value)).WantError(t, http.StatusUnprocessableEntity, "validation", tc.field)
			})
		}
	})

	t.Run("another user's payment method is not found", func(t *testing.T) {
		h := newHouse(t, s)
		h.do(t, h.owner, http.MethodPost, "/expenses", map[string]any{"paymentMethodId": h.memberCash, "amount": 10, "currency": "ARS",
			"description": "x", "spentAt": "2026-01-10", "installments": 1}).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("a category from another household is rejected", func(t *testing.T) {
		h := newHouse(t, s)
		other := s.Register(t)
		oh := s.CreateHousehold(t, other, "")
		var cats []struct {
			ID string `json:"id"`
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: other.Token, Household: oh.ID}).Status(t, http.StatusOK).JSON(t, &cats)

		res := h.do(t, h.owner, http.MethodPost, "/expenses", map[string]any{"paymentMethodId": h.ownerCash, "amount": 10, "currency": "ARS",
			"description": "x", "spentAt": "2026-01-10", "installments": 1, "categoryId": cats[0].ID})
		res.Status(t, http.StatusUnprocessableEntity)
	})
}

func TestReadAndFilterExpenses(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	var cats []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	h.do(t, h.owner, http.MethodGet, "/categories", nil).Status(t, http.StatusOK).JSON(t, &cats)
	cat := cats[0].ID

	a := h.cashExpense(t, h.owner, 100, map[string]any{"description": "Verdulería del barrio", "spentAt": "2026-01-05", "categoryId": cat}).Expense
	b := h.cashExpense(t, h.owner, 200, map[string]any{"description": "Nafta", "spentAt": "2026-02-10"}).Expense
	c := h.cashExpense(t, h.member, 300, map[string]any{"description": "Farmacia", "spentAt": "2026-03-15"}).Expense

	type listJSON struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		TotalCount int64 `json:"totalCount"`
		Limit      int   `json:"limit"`
		Offset     int   `json:"offset"`
	}
	ids := func(t *testing.T, query string) (string, listJSON) {
		t.Helper()
		var l listJSON
		h.do(t, h.member, http.MethodGet, "/expenses"+query, nil).Status(t, http.StatusOK).JSON(t, &l)
		var out []string
		for _, it := range l.Items {
			switch it.ID {
			case a.ID:
				out = append(out, "a")
			case b.ID:
				out = append(out, "b")
			case c.ID:
				out = append(out, "c")
			default:
				out = append(out, "?")
			}
		}
		return strings.Join(out, ","), l
	}

	for _, tc := range []struct{ query, want string }{
		{"", "c,b,a"}, // newest spentAt first, every member's expenses
		{"?categoryId=" + cat, "a"},
		{"?paymentMethodId=" + h.memberCash, "c"},
		{"?from=2026-02-01", "c,b"},
		{"?to=2026-02-10", "b,a"},
		{"?from=2026-02-01&to=2026-02-28", "b"},
		{"?q=verdu", "a"},
		{"?limit=1", "c"},
		{"?limit=1&offset=1", "b"},
	} {
		t.Run("list"+tc.query, func(t *testing.T) {
			got, l := ids(t, tc.query)
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
			if tc.query == "?limit=1&offset=1" && (l.TotalCount != 3 || l.Limit != 1 || l.Offset != 1) {
				t.Errorf("paging = %+v, want totalCount 3", l)
			}
		})
	}

	t.Run("list validation", func(t *testing.T) {
		for q, field := range map[string]string{
			"?categoryId=x": "categoryId", "?paymentMethodId=x": "paymentMethodId", "?from=2026-1-1": "from",
			"?to=hoy": "to", "?limit=0": "limit", "?limit=201": "limit", "?offset=-1": "offset",
			"?kind=recurring": "kind",
		} {
			h.do(t, h.owner, http.MethodGet, "/expenses"+q, nil).WantError(t, http.StatusUnprocessableEntity, "validation", field)
		}
	})

	t.Run("an offset past int32 is rejected", func(t *testing.T) {
		h.do(t, h.owner, http.MethodGet, "/expenses?offset=3000000000", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "offset")
	})

	t.Run("get one", func(t *testing.T) {
		var d apitest.ExpenseDetail
		h.do(t, h.owner, http.MethodGet, "/expenses/"+c.ID, nil).Status(t, http.StatusOK).JSON(t, &d)
		if d.Expense.ID != c.ID || d.Expense.CreatedBy != h.member.ID || len(d.Installments) != 1 {
			t.Errorf("detail = %+v", d)
		}
		h.do(t, h.owner, http.MethodGet, "/expenses/nope", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
		h.do(t, h.owner, http.MethodGet, "/expenses/00000000-0000-0000-0000-000000000001", nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("drafts list is empty without recurring expenses", func(t *testing.T) {
		res := h.do(t, h.owner, http.MethodGet, "/expenses/drafts", nil).Status(t, http.StatusOK)
		if strings.TrimSpace(string(res.Body)) != "[]" {
			t.Errorf("drafts = %s", res.Body)
		}
	})
}

func TestEditAndDeleteExpenses(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("edit description, date and category", func(t *testing.T) {
		h := newHouse(t, s)
		e := h.cashExpense(t, h.owner, 100, nil).Expense
		var got apitest.Expense
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"description": " Editado ", "spentAt": "2026-02-01"}).
			Status(t, http.StatusOK).JSON(t, &got)
		if got.Description != "Editado" || got.SpentAt != "2026-02-01" || got.Amount != 100 {
			t.Errorf("edited = %+v", got)
		}
		// Partial: an absent field is left as is.
		var partial apitest.Expense
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"description": "Solo la descripción"}).
			Status(t, http.StatusOK).JSON(t, &partial)
		if partial.Description != "Solo la descripción" || partial.SpentAt != "2026-02-01" {
			t.Errorf("partial edit = %+v", partial)
		}
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "body")
		// categoryId: absent keeps it, null removes it.
		var cats []struct {
			ID string `json:"id"`
		}
		h.do(t, h.owner, http.MethodGet, "/categories", nil).Status(t, http.StatusOK).JSON(t, &cats)
		var withCat, keeps, cleared apitest.Expense
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"categoryId": cats[0].ID}).Status(t, http.StatusOK).JSON(t, &withCat)
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"description": "Otra"}).Status(t, http.StatusOK).JSON(t, &keeps)
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"categoryId": nil}).Status(t, http.StatusOK).JSON(t, &cleared)
		if withCat.CategoryID == nil || keeps.CategoryID == nil || *keeps.CategoryID != cats[0].ID || cleared.CategoryID != nil {
			t.Errorf("category set/kept/cleared = %v %v %v", withCat.CategoryID, keeps.CategoryID, cleared.CategoryID)
		}
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"description": nil}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "description")
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"spentAt": nil}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "spentAt")
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"description": "", "spentAt": "2026-02-01"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "description")
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"description": "x", "spentAt": ""}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "spentAt")
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"description": "x", "spentAt": "2026-02-01", "amount": 5}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "body")
	})

	t.Run("installments: mark paid, move dates", func(t *testing.T) {
		h := newHouse(t, s)
		card := s.CreateCreditCard(t, h.owner, "Visa", 25, 5, "2026-11-26", "2026-12-07")
		e := s.CreateExpense(t, h.owner, h.id, map[string]any{"paymentMethodId": card.ID, "amount": 200, "spentAt": "2026-11-10", "installments": 2}).Expense
		path := "/expenses/" + e.ID + "/installments/"

		var before apitest.ExpenseDetail
		h.do(t, h.owner, http.MethodGet, "/expenses/"+e.ID, nil).Status(t, http.StatusOK).JSON(t, &before)
		autoBilling := before.Installments[1].BillingDate

		var inst apitest.Installment
		h.do(t, h.owner, http.MethodPatch, path+"1", map[string]any{"isPaid": true}).Status(t, http.StatusOK).JSON(t, &inst)
		if !inst.IsPaid || inst.InstallmentNumber != 1 {
			t.Errorf("paid = %+v", inst)
		}
		h.do(t, h.owner, http.MethodPatch, path+"2", map[string]any{"billingDate": "2026-12-20", "dueDate": "2027-01-02"}).
			Status(t, http.StatusOK).JSON(t, &inst)
		if inst.BillingDate != "2026-12-20" || inst.DueDate == nil || *inst.DueDate != "2027-01-02" {
			t.Errorf("moved = %+v", inst)
		}
		var cleared apitest.Installment // fresh value: the API omits a null dueDate
		h.do(t, h.owner, http.MethodPatch, path+"2", map[string]any{"dueDate": nil}).Status(t, http.StatusOK).JSON(t, &cleared)
		if cleared.DueDate != nil {
			t.Errorf("dueDate not cleared: %v", *cleared.DueDate)
		}

		h.do(t, h.owner, http.MethodPatch, path+"2", map[string]any{"dueDate": "2026-12-01"}).WantError(t, http.StatusUnprocessableEntity, "validation", "dueDate")
		// billingDate: null puts the installment back on the card's own date.
		var reset apitest.Installment
		h.do(t, h.owner, http.MethodPatch, path+"2", map[string]any{"billingDate": nil, "isPaid": true}).
			Status(t, http.StatusOK).JSON(t, &reset)
		if reset.BillingDate != autoBilling || autoBilling == "2026-12-20" || !reset.IsPaid {
			t.Errorf("billingDate reset to %s, want the automatic %s (paid %v)", reset.BillingDate, autoBilling, reset.IsPaid)
		}
		h.do(t, h.owner, http.MethodPatch, path+"2", map[string]any{"dueDate": 5}).WantError(t, http.StatusUnprocessableEntity, "validation", "dueDate")
		h.do(t, h.owner, http.MethodPatch, path+"2", map[string]any{"billingDate": "mañana"}).WantError(t, http.StatusUnprocessableEntity, "validation", "billingDate")
		h.do(t, h.owner, http.MethodPatch, path+"2", map[string]any{}).WantError(t, http.StatusUnprocessableEntity, "validation", "body")
		h.do(t, h.owner, http.MethodPatch, path+"0", map[string]any{"isPaid": true}).WantError(t, http.StatusUnprocessableEntity, "validation", "n")
		h.do(t, h.owner, http.MethodPatch, path+"3", map[string]any{"isPaid": true}).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("delete", func(t *testing.T) {
		h := newHouse(t, s)
		e := h.cashExpense(t, h.owner, 100, nil).Expense
		h.do(t, h.owner, http.MethodDelete, "/expenses/"+e.ID, nil).Status(t, http.StatusNoContent)
		h.do(t, h.owner, http.MethodGet, "/expenses/"+e.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
		h.do(t, h.owner, http.MethodDelete, "/expenses/"+e.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("confirm only applies to drafts", func(t *testing.T) {
		h := newHouse(t, s)
		e := h.cashExpense(t, h.owner, 100, nil).Expense
		h.do(t, h.owner, http.MethodPost, "/expenses/"+e.ID+"/confirm", map[string]any{"amount": 100}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "status")
		h.do(t, h.owner, http.MethodPost, "/expenses/"+e.ID+"/confirm", map[string]any{"amount": 0}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "amount")
	})

	t.Run("another member cannot edit or delete my expense; the household owner can", func(t *testing.T) {
		h := newHouse(t, s)
		mine := h.cashExpense(t, h.owner, 100, map[string]any{"description": "Mío"}).Expense
		edit := h.do(t, h.member, http.MethodPatch, "/expenses/"+mine.ID, map[string]any{"description": "Ajeno", "spentAt": "2026-01-10"})
		edit.Status(t, http.StatusForbidden)
		del := h.do(t, h.member, http.MethodDelete, "/expenses/"+mine.ID, nil)
		del.Status(t, http.StatusForbidden)

		theirs := h.cashExpense(t, h.member, 50, map[string]any{"description": "Del miembro"}).Expense
		h.do(t, h.owner, http.MethodPatch, "/expenses/"+theirs.ID, map[string]any{"description": "Corregido"}).Status(t, http.StatusOK)
		h.do(t, h.owner, http.MethodDelete, "/expenses/"+theirs.ID, nil).Status(t, http.StatusNoContent)
	})

	t.Run("a category from another household is rejected on edit", func(t *testing.T) {
		h := newHouse(t, s)
		e := h.cashExpense(t, h.owner, 100, nil).Expense
		other := s.Register(t)
		oh := s.CreateHousehold(t, other, "")
		var cats []struct {
			ID string `json:"id"`
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: other.Token, Household: oh.ID}).Status(t, http.StatusOK).JSON(t, &cats)
		res := h.do(t, h.owner, http.MethodPatch, "/expenses/"+e.ID, map[string]any{"description": "x", "spentAt": "2026-01-10", "categoryId": cats[0].ID})
		res.Status(t, http.StatusUnprocessableEntity)
	})
}

func TestExpensesAreHouseholdScoped(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	e := h.cashExpense(t, h.owner, 100, nil).Expense

	// Owner of another household, sending their own household header.
	other := s.Register(t)
	oh := s.CreateHousehold(t, other, "")
	call := func(method, path string, body any) *apitest.Res {
		return s.Do(t, apitest.Req{Method: method, Path: path, Token: other.Token, Household: oh.ID, Body: body})
	}
	call(http.MethodGet, "/expenses/"+e.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	call(http.MethodPatch, "/expenses/"+e.ID, map[string]any{"description": "x", "spentAt": "2026-01-10"}).WantError(t, http.StatusNotFound, "not_found", "")
	call(http.MethodDelete, "/expenses/"+e.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	call(http.MethodPatch, "/expenses/"+e.ID+"/installments/1", map[string]any{"isPaid": false}).WantError(t, http.StatusNotFound, "not_found", "")
	call(http.MethodPost, "/expenses/"+e.ID+"/confirm", map[string]any{"amount": 1}).WantError(t, http.StatusNotFound, "not_found", "")
	var l struct {
		TotalCount int64 `json:"totalCount"`
	}
	call(http.MethodGet, "/expenses", nil).Status(t, http.StatusOK).JSON(t, &l)
	if l.TotalCount != 0 {
		t.Errorf("other household sees %d expenses", l.TotalCount)
	}

	// Their token with my household header: not a member.
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/expenses", Token: other.Token, Household: h.id}).WantError(t, http.StatusNotFound, "not_found", "")
	s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/expenses", Household: h.id}).WantError(t, http.StatusUnauthorized, "unauthorized", "")
}
