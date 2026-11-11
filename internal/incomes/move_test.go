package incomes_test

// Moving an income between Mi plata and a hogar (plan 9.1).

import (
	"net/http"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

func TestMoveIncome(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	mine := s.PersonalSpace(t, h.owner)
	move := func(u apitest.User, from, id string, body map[string]any) *apitest.Res {
		return s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/incomes/" + id + "/move", Token: u.Token, Household: from, Body: body})
	}

	t.Run("a salary loaded in the hogar goes to Mi plata and back", func(t *testing.T) {
		inc := h.create(t, h.owner, nil)
		var moved incomeJSON
		move(h.owner, h.id, inc.ID, map[string]any{"householdId": mine}).Status(t, http.StatusOK).JSON(t, &moved)
		if moved.ID != inc.ID || moved.HouseholdID != mine || moved.Amount != inc.Amount {
			t.Errorf("moved = %+v", moved)
		}
		h.do(t, h.member, http.MethodGet, "/incomes/"+inc.ID, nil).Status(t, http.StatusNotFound)

		move(h.owner, mine, inc.ID, map[string]any{"householdId": h.id}).Status(t, http.StatusOK).JSON(t, &moved)
		if moved.HouseholdID != h.id {
			t.Errorf("back = %+v", moved)
		}
	})

	t.Run("only the member who received it moves it, to a space of theirs", func(t *testing.T) {
		inc := h.create(t, h.member, nil)
		move(h.owner, h.id, inc.ID, map[string]any{"householdId": mine}).WantError(t, http.StatusForbidden, "forbidden", "")
		move(h.member, h.id, inc.ID, map[string]any{"householdId": mine}).Status(t, http.StatusNotFound)
		move(h.member, h.id, inc.ID, map[string]any{"householdId": h.id}).WantError(t, http.StatusUnprocessableEntity, "validation", "householdId")
		move(h.member, h.id, inc.ID, map[string]any{}).WantError(t, http.StatusUnprocessableEntity, "validation", "householdId")
		move(h.member, s.PersonalSpace(t, h.member), inc.ID, map[string]any{"householdId": h.id}).Status(t, http.StatusNotFound)
	})

	t.Run("the recurring series stays behind", func(t *testing.T) {
		var series recurringJSON
		h.do(t, h.owner, http.MethodPost, "/recurring-incomes", map[string]any{"amount": 1500, "currency": "ARS",
			"description": "Sueldo", "source": "salary", "frequency": "monthly", "dayOfMonth": 1, "startsAt": "2026-11-01"}).
			Status(t, http.StatusCreated).JSON(t, &series)
		inc := h.create(t, h.owner, nil)
		if _, err := s.Pool.Exec(t.Context(), `UPDATE incomes SET recurring_income_id = $2 WHERE id = $1`, inc.ID, series.ID); err != nil {
			t.Fatal(err)
		}
		var moved incomeJSON
		move(h.owner, h.id, inc.ID, map[string]any{"householdId": mine}).Status(t, http.StatusOK).JSON(t, &moved)
		if moved.RecurringIncomeID != nil {
			t.Errorf("recurring link = %s, want none", *moved.RecurringIncomeID)
		}
	})
}
