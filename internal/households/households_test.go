package households_test

// Characterization tests of households, members, roles, invites and the
// superadmin household endpoints (item 0.21c), through the real router.

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

type householdJSON struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	BaseCurrency string    `json:"baseCurrency"`
	FxSource     string    `json:"fxSource"`
	Kind         string    `json:"kind"`
	CreatedBy    string    `json:"createdBy"`
	CreatedAt    time.Time `json:"createdAt"`
	DeletedAt    *string   `json:"deletedAt"`
}

type memberJSON struct {
	UserID string `json:"userId"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

func req(method, path, token string, body any) apitest.Req {
	return apitest.Req{Method: method, Path: path, Token: token, Body: body}
}

func members(t testing.TB, s *apitest.Server, u apitest.User, hID string) map[string]string {
	t.Helper()
	var list []memberJSON
	s.Do(t, req(http.MethodGet, "/households/"+hID+"/members", u.Token, nil)).Status(t, http.StatusOK).JSON(t, &list)
	roles := map[string]string{}
	for _, m := range list {
		roles[m.UserID] = m.Role
	}
	return roles
}

func count(t testing.TB, s *apitest.Server, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateAndListHouseholds(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("creator becomes owner, defaults are seeded", func(t *testing.T) {
		u := s.Register(t)
		var h householdJSON
		s.Do(t, req(http.MethodPost, "/households", u.Token, map[string]string{"name": "  Casa "})).
			Status(t, http.StatusCreated).JSON(t, &h)
		if h.Name != "Casa" || h.BaseCurrency != "ARS" || h.CreatedBy != u.ID {
			t.Errorf("household = %+v", h)
		}
		if roles := members(t, s, u, h.ID); len(roles) != 1 || roles[u.ID] != "owner" {
			t.Errorf("members = %v, want only the owner", roles)
		}
		if n := count(t, s, `SELECT count(*) FROM categories WHERE household_id = $1`, h.ID); n == 0 {
			t.Errorf("no default categories seeded")
		}
		if n := count(t, s, `SELECT count(*) FROM household_split_rules WHERE household_id = $1 AND user_id = $2`, h.ID, u.ID); n != 1 {
			t.Errorf("split rule rows for owner = %d, want 1", n)
		}
	})

	t.Run("list shows only the caller's households", func(t *testing.T) {
		a, b := s.Register(t), s.Register(t)
		ha := s.CreateHousehold(t, a, "A1")
		s.CreateHousehold(t, b, "B1")

		var list []householdJSON
		s.Do(t, req(http.MethodGet, "/households", a.Token, nil)).Status(t, http.StatusOK).JSON(t, &list)
		if len(list) != 2 || list[0].Kind != "personal" || list[1].ID != ha.ID {
			t.Errorf("a's households = %+v", list)
		}
	})

	for name, body := range map[string]map[string]string{
		"empty name":           {"name": " ", "baseCurrency": "ARS"},
		"dollars":              {"name": "Casa", "baseCurrency": "USD"},
		"unsupported currency": {"name": "Casa", "baseCurrency": "BRL"},
	} {
		t.Run("validation: "+name, func(t *testing.T) {
			u := s.Register(t)
			field := "name"
			if body["name"] != " " {
				field = "baseCurrency"
			}
			s.Do(t, req(http.MethodPost, "/households", u.Token, body)).WantError(t, http.StatusUnprocessableEntity, "validation", field)
		})
	}

	t.Run("unknown field", func(t *testing.T) {
		u := s.Register(t)
		s.Do(t, req(http.MethodPost, "/households", u.Token, map[string]string{"name": "Casa", "baseCurrency": "ARS", "owner": "x"})).
			WantError(t, http.StatusUnprocessableEntity, "validation", "body")
	})

	t.Run("requires auth", func(t *testing.T) {
		s.Do(t, req(http.MethodGet, "/households", "", nil)).WantError(t, http.StatusUnauthorized, "unauthorized", "")
		s.Do(t, req(http.MethodPost, "/households", "", map[string]string{"name": "x", "baseCurrency": "ARS"})).
			WantError(t, http.StatusUnauthorized, "unauthorized", "")
	})
}

func TestHouseholdAccess(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	owner := s.Register(t)
	h := s.CreateHousehold(t, owner, "Casa")
	member := s.AddMember(t, owner, h.ID)
	outsider := s.Register(t)
	path := "/households/" + h.ID

	t.Run("owner and member can read; outsider gets 404", func(t *testing.T) {
		for _, u := range []apitest.User{owner, member} {
			var got householdJSON
			s.Do(t, req(http.MethodGet, path, u.Token, nil)).Status(t, http.StatusOK).JSON(t, &got)
			if got.ID != h.ID {
				t.Errorf("get = %+v", got)
			}
			if roles := members(t, s, u, h.ID); roles[owner.ID] != "owner" || roles[member.ID] != "member" {
				t.Errorf("members = %v", roles)
			}
		}
		s.Do(t, req(http.MethodGet, path, outsider.Token, nil)).WantError(t, http.StatusNotFound, "not_found", "")
		s.Do(t, req(http.MethodGet, path+"/members", outsider.Token, nil)).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("only the owner can update or delete", func(t *testing.T) {
		body := map[string]string{"name": "Hackeada", "baseCurrency": "ARS"}
		for _, u := range []apitest.User{member, outsider} {
			s.Do(t, req(http.MethodPatch, path, u.Token, body)).WantError(t, http.StatusForbidden, "forbidden", "")
			s.Do(t, req(http.MethodDelete, path, u.Token, nil)).WantError(t, http.StatusForbidden, "forbidden", "")
		}
		var got householdJSON
		s.Do(t, req(http.MethodGet, path, owner.Token, nil)).Status(t, http.StatusOK).JSON(t, &got)
		if got.Name != "Casa" {
			t.Errorf("name changed by a non-owner: %q", got.Name)
		}
	})

	t.Run("owner updates the name; the currency stays pesos (plan 5.5)", func(t *testing.T) {
		var got householdJSON
		s.Do(t, req(http.MethodPatch, path, owner.Token, map[string]string{"name": " Casa nueva "})).
			Status(t, http.StatusOK).JSON(t, &got)
		if got.Name != "Casa nueva" || got.BaseCurrency != "ARS" {
			t.Errorf("update = %+v", got)
		}
		s.Do(t, req(http.MethodPatch, path, owner.Token, map[string]string{"name": "Casa nueva", "baseCurrency": "ars"})).
			Status(t, http.StatusOK)
		s.Do(t, req(http.MethodPatch, path, owner.Token, map[string]string{"name": "Casa nueva", "baseCurrency": "EUR"})).
			WantError(t, http.StatusUnprocessableEntity, "validation", "baseCurrency")
		if _, err := s.Pool.Exec(t.Context(), `UPDATE households SET base_currency = 'USD' WHERE id = $1`, h.ID); err == nil {
			t.Error("the database let a space switch to dollars")
		}
		s.Do(t, req(http.MethodPatch, path, owner.Token, map[string]string{"name": "", "baseCurrency": "ARS"})).
			WantError(t, http.StatusUnprocessableEntity, "validation", "name")
		s.Do(t, req(http.MethodPatch, path, owner.Token, map[string]string{"name": "Casa", "baseCurrency": "XXX"})).
			WantError(t, http.StatusUnprocessableEntity, "validation", "baseCurrency")
	})

	t.Run("the rate source: MEP by default, changed by the owner, kept when not sent (plan 5.1)", func(t *testing.T) {
		var got householdJSON
		s.Do(t, req(http.MethodGet, path, owner.Token, nil)).Status(t, http.StatusOK).JSON(t, &got)
		if got.FxSource != "bolsa" {
			t.Errorf("default fxSource = %q", got.FxSource)
		}
		s.Do(t, req(http.MethodPatch, path, owner.Token, map[string]string{"name": "Casa", "baseCurrency": "ARS", "fxSource": "blue"})).
			Status(t, http.StatusOK).JSON(t, &got)
		if got.FxSource != "blue" {
			t.Errorf("fxSource = %q, want blue", got.FxSource)
		}
		s.Do(t, req(http.MethodPatch, path, owner.Token, map[string]string{"name": "Casa", "baseCurrency": "ARS"})).
			Status(t, http.StatusOK).JSON(t, &got)
		if got.FxSource != "blue" {
			t.Errorf("fxSource not sent = %q, want it kept", got.FxSource)
		}
		s.Do(t, req(http.MethodPatch, path, owner.Token, map[string]string{"name": "Casa", "baseCurrency": "ARS", "fxSource": "euro_blue"})).
			WantError(t, http.StatusUnprocessableEntity, "validation", "fxSource")
	})

	t.Run("bad ids", func(t *testing.T) {
		s.Do(t, req(http.MethodGet, "/households/not-a-uuid", owner.Token, nil)).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
		s.Do(t, req(http.MethodDelete, path+"/members/not-a-uuid", owner.Token, nil)).
			WantError(t, http.StatusUnprocessableEntity, "validation", "userId")
	})

	t.Run("X-Household-ID: members pass, others get 404", func(t *testing.T) {
		// /categories is a household-scoped route; any one exercises the middleware.
		for _, u := range []apitest.User{owner, member} {
			s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: u.Token, Household: h.ID}).Status(t, http.StatusOK)
		}
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: outsider.Token, Household: h.ID}).
			WantError(t, http.StatusNotFound, "not_found", "")
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: owner.Token}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "X-Household-ID")
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: owner.Token, Household: "nope"}).
			WantError(t, http.StatusUnprocessableEntity, "validation", "X-Household-ID")
	})
}

func TestMembersAndOwnership(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	setup := func(t *testing.T) (apitest.User, apitest.Household, apitest.User, apitest.User) {
		owner := s.Register(t)
		h := s.CreateHousehold(t, owner, "")
		return owner, h, s.AddMember(t, owner, h.ID), s.AddMember(t, owner, h.ID)
	}
	memberPath := func(h apitest.Household, u apitest.User) string {
		return "/households/" + h.ID + "/members/" + u.ID
	}

	t.Run("a member can leave", func(t *testing.T) {
		owner, h, m1, _ := setup(t)
		s.Do(t, req(http.MethodDelete, memberPath(h, m1), m1.Token, nil)).Status(t, http.StatusNoContent)
		if _, still := members(t, s, owner, h.ID)[m1.ID]; still {
			t.Errorf("member still listed after leaving")
		}
		s.Do(t, req(http.MethodGet, "/households/"+h.ID, m1.Token, nil)).WantError(t, http.StatusNotFound, "not_found", "")
	})

	t.Run("the owner cannot leave", func(t *testing.T) {
		owner, h, _, _ := setup(t)
		s.Do(t, req(http.MethodDelete, memberPath(h, owner), owner.Token, nil)).WantError(t, http.StatusUnprocessableEntity, "validation", "user")
	})

	t.Run("owner removes a member; members cannot remove each other", func(t *testing.T) {
		owner, h, m1, m2 := setup(t)
		s.Do(t, req(http.MethodDelete, memberPath(h, m2), m1.Token, nil)).WantError(t, http.StatusForbidden, "forbidden", "")
		s.Do(t, req(http.MethodDelete, memberPath(h, owner), m1.Token, nil)).WantError(t, http.StatusForbidden, "forbidden", "")
		s.Do(t, req(http.MethodDelete, memberPath(h, m2), owner.Token, nil)).Status(t, http.StatusNoContent)
		if _, still := members(t, s, owner, h.ID)[m2.ID]; still {
			t.Errorf("removed member still listed")
		}
	})

	t.Run("removing someone who is not a member is a silent 204", func(t *testing.T) {
		owner, h, _, _ := setup(t)
		stranger := s.Register(t)
		s.Do(t, req(http.MethodDelete, memberPath(h, stranger), owner.Token, nil)).Status(t, http.StatusNoContent)
	})

	t.Run("transfer ownership", func(t *testing.T) {
		owner, h, m1, m2 := setup(t)
		role := func(u apitest.User, by apitest.User, r string) *apitest.Res {
			return s.Do(t, req(http.MethodPatch, memberPath(h, u)+"/role", by.Token, map[string]string{"role": r}))
		}
		role(m1, owner, "member").WantError(t, http.StatusUnprocessableEntity, "validation", "role")
		role(owner, owner, "owner").WantError(t, http.StatusUnprocessableEntity, "validation", "userId")
		role(s.Register(t), owner, "owner").WantError(t, http.StatusUnprocessableEntity, "validation", "userId")
		role(m2, m1, "owner").WantError(t, http.StatusForbidden, "forbidden", "")

		role(m1, owner, "owner").Status(t, http.StatusNoContent)
		roles := members(t, s, m1, h.ID)
		if roles[m1.ID] != "owner" || roles[owner.ID] != "member" {
			t.Errorf("after transfer = %v", roles)
		}
		// The old owner lost owner powers; the new one has them.
		s.Do(t, req(http.MethodPatch, "/households/"+h.ID, owner.Token, map[string]string{"name": "x", "baseCurrency": "ARS"})).
			WantError(t, http.StatusForbidden, "forbidden", "")
		s.Do(t, req(http.MethodDelete, memberPath(h, owner), owner.Token, nil)).Status(t, http.StatusNoContent)
	})

	t.Run("deleting a member's account removes the membership", func(t *testing.T) {
		owner, h, m1, _ := setup(t)
		s.Do(t, req(http.MethodDelete, "/me", m1.Token, nil)).Status(t, http.StatusNoContent)
		if _, still := members(t, s, owner, h.ID)[m1.ID]; still {
			t.Errorf("deleted account still listed as member")
		}
	})

	// Plan 0.8: the deletion is one transaction. Before, memberships were
	// removed first, so a failure marking the user deleted left a live
	// account with no households.
	t.Run("a failed account deletion changes nothing", func(t *testing.T) {
		owner, h, m1, _ := setup(t)
		fn := "fail_delete_" + strings.ReplaceAll(m1.ID, "-", "")
		for _, sql := range []string{
			`CREATE FUNCTION ` + fn + `() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'injected failure'; END $$`,
			`CREATE TRIGGER ` + fn + ` BEFORE UPDATE OF deleted_at ON users
			FOR EACH ROW WHEN (NEW.id = '` + m1.ID + `' AND NEW.deleted_at IS NOT NULL) EXECUTE FUNCTION ` + fn + `()`,
		} {
			if _, err := s.Pool.Exec(context.Background(), sql); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			_, _ = s.Pool.Exec(context.Background(), `DROP TRIGGER `+fn+` ON users; DROP FUNCTION `+fn+`()`)
		})

		s.Do(t, req(http.MethodDelete, "/me", m1.Token, nil)).Status(t, http.StatusInternalServerError)
		if _, still := members(t, s, owner, h.ID)[m1.ID]; !still {
			t.Errorf("membership removed by a deletion that failed")
		}
		s.Do(t, req(http.MethodGet, "/me", m1.Token, nil)).Status(t, http.StatusOK)
	})
}

func TestDeleteHouseholdAndAdmin(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	admin := s.Register(t)
	s.MakeSuperadmin(t, admin)

	t.Run("soft delete hides the household; admin restores it", func(t *testing.T) {
		owner := s.Register(t)
		h := s.CreateHousehold(t, owner, "Borrable")
		member := s.AddMember(t, owner, h.ID)
		path := "/households/" + h.ID

		s.Do(t, req(http.MethodDelete, path, owner.Token, nil)).Status(t, http.StatusNoContent)
		for _, u := range []apitest.User{owner, member} {
			s.Do(t, req(http.MethodGet, path, u.Token, nil)).WantError(t, http.StatusNotFound, "not_found", "")
			s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/categories", Token: u.Token, Household: h.ID}).
				WantError(t, http.StatusNotFound, "not_found", "")
		}

		var deleted []struct {
			householdJSON
			Owner struct {
				ID    string `json:"id"`
				Email string `json:"email"`
			} `json:"owner"`
		}
		s.Do(t, req(http.MethodGet, "/admin/households/deleted", admin.Token, nil)).Status(t, http.StatusOK).JSON(t, &deleted)
		found := false
		for _, d := range deleted {
			if d.ID == h.ID {
				found = d.DeletedAt != nil && d.Owner.ID == owner.ID && d.Owner.Email == owner.Email
			}
		}
		if !found {
			t.Errorf("deleted list = %+v, want %s with owner and deletedAt", deleted, h.ID)
		}

		var got householdJSON
		s.Do(t, req(http.MethodGet, "/admin/households/"+h.ID, admin.Token, nil)).Status(t, http.StatusOK).JSON(t, &got)
		if got.DeletedAt == nil {
			t.Errorf("admin get = %+v, want deletedAt", got)
		}

		s.Do(t, req(http.MethodPost, "/admin/households/"+h.ID+"/restore", admin.Token, nil)).Status(t, http.StatusNoContent)
		s.Do(t, req(http.MethodGet, path, member.Token, nil)).Status(t, http.StatusOK)
		s.Do(t, req(http.MethodPost, "/admin/households/"+h.ID+"/restore", admin.Token, nil)).
			WantError(t, http.StatusUnprocessableEntity, "validation", "id")
	})

	t.Run("purge needs a soft delete first, then removes everything", func(t *testing.T) {
		owner := s.Register(t)
		h := s.CreateHousehold(t, owner, "Purgable")
		purge := "/admin/households/" + h.ID + "/purge"

		s.Do(t, req(http.MethodDelete, purge, admin.Token, nil)).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
		s.Do(t, req(http.MethodDelete, "/households/"+h.ID, owner.Token, nil)).Status(t, http.StatusNoContent)
		s.Do(t, req(http.MethodDelete, purge, admin.Token, nil)).Status(t, http.StatusNoContent)

		s.Do(t, req(http.MethodGet, "/admin/households/"+h.ID, admin.Token, nil)).WantError(t, http.StatusNotFound, "not_found", "")
		if n := count(t, s, `SELECT count(*) FROM categories WHERE household_id = $1`, h.ID); n != 0 {
			t.Errorf("categories left after purge: %d", n)
		}
	})

	t.Run("unknown and malformed ids", func(t *testing.T) {
		missing := "00000000-0000-0000-0000-000000000001"
		s.Do(t, req(http.MethodGet, "/admin/households/"+missing, admin.Token, nil)).WantError(t, http.StatusNotFound, "not_found", "")
		s.Do(t, req(http.MethodPost, "/admin/households/"+missing+"/restore", admin.Token, nil)).WantError(t, http.StatusNotFound, "not_found", "")
		s.Do(t, req(http.MethodGet, "/admin/households/nope", admin.Token, nil)).WantError(t, http.StatusUnprocessableEntity, "validation", "id")
	})

	t.Run("admin routes need a superadmin", func(t *testing.T) {
		u := s.Register(t)
		h := s.CreateHousehold(t, u, "")
		for _, r := range []apitest.Req{
			req(http.MethodGet, "/admin/households/deleted", u.Token, nil),
			req(http.MethodGet, "/admin/households/"+h.ID, u.Token, nil),
			req(http.MethodPost, "/admin/households/"+h.ID+"/restore", u.Token, nil),
			req(http.MethodDelete, "/admin/households/"+h.ID+"/purge", u.Token, nil),
		} {
			s.Do(t, r).WantError(t, http.StatusForbidden, "forbidden", "")
			r.Token = ""
			s.Do(t, r).WantError(t, http.StatusUnauthorized, "unauthorized", "")
		}
	})

	t.Run("an owner who deleted their only household can delete the account", func(t *testing.T) {
		owner := s.Register(t)
		h := s.CreateHousehold(t, owner, "")
		s.Do(t, req(http.MethodDelete, "/households/"+h.ID, owner.Token, nil)).Status(t, http.StatusNoContent)

		s.Do(t, req(http.MethodDelete, "/me", owner.Token, nil)).Status(t, http.StatusNoContent)
	})
}
