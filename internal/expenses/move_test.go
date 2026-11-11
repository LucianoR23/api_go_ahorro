package expenses_test

// Moving an expense between Mi plata and a hogar (plan 9.1).

import (
	"net/http"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

func (h house) in(t testing.TB, u apitest.User, space, method, path string, body any) *apitest.Res {
	t.Helper()
	return h.s.Do(t, apitest.Req{Method: method, Path: path, Token: u.Token, Household: space, Body: body})
}

func (h house) category(t testing.TB, u apitest.User, space, name, parentID string) string {
	t.Helper()
	body := map[string]any{"name": name}
	if parentID != "" {
		body["parentId"] = parentID
	}
	var c struct {
		ID string `json:"id"`
	}
	h.in(t, u, space, http.MethodPost, "/categories", body).Status(t, http.StatusCreated).JSON(t, &c)
	return c.ID
}

func (h house) move(t testing.TB, u apitest.User, from, id, to string) *apitest.Res {
	t.Helper()
	body := map[string]any{}
	if to != "" {
		body["householdId"] = to
	}
	return h.in(t, u, from, http.MethodPost, "/expenses/"+id+"/move", body)
}

func TestMoveExpense(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("from Mi plata to the hogar it splits, keeps its id and finds the category by name", func(t *testing.T) {
		h := newHouse(t, s)
		mine := s.PersonalSpace(t, h.owner)
		parent := h.category(t, h.owner, mine, "Gatitos", "")
		child := h.category(t, h.owner, mine, "Alimento", parent)
		hogarParent := h.category(t, h.owner, h.id, "gatitos ", "")
		hogarChild := h.category(t, h.owner, h.id, "ALIMENTO", hogarParent)

		e := s.CreateExpense(t, h.owner, mine, map[string]any{"paymentMethodId": h.ownerCash, "amount": 45000,
			"spentAt": "2026-11-03", "categoryId": child}).Expense
		if e.IsShared {
			t.Fatal("a Mi plata expense starts unshared")
		}

		var moved apitest.ExpenseDetail
		h.move(t, h.owner, mine, e.ID, h.id).Status(t, http.StatusOK).JSON(t, &moved)
		if moved.Expense.ID != e.ID || moved.Expense.HouseholdID != h.id || !moved.Expense.IsShared {
			t.Errorf("moved = %+v", moved.Expense)
		}
		if moved.Expense.CategoryID == nil || *moved.Expense.CategoryID != hogarChild {
			t.Errorf("category = %v, want %s", moved.Expense.CategoryID, hogarChild)
		}
		got := sharesOf(moved.Installments[0])
		if got[h.owner.ID] != 22500 || got[h.member.ID] != 22500 {
			t.Errorf("shares = %v", got)
		}
		// It is gone from Mi plata and the member sees it in the hogar.
		h.in(t, h.owner, mine, http.MethodGet, "/expenses/"+e.ID, nil).Status(t, http.StatusNotFound)
		h.do(t, h.member, http.MethodGet, "/expenses/"+e.ID, nil).Status(t, http.StatusOK)
	})

	t.Run("back to Mi plata it stops splitting", func(t *testing.T) {
		h := newHouse(t, s)
		mine := s.PersonalSpace(t, h.owner)
		e := h.cashExpense(t, h.owner, 1000, map[string]any{"isShared": true}).Expense

		var moved apitest.ExpenseDetail
		h.move(t, h.owner, h.id, e.ID, mine).Status(t, http.StatusOK).JSON(t, &moved)
		if moved.Expense.HouseholdID != mine || moved.Expense.IsShared || len(moved.Installments[0].Shares) != 0 {
			t.Errorf("moved = %+v %+v", moved.Expense, moved.Installments)
		}
		h.do(t, h.member, http.MethodGet, "/expenses/"+e.ID, nil).Status(t, http.StatusNotFound)
	})

	t.Run("the member hears it arrive", func(t *testing.T) {
		h := newHouse(t, s)
		mine := s.PersonalSpace(t, h.owner)
		e := s.CreateExpense(t, h.owner, mine, map[string]any{"paymentMethodId": h.ownerCash, "amount": 800,
			"spentAt": "2026-11-03", "description": "Súper"}).Expense
		titles := func() []string {
			rows, err := s.Pool.Query(t.Context(),
				`SELECT title FROM daily_insights WHERE user_id = $1 AND insight_type = 'shared_expense' ORDER BY created_at`, h.member.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var out []string
			for rows.Next() {
				var s string
				if err := rows.Scan(&s); err != nil {
					t.Fatal(err)
				}
				out = append(out, s)
			}
			return out
		}
		h.move(t, h.owner, mine, e.ID, h.id).Status(t, http.StatusOK)
		h.move(t, h.owner, h.id, e.ID, mine).Status(t, http.StatusOK)
		// Leaving goes by push only: event insights are one per expense and
		// member, like the notices of an edit or a deletion.
		if got := titles(); len(got) != 1 || got[0] != "Nuevo gasto compartido" {
			t.Errorf("member insights = %q", got)
		}
	})

	t.Run("installments move with their paid state", func(t *testing.T) {
		h := newHouse(t, s)
		mine := s.PersonalSpace(t, h.owner)
		card := s.CreateCreditCard(t, h.owner, "Visa", 25, 5, "2026-11-26", "2026-12-07")
		e := s.CreateExpense(t, h.owner, mine, map[string]any{"paymentMethodId": card.ID, "amount": 300,
			"spentAt": "2026-11-10", "installments": 3}).Expense
		h.in(t, h.owner, mine, http.MethodPatch, "/expenses/"+e.ID+"/installments/1", map[string]any{"isPaid": true}).
			Status(t, http.StatusOK)

		var moved apitest.ExpenseDetail
		h.move(t, h.owner, mine, e.ID, h.id).Status(t, http.StatusOK).JSON(t, &moved)
		if len(moved.Installments) != 3 || !moved.Installments[0].IsPaid || moved.Installments[1].IsPaid {
			t.Fatalf("installments = %+v", moved.Installments)
		}
		for _, inst := range moved.Installments {
			if got := sharesOf(inst); got[h.owner.ID] != 50 || got[h.member.ID] != 50 {
				t.Errorf("installment %d shares = %v", inst.InstallmentNumber, got)
			}
		}
		var stored int
		if err := s.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM expense_installments WHERE expense_id = $1 AND household_id = $2`, e.ID, h.id).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != 3 {
			t.Errorf("installments in the hogar = %d, want 3", stored)
		}
	})

	t.Run("a subcategory missing in the target falls back to its parent, else none", func(t *testing.T) {
		h := newHouse(t, s)
		mine := s.PersonalSpace(t, h.owner)
		parent := h.category(t, h.owner, mine, "Acuario", "")
		child := h.category(t, h.owner, mine, "Peces", parent)
		lonely := h.category(t, h.owner, mine, "Rarezas", "")
		hogarParent := h.category(t, h.owner, h.id, "Acuario", "")

		e1 := s.CreateExpense(t, h.owner, mine, map[string]any{"paymentMethodId": h.ownerCash, "amount": 10, "spentAt": "2026-11-03", "categoryId": child}).Expense
		e2 := s.CreateExpense(t, h.owner, mine, map[string]any{"paymentMethodId": h.ownerCash, "amount": 10, "spentAt": "2026-11-03", "categoryId": lonely}).Expense
		var a, b apitest.ExpenseDetail
		h.move(t, h.owner, mine, e1.ID, h.id).Status(t, http.StatusOK).JSON(t, &a)
		h.move(t, h.owner, mine, e2.ID, h.id).Status(t, http.StatusOK).JSON(t, &b)
		if a.Expense.CategoryID == nil || *a.Expense.CategoryID != hogarParent {
			t.Errorf("fallback = %v, want %s", a.Expense.CategoryID, hogarParent)
		}
		if b.Expense.CategoryID != nil {
			t.Errorf("no match = %v, want none", *b.Expense.CategoryID)
		}
	})

	t.Run("only the author moves it, to a space of theirs", func(t *testing.T) {
		h := newHouse(t, s)
		ownerMine := s.PersonalSpace(t, h.owner)
		memberMine := s.PersonalSpace(t, h.member)
		byMember := h.cashExpense(t, h.member, 100, map[string]any{"isShared": true}).Expense
		byOwner := h.cashExpense(t, h.owner, 100, map[string]any{"isShared": true}).Expense

		// Not even the owner of the hogar takes someone else's expense.
		h.move(t, h.owner, h.id, byMember.ID, ownerMine).WantError(t, http.StatusForbidden, "forbidden", "")
		h.move(t, h.member, h.id, byOwner.ID, memberMine).WantError(t, http.StatusForbidden, "forbidden", "")
		// Someone else's Mi plata is not a target.
		h.move(t, h.owner, h.id, byOwner.ID, memberMine).Status(t, http.StatusNotFound)
		h.move(t, h.owner, h.id, byOwner.ID, h.id).WantError(t, http.StatusUnprocessableEntity, "validation", "householdId")
		h.move(t, h.owner, h.id, byOwner.ID, "").WantError(t, http.StatusUnprocessableEntity, "validation", "householdId")
		// From a space it is not in, it does not exist.
		h.move(t, h.owner, ownerMine, byOwner.ID, h.id).Status(t, http.StatusNotFound)
	})

	t.Run("a draft is confirmed first; the recurring series stays behind", func(t *testing.T) {
		h := newHouse(t, s)
		mine := s.PersonalSpace(t, h.owner)
		e := h.cashExpense(t, h.owner, 100, nil).Expense
		if _, err := s.Pool.Exec(t.Context(), `UPDATE expenses SET status = 'draft' WHERE id = $1`, e.ID); err != nil {
			t.Fatal(err)
		}
		h.move(t, h.owner, h.id, e.ID, mine).WantError(t, http.StatusUnprocessableEntity, "validation", "status")

		var series struct {
			ID string `json:"id"`
		}
		h.do(t, h.owner, http.MethodPost, "/recurring-expenses", map[string]any{"paymentMethodId": h.ownerCash, "amount": 500,
			"currency": "ARS", "description": "Gimnasio", "installments": 1, "frequency": "monthly", "dayOfMonth": 5,
			"startsAt": "2026-11-05"}).Status(t, http.StatusCreated).JSON(t, &series)
		r := h.cashExpense(t, h.owner, 500, nil).Expense
		if _, err := s.Pool.Exec(t.Context(), `UPDATE expenses SET recurring_expense_id = $2 WHERE id = $1`, r.ID, series.ID); err != nil {
			t.Fatal(err)
		}
		h.move(t, h.owner, h.id, r.ID, mine).Status(t, http.StatusOK)
		var link *string
		if err := s.Pool.QueryRow(t.Context(), `SELECT recurring_expense_id::text FROM expenses WHERE id = $1`, r.ID).Scan(&link); err != nil {
			t.Fatal(err)
		}
		if link != nil {
			t.Errorf("recurring link = %s, want none", *link)
		}
	})
}
