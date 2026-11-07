package categories_test

// Characterization tests of household categories (item 0.21d).

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type categoryJSON struct {
	ID          string `json:"id"`
	HouseholdID string `json:"householdId"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Color       string `json:"color"`
}

func TestCategories(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	owner := s.Register(t)
	h := s.CreateHousehold(t, owner, "")
	member := s.AddMember(t, owner, h.ID)

	call := func(t testing.TB, u apitest.User, method, path string, body any) *apitest.Res {
		t.Helper()
		return s.Do(t, apitest.Req{Method: method, Path: path, Token: u.Token, Household: h.ID, Body: body})
	}
	list := func(t testing.TB, u apitest.User) []categoryJSON {
		t.Helper()
		var out []categoryJSON
		call(t, u, http.MethodGet, "/categories", nil).Status(t, http.StatusOK).JSON(t, &out)
		return out
	}

	t.Run("a new household comes with default categories", func(t *testing.T) {
		got := list(t, owner)
		if len(got) == 0 {
			t.Fatal("no default categories")
		}
		for _, c := range got {
			if c.HouseholdID != h.ID || c.Name == "" || c.Icon == "" || c.Color == "" {
				t.Errorf("default category = %+v", c)
			}
		}
	})

	t.Run("any member creates, edits and deletes", func(t *testing.T) {
		var c categoryJSON
		call(t, member, http.MethodPost, "/categories", map[string]string{"name": "  Mascotas ", "icon": "🐶", "color": "#ff0000"}).
			Status(t, http.StatusCreated).JSON(t, &c)
		if c.Name != "Mascotas" || c.Icon != "🐶" || c.Color != "#ff0000" || c.HouseholdID != h.ID {
			t.Errorf("created = %+v", c)
		}

		var upd categoryJSON
		call(t, owner, http.MethodPatch, "/categories/"+c.ID, map[string]string{"name": "Perro"}).Status(t, http.StatusOK).JSON(t, &upd)
		// PATCH replaces every field: icon and color fall back to the defaults.
		if upd.Name != "Perro" || upd.Icon != "💰" || upd.Color != "#2E75B6" {
			t.Errorf("updated = %+v", upd)
		}

		call(t, member, http.MethodDelete, "/categories/"+c.ID, nil).Status(t, http.StatusNoContent)
		for _, got := range list(t, owner) {
			if got.ID == c.ID {
				t.Errorf("deleted category still listed")
			}
		}
		call(t, owner, http.MethodDelete, "/categories/"+c.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("icon and color default when empty", func(t *testing.T) {
		var c categoryJSON
		call(t, owner, http.MethodPost, "/categories", map[string]string{"name": "Sin icono"}).Status(t, http.StatusCreated).JSON(t, &c)
		if c.Icon != "💰" || c.Color != "#2E75B6" {
			t.Errorf("defaults = %+v", c)
		}
	})

	t.Run("name rules", func(t *testing.T) {
		call(t, owner, http.MethodPost, "/categories", map[string]string{"name": " "}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "name")
		call(t, owner, http.MethodPost, "/categories", map[string]string{"name": strings.Repeat("ñ", 41)}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "name")
		// 40 multi-byte runes are fine: the limit counts characters, not bytes.
		call(t, owner, http.MethodPost, "/categories", map[string]string{"name": strings.Repeat("ñ", 40)}).Status(t, http.StatusCreated)

		call(t, owner, http.MethodPost, "/categories", map[string]string{"name": "Duplicada"}).Status(t, http.StatusCreated)
		call(t, owner, http.MethodPost, "/categories", map[string]string{"name": "Duplicada"}).WantError(t, http.StatusConflict, "conflict", "")
		call(t, owner, http.MethodPatch, "/categories/nope", map[string]string{"name": "x"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "id")
		call(t, owner, http.MethodPost, "/categories", map[string]string{"name": "x", "budget": "1"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "body")
	})

	t.Run("categories of another household are invisible", func(t *testing.T) {
		other := s.Register(t)
		oh := s.CreateHousehold(t, other, "")
		var theirs categoryJSON
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/categories", Token: other.Token, Household: oh.ID,
			Body: map[string]string{"name": "Privada"}}).Status(t, http.StatusCreated).JSON(t, &theirs)

		// From my household, their category id does not exist.
		call(t, owner, http.MethodPatch, "/categories/"+theirs.ID, map[string]string{"name": "Mía"}).WantError(t, http.StatusNotFound, "not_found", "")
		call(t, owner, http.MethodDelete, "/categories/"+theirs.ID, nil).WantError(t, http.StatusNotFound, "not_found", "")
		// And I cannot switch to their household.
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: owner.Token, Household: oh.ID}).
			WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("requires auth and a household", func(t *testing.T) {
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Household: h.ID}).WantError(t, http.StatusUnauthorized, "unauthorized", "")
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: owner.Token}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "X-Household-ID")
	})
}
