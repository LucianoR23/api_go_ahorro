package recurringexpenses_test

// Characterization tests of recurring expenses: CRUD, the first occurrence
// booked on creation, the daily job, variable amounts as drafts, draft
// confirmation, the spike insight and the series stats (item 0.21f).

import (
	"context"
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

type recurringJSON struct {
	ID                string   `json:"id"`
	CreatedBy         string   `json:"createdBy"`
	PaymentMethodID   string   `json:"paymentMethodId"`
	Amount            float64  `json:"amount"`
	Installments      int      `json:"installments"`
	IsShared          bool     `json:"isShared"`
	Frequency         string   `json:"frequency"`
	IsActive          bool     `json:"isActive"`
	StartsAt          string   `json:"startsAt"`
	LastGenerated     *string  `json:"lastGenerated"`
	AmountIsVariable  bool     `json:"amountIsVariable"`
	AlertThresholdPct *float64 `json:"alertThresholdPct"`
	LastAmount        *float64 `json:"lastAmount"`
}

type house struct {
	s             *apitest.Server
	id            string
	owner, member apitest.User
	cash          string
}

func newHouse(t *testing.T, s *apitest.Server) house {
	t.Helper()
	owner := s.Register(t, apitest.RegisterOpts{FirstName: "Owner"})
	h := s.CreateHousehold(t, owner, "")
	return house{s: s, id: h.ID, owner: owner, member: s.AddMember(t, owner, h.ID), cash: s.Cash(t, owner).ID}
}

func (h house) do(t testing.TB, u apitest.User, method, path string, body any) *apitest.Res {
	t.Helper()
	return h.s.Do(t, apitest.Req{Method: method, Path: path, Token: u.Token, Household: h.id, Body: body})
}

func (h house) body(extra map[string]any) map[string]any {
	b := map[string]any{"paymentMethodId": h.cash, "amount": 15000, "currency": "ARS", "description": "Internet",
		"installments": 1, "frequency": "monthly", "dayOfMonth": 10, "startsAt": "2099-01-01"}
	for k, v := range extra {
		if v == nil {
			delete(b, k)
		} else {
			b[k] = v
		}
	}
	return b
}

func (h house) create(t testing.TB, extra map[string]any) recurringJSON {
	t.Helper()
	var r recurringJSON
	h.do(t, h.owner, http.MethodPost, "/recurring-expenses", h.body(extra)).Status(t, http.StatusCreated).JSON(t, &r)
	return r
}

// startFrom moves a template's start into the past, so the job can be run
// for fixed dates without the create call booking anything.
func (h house) startFrom(t testing.TB, id, date string) {
	t.Helper()
	if _, err := h.s.Pool.Exec(t.Context(), `UPDATE recurring_expenses SET starts_at = $2 WHERE id = $1`, id, date); err != nil {
		t.Fatal(err)
	}
}

func (h house) runJob(t testing.TB, date string) {
	t.Helper()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.s.Jobs.RecurringExpenses(context.Background(), d); err != nil {
		t.Fatalf("job %s: %v", date, err)
	}
}

func (h house) seriesExpenses(t testing.TB, id string) []apitest.Expense {
	t.Helper()
	var l struct {
		Items []apitest.Expense `json:"items"`
	}
	h.do(t, h.owner, http.MethodGet, "/expenses?limit=200", nil).Status(t, http.StatusOK).JSON(t, &l)
	var drafts []apitest.Expense
	h.do(t, h.owner, http.MethodGet, "/expenses/drafts", nil).Status(t, http.StatusOK).JSON(t, &drafts)
	seen := map[string]bool{}
	var out []apitest.Expense
	for _, e := range append(l.Items, drafts...) {
		if !seen[e.ID] {
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	var n int
	_ = h.s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM expenses WHERE recurring_expense_id = $1`, id).Scan(&n)
	if n != len(out) {
		t.Fatalf("series %s has %d expenses in the database, %d through the API", id, n, len(out))
	}
	return out
}

func TestRecurringExpensesCRUD(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("create, list, update, deactivate, delete", func(t *testing.T) {
		h := newHouse(t, s)
		r := h.create(t, nil)
		if !r.IsActive || r.CreatedBy != h.owner.ID || r.LastGenerated != nil || r.AmountIsVariable {
			t.Errorf("created = %+v", r)
		}
		var list []recurringJSON
		h.do(t, h.member, http.MethodGet, "/recurring-expenses", nil).Status(t, http.StatusOK).JSON(t, &list)
		if len(list) != 1 || list[0].ID != r.ID {
			t.Errorf("list = %+v", list)
		}

		update := h.body(map[string]any{"amount": 18000, "description": "Internet fibra", "isShared": true, "startsAt": nil})
		var upd recurringJSON
		h.do(t, h.owner, http.MethodPatch, "/recurring-expenses/"+r.ID, update).Status(t, http.StatusOK).JSON(t, &upd)
		if upd.Amount != 18000 || !upd.IsShared {
			t.Errorf("updated = %+v", upd)
		}

		h.do(t, h.owner, http.MethodPatch, "/recurring-expenses/"+r.ID+"/active", map[string]any{"isActive": false}).Status(t, http.StatusNoContent)
		var got recurringJSON
		h.do(t, h.owner, http.MethodGet, "/recurring-expenses/"+r.ID, nil).Status(t, http.StatusOK).JSON(t, &got)
		if got.IsActive {
			t.Errorf("still active")
		}
		h.do(t, h.owner, http.MethodDelete, "/recurring-expenses/"+r.ID, nil).Status(t, http.StatusNoContent)
		h.do(t, h.owner, http.MethodGet, "/recurring-expenses/"+r.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("validation", func(t *testing.T) {
		h := newHouse(t, s)
		for name, tc := range map[string]struct {
			extra map[string]any
			field string
		}{
			"zero amount":                {map[string]any{"amount": 0}, "amount"},
			"empty description":          {map[string]any{"description": " "}, "description"},
			"61 installments":            {map[string]any{"installments": 61}, "installments"},
			"bad frequency":              {map[string]any{"frequency": "daily"}, "frequency"},
			"monthly without day":        {map[string]any{"dayOfMonth": nil}, "dayOfMonth"},
			"weekly without day":         {map[string]any{"frequency": "weekly"}, "dayOfWeek"},
			"yearly without month":       {map[string]any{"frequency": "yearly"}, "monthOfYear"},
			"threshold without variable": {map[string]any{"alertThresholdPct": 20}, "alertThresholdPct"},
			"threshold over 500":         {map[string]any{"amountIsVariable": true, "alertThresholdPct": 501}, "alertThresholdPct"},
			"variable with installments": {map[string]any{"amountIsVariable": true, "installments": 3}, "installments"},
			"bad startsAt":               {map[string]any{"startsAt": "x"}, "startsAt"},
		} {
			t.Run(name, func(t *testing.T) {
				h.do(t, h.owner, http.MethodPost, "/recurring-expenses", h.body(tc.extra)).WantError(t, http.StatusUnprocessableEntity, "validation", tc.field)
			})
		}
	})

	t.Run("another user's payment method is accepted, then every occurrence fails", func(t *testing.T) {
		h := newHouse(t, s)
		res := h.do(t, h.owner, http.MethodPost, "/recurring-expenses", h.body(map[string]any{"paymentMethodId": s.Cash(t, h.member).ID}))
		testutil.KnownBug(t, "0.6", "a recurring expense's payment_method_id is not checked against the creator", func(t testing.TB) {
			if res.Code == http.StatusCreated {
				t.Errorf("template created on another user's payment method (its expenses can never be generated)")
			}
		})
	})

	t.Run("household scoped", func(t *testing.T) {
		h := newHouse(t, s)
		r := h.create(t, nil)
		other := s.Register(t)
		oh := s.CreateHousehold(t, other, "")
		for _, req := range []apitest.Req{
			{Method: http.MethodGet, Path: "/recurring-expenses/" + r.ID},
			{Method: http.MethodGet, Path: "/recurring-expenses/" + r.ID + "/stats"},
			{Method: http.MethodPatch, Path: "/recurring-expenses/" + r.ID + "/active", Body: map[string]any{"isActive": false}},
			{Method: http.MethodDelete, Path: "/recurring-expenses/" + r.ID},
		} {
			req.Token, req.Household = other.Token, oh.ID
			s.Do(t, req).WantError(t, http.StatusNotFound, "not_found", "")
		}
		h.do(t, h.owner, http.MethodGet, "/recurring-expenses/nope", nil).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
	})
}

func TestRecurringExpenseGeneration(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("a past start books the latest occurrence on creation", func(t *testing.T) {
		h := newHouse(t, s)
		now := time.Now()
		due := now.AddDate(0, 0, -3)
		r := h.create(t, map[string]any{"frequency": "weekly", "dayOfMonth": nil, "dayOfWeek": int(due.Weekday()),
			"startsAt": now.AddDate(0, 0, -30).Format("2006-01-02")})
		if r.LastGenerated == nil || *r.LastGenerated != due.Format("2006-01-02") {
			t.Errorf("lastGenerated = %v, want %s", r.LastGenerated, due.Format("2006-01-02"))
		}
		got := h.seriesExpenses(t, r.ID)
		if len(got) != 1 || got[0].SpentAt != due.Format("2006-01-02") || got[0].Amount != 15000 {
			t.Fatalf("generated = %+v (only the latest occurrence)", got)
		}
		var d apitest.ExpenseDetail
		h.do(t, h.owner, http.MethodGet, "/expenses/"+got[0].ID, nil).Status(t, http.StatusOK).JSON(t, &d)
		if d.Expense.Status != "confirmed" {
			t.Errorf("status = %q, want confirmed (fixed amount)", d.Expense.Status)
		}
	})

	t.Run("daily job: matching days only, once per day, shared split, stops when inactive", func(t *testing.T) {
		h := newHouse(t, s)
		r := h.create(t, map[string]any{"dayOfMonth": 31, "isShared": true})
		h.startFrom(t, r.ID, "2026-01-01")

		h.runJob(t, "2026-02-27")
		if n := len(h.seriesExpenses(t, r.ID)); n != 0 {
			t.Errorf("generated on a non-matching day: %d", n)
		}
		h.runJob(t, "2026-02-28") // 31 clamps to February's last day
		h.runJob(t, "2026-02-28")
		got := h.seriesExpenses(t, r.ID)
		if len(got) != 1 || got[0].SpentAt != "2026-02-28" || !got[0].IsShared {
			t.Fatalf("after Feb 28 twice = %+v", got)
		}
		var d apitest.ExpenseDetail
		h.do(t, h.owner, http.MethodGet, "/expenses/"+got[0].ID, nil).Status(t, http.StatusOK).JSON(t, &d)
		if len(d.Installments) != 1 || len(d.Installments[0].Shares) != 2 {
			t.Errorf("generated expense not split: %+v", d.Installments)
		}
		res := h.do(t, h.owner, http.MethodGet, "/expenses/"+got[0].ID, nil)
		testutil.KnownBug(t, "0.1", "the expense JSON has no recurringExpenseId, so the app cannot tell fixed expenses or find a draft's series", func(t testing.TB) {
			if !strings.Contains(string(res.Body), `"recurringExpenseId":"`+r.ID+`"`) {
				t.Errorf("expense %s does not say it belongs to series %s", got[0].ID, r.ID)
			}
		})

		h.do(t, h.owner, http.MethodPatch, "/recurring-expenses/"+r.ID+"/active", map[string]any{"isActive": false}).Status(t, http.StatusNoContent)
		h.runJob(t, "2026-03-31")
		if n := len(h.seriesExpenses(t, r.ID)); n != 1 {
			t.Errorf("inactive template generated: %d expenses", n)
		}
	})
}

func TestVariableAmountDrafts(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("drafts, confirmation, last amount, spike insight and stats", func(t *testing.T) {
		h := newHouse(t, s)
		r := h.create(t, map[string]any{"description": "Luz", "amount": 10000, "amountIsVariable": true, "alertThresholdPct": 20})
		h.startFrom(t, r.ID, "2026-01-01")

		// January: a draft with the template amount (no last amount yet).
		h.runJob(t, "2026-01-10")
		var drafts []apitest.Expense
		h.do(t, h.owner, http.MethodGet, "/expenses/drafts", nil).Status(t, http.StatusOK).JSON(t, &drafts)
		if len(drafts) != 1 || drafts[0].Status != "draft" || drafts[0].Amount != 10000 {
			t.Fatalf("drafts = %+v", drafts)
		}

		var confirmed apitest.Expense
		h.do(t, h.member, http.MethodPost, "/expenses/"+drafts[0].ID+"/confirm", map[string]any{"amount": 12000}).
			Status(t, http.StatusOK).JSON(t, &confirmed)
		if confirmed.Status != "confirmed" || confirmed.Amount != 12000 || confirmed.AmountBase != 12000 {
			t.Errorf("confirmed = %+v", confirmed)
		}
		h.do(t, h.owner, http.MethodPost, "/expenses/"+drafts[0].ID+"/confirm", map[string]any{"amount": 1}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "status")

		var tpl recurringJSON
		h.do(t, h.owner, http.MethodGet, "/recurring-expenses/"+r.ID, nil).Status(t, http.StatusOK).JSON(t, &tpl)
		if tpl.LastAmount == nil || *tpl.LastAmount != 12000 {
			t.Errorf("lastAmount = %v, want 12000", tpl.LastAmount)
		}

		// February: the draft starts from the last confirmed amount; confirming
		// 25% more (threshold 20%) warns the template's creator.
		h.runJob(t, "2026-02-10")
		drafts = nil
		h.do(t, h.owner, http.MethodGet, "/expenses/drafts", nil).Status(t, http.StatusOK).JSON(t, &drafts)
		if len(drafts) != 1 || drafts[0].Amount != 12000 {
			t.Fatalf("february drafts = %+v", drafts)
		}
		h.do(t, h.owner, http.MethodPost, "/expenses/"+drafts[0].ID+"/confirm", map[string]any{"amount": 15000}).Status(t, http.StatusOK)

		var spikes int
		if err := s.Pool.QueryRow(t.Context(), `SELECT count(*) FROM daily_insights WHERE user_id = $1 AND insight_type = 'recurring_spike'`, h.owner.ID).Scan(&spikes); err != nil {
			t.Fatal(err)
		}
		if spikes != 1 {
			t.Errorf("recurring_spike insights for the creator = %d, want 1", spikes)
		}

		var st struct {
			RecurringExpenseID string `json:"recurringExpenseId"`
			History            []struct {
				Amount       float64  `json:"amount"`
				SpentAt      string   `json:"spentAt"`
				VariationPct *float64 `json:"variationPct"`
			} `json:"history"`
			AverageLastN     float64  `json:"averageLastN"`
			LastVariationPct *float64 `json:"lastVariationPct"`
		}
		h.do(t, h.owner, http.MethodGet, "/recurring-expenses/"+r.ID+"/stats", nil).Status(t, http.StatusOK).JSON(t, &st)
		if len(st.History) != 2 || st.History[0].SpentAt != "2026-02-10" || st.History[0].Amount != 15000 ||
			st.AverageLastN != 13500 || st.LastVariationPct == nil || *st.LastVariationPct != 25 || st.History[1].VariationPct != nil {
			t.Errorf("stats = %+v", st)
		}
	})

	t.Run("confirming a shared draft keeps the shares of the draft amount", func(t *testing.T) {
		h := newHouse(t, s)
		r := h.create(t, map[string]any{"description": "Gas", "amount": 1000, "amountIsVariable": true, "isShared": true})
		h.startFrom(t, r.ID, "2026-01-01")
		h.runJob(t, "2026-01-10")
		var drafts []apitest.Expense
		h.do(t, h.owner, http.MethodGet, "/expenses/drafts", nil).Status(t, http.StatusOK).JSON(t, &drafts)
		h.do(t, h.owner, http.MethodPost, "/expenses/"+drafts[0].ID+"/confirm", map[string]any{"amount": 3000}).Status(t, http.StatusOK)

		var d apitest.ExpenseDetail
		h.do(t, h.owner, http.MethodGet, "/expenses/"+drafts[0].ID, nil).Status(t, http.StatusOK).JSON(t, &d)
		testutil.KnownBug(t, "0.4", "confirming a draft updates the expense amount but not its installment and shares", func(t testing.TB) {
			inst := d.Installments[0]
			total := 0.0
			for _, sh := range inst.Shares {
				total += sh.AmountBaseOwed
			}
			if inst.InstallmentAmount != 3000 || total != 3000 {
				t.Errorf("installment %v, shares total %v; want both 3000", inst.InstallmentAmount, total)
			}
		})
	})

	t.Run("drafts count as spending before they are confirmed", func(t *testing.T) {
		h := newHouse(t, s)
		r := h.create(t, map[string]any{"description": "Agua", "amount": 900, "amountIsVariable": true, "isShared": true})
		h.startFrom(t, r.ID, "2026-01-01")
		h.runJob(t, "2026-01-10")

		var drafts []apitest.Expense
		h.do(t, h.owner, http.MethodGet, "/expenses/drafts", nil).Status(t, http.StatusOK).JSON(t, &drafts)
		if len(drafts) != 1 {
			t.Fatalf("drafts = %+v", drafts)
		}
		var l struct {
			Items []apitest.Expense `json:"items"`
		}
		h.do(t, h.owner, http.MethodGet, "/expenses", nil).Status(t, http.StatusOK).JSON(t, &l)
		testutil.KnownBug(t, "0.4", "the expense list includes unconfirmed drafts by default", func(t testing.TB) {
			for _, e := range l.Items {
				if e.ID == drafts[0].ID {
					t.Errorf("draft %s listed as spending", e.ID)
				}
			}
		})
		testutil.KnownBug(t, "0.4", "the expense list does not expose status (always empty)", func(t testing.TB) {
			for _, e := range l.Items {
				if e.Status == "" {
					t.Errorf("expense %s listed without status", e.ID)
				}
			}
		})
		var b struct {
			Balances []any `json:"balances"`
		}
		h.do(t, h.owner, http.MethodGet, "/balances", nil).Status(t, http.StatusOK).JSON(t, &b)
		testutil.KnownBug(t, "0.4", "balances count the shares of unconfirmed drafts", func(t testing.TB) {
			if len(b.Balances) != 0 {
				t.Errorf("a draft created a debt: %v", b.Balances)
			}
		})
	})
}
