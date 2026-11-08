package categories_test

// Characterization tests of household categories (item 0.21d).

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

var iconKey = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

type categoryJSON struct {
	ID          string  `json:"id"`
	HouseholdID string  `json:"householdId"`
	ParentID    *string `json:"parentId"`
	Name        string  `json:"name"`
	Icon        string  `json:"icon"`
	Color       string  `json:"color"`
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

	t.Run("a new household comes with the Argentine categories", func(t *testing.T) {
		got := list(t, owner)
		byName := map[string]categoryJSON{}
		for _, c := range got {
			if c.HouseholdID != h.ID || c.Name == "" || c.Color == "" || !iconKey.MatchString(c.Icon) {
				t.Errorf("default category = %+v", c)
			}
			byName[c.Name] = c
		}
		top, sub := 0, 0
		for _, c := range got {
			if c.ParentID == nil {
				top++
			} else {
				sub++
			}
		}
		if top != 19 || sub != 9 {
			t.Errorf("defaults: %d top-level and %d subcategories, want 19 and 9", top, sub)
		}
		servicios := byName["Servicios"]
		for _, name := range []string{"Luz", "Gas", "Agua", "Internet", "Celular"} {
			c, ok := byName[name]
			if !ok || c.ParentID == nil || *c.ParentID != servicios.ID {
				t.Errorf("%s is not under Servicios: %+v", name, c)
			}
			if c.Color != servicios.Color {
				t.Errorf("%s color = %s, want its parent's %s", name, c.Color, servicios.Color)
			}
		}
	})

	t.Run("any member creates, edits and deletes", func(t *testing.T) {
		var c categoryJSON
		call(t, member, http.MethodPost, "/categories", map[string]string{"name": "  Gatos ", "icon": "paw-print", "color": "#ff0000"}).
			Status(t, http.StatusCreated).JSON(t, &c)
		if c.Name != "Gatos" || c.Icon != "paw-print" || c.Color != "#ff0000" || c.HouseholdID != h.ID {
			t.Errorf("created = %+v", c)
		}

		var upd categoryJSON
		call(t, owner, http.MethodPatch, "/categories/"+c.ID, map[string]string{"name": "Perro"}).Status(t, http.StatusOK).JSON(t, &upd)
		// PATCH replaces every field: icon and color fall back to the defaults.
		if upd.Name != "Perro" || upd.Icon != "tag" || upd.Color != "#2E75B6" {
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
		if c.Icon != "tag" || c.Color != "#2E75B6" {
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

	t.Run("icon is a key", func(t *testing.T) {
		for _, bad := range []string{"🐶", "Shopping-Cart", "shopping cart", "-tag", strings.Repeat("a", 33)} {
			call(t, owner, http.MethodPost, "/categories", map[string]string{"name": "Icono " + bad, "icon": bad}).
				WantError(t, http.StatusUnprocessableEntity, "validation", "icon")
		}
	})

	t.Run("subcategories", func(t *testing.T) {
		var parent, child, other categoryJSON
		call(t, owner, http.MethodPost, "/categories", map[string]string{"name": "Auto", "icon": "car"}).
			Status(t, http.StatusCreated).JSON(t, &parent)
		if parent.ParentID != nil {
			t.Errorf("top-level parentId = %v", *parent.ParentID)
		}
		call(t, member, http.MethodPost, "/categories", map[string]any{"name": "Seguro", "icon": "car", "parentId": parent.ID}).
			Status(t, http.StatusCreated).JSON(t, &child)
		if child.ParentID == nil || *child.ParentID != parent.ID {
			t.Fatalf("child = %+v", child)
		}
		call(t, owner, http.MethodPost, "/categories", map[string]string{"name": "Patente", "icon": "landmark"}).
			Status(t, http.StatusCreated).JSON(t, &other)

		// One level only, and only categories of this household.
		call(t, owner, http.MethodPost, "/categories", map[string]any{"name": "Nieta", "parentId": child.ID}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "parentId")
		call(t, owner, http.MethodPost, "/categories", map[string]any{"name": "Huérfana", "parentId": "00000000-0000-0000-0000-000000000000"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "parentId")
		call(t, owner, http.MethodPatch, "/categories/"+other.ID, map[string]any{"name": "Patente", "parentId": other.ID}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "parentId")
		call(t, owner, http.MethodPatch, "/categories/"+parent.ID, map[string]any{"name": "Auto", "parentId": other.ID}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "parentId")
		stranger := s.Register(t)
		sh := s.CreateHousehold(t, stranger, "")
		var theirs categoryJSON
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/categories", Token: stranger.Token, Household: sh.ID,
			Body: map[string]string{"name": "Ajena"}}).Status(t, http.StatusCreated).JSON(t, &theirs)
		call(t, owner, http.MethodPost, "/categories", map[string]any{"name": "Colgada", "parentId": theirs.ID}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "parentId")

		// PATCH without parentId keeps it; a new parent moves it; null makes it top-level.
		var upd categoryJSON
		call(t, owner, http.MethodPatch, "/categories/"+child.ID, map[string]string{"name": "Seguro del auto", "icon": "car"}).
			Status(t, http.StatusOK).JSON(t, &upd)
		if upd.ParentID == nil || *upd.ParentID != parent.ID {
			t.Errorf("parent lost on a PATCH without parentId: %+v", upd)
		}
		call(t, owner, http.MethodPatch, "/categories/"+child.ID, map[string]any{"name": "Seguro del auto", "parentId": other.ID}).
			Status(t, http.StatusOK).JSON(t, &upd)
		if upd.ParentID == nil || *upd.ParentID != other.ID {
			t.Errorf("parent not moved: %+v", upd)
		}
		call(t, owner, http.MethodPatch, "/categories/"+child.ID, map[string]any{"name": "Seguro del auto", "parentId": nil}).
			Status(t, http.StatusOK).JSON(t, &upd)
		if upd.ParentID != nil {
			t.Errorf("parentId null did not clear it: %+v", upd)
		}

		// Deleting a parent keeps its children as top-level categories.
		call(t, owner, http.MethodPatch, "/categories/"+child.ID, map[string]any{"name": "Seguro del auto", "parentId": parent.ID}).
			Status(t, http.StatusOK)
		call(t, owner, http.MethodDelete, "/categories/"+parent.ID, nil).Status(t, http.StatusNoContent)
		found := false
		for _, c := range list(t, owner) {
			if c.ID == child.ID {
				found = true
				if c.ParentID != nil {
					t.Errorf("child still has a parent: %+v", c)
				}
			}
		}
		if !found {
			t.Error("deleting the parent deleted its child")
		}
	})

	t.Run("requires auth and a household", func(t *testing.T) {
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Household: h.ID}).WantError(t, http.StatusUnauthorized, "unauthorized", "")
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: owner.Token}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "X-Household-ID")
	})
}
