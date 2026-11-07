package households_test

import (
	"net/http"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

// Plan 1.2: "Mi plata" (personal, only its owner) + at most one "Hogar"
// (shared) per user.
func TestSpaces(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	// personalSpace returns u's "Mi plata" (created on register).
	personalSpace := func(t *testing.T, u apitest.User) string {
		t.Helper()
		var id string
		if err := s.Pool.QueryRow(t.Context(),
			`SELECT id::text FROM households WHERE created_by = $1 AND kind = 'personal'`, u.ID).Scan(&id); err != nil {
			t.Fatalf("personal space: %v", err)
		}
		return id
	}

	list := func(t *testing.T, u apitest.User) []householdJSON {
		t.Helper()
		var out []householdJSON
		s.Do(t, req(http.MethodGet, "/households", u.Token, nil)).Status(t, http.StatusOK).JSON(t, &out)
		return out
	}

	invite := func(t *testing.T, owner apitest.User, hID, email string) string {
		t.Helper()
		var inv apitest.Invite
		s.Do(t, req(http.MethodPost, "/households/"+hID+"/invites", owner.Token, map[string]string{"email": email})).
			Status(t, http.StatusCreated).JSON(t, &inv)
		return inv.Token
	}

	t.Run("a hogar is shared", func(t *testing.T) {
		u := s.Register(t)
		var h householdJSON
		s.Do(t, req(http.MethodPost, "/households", u.Token, map[string]string{"name": "Casa", "baseCurrency": "ARS"})).
			Status(t, http.StatusCreated).JSON(t, &h)
		if h.Kind != "shared" {
			t.Errorf("kind = %q, want shared", h.Kind)
		}
	})

	t.Run("a second hogar is refused, naming the current one", func(t *testing.T) {
		u := s.Register(t)
		s.CreateHousehold(t, u, "Casa")
		e := s.Do(t, req(http.MethodPost, "/households", u.Token, map[string]string{"name": "Otra", "baseCurrency": "ARS"})).
			WantError(t, http.StatusConflict, "conflict", "")
		if e.Message != "Ya sos parte del hogar Casa. Para crear otro, primero salí de Casa." {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("accepting an invite while in another hogar is refused", func(t *testing.T) {
		owner, guest := s.Register(t), s.Register(t)
		h := s.CreateHousehold(t, owner, "Lo de Ana")
		s.CreateHousehold(t, guest, "Mi depto")
		token := invite(t, owner, h.ID, guest.Email)

		e := accept(t, s, guest, token).WantError(t, http.StatusConflict, "conflict", "")
		if e.Message != "Ya sos parte del hogar Mi depto. Para unirte a Lo de Ana, primero salí de Mi depto." {
			t.Errorf("message = %q", e.Message)
		}
		if status(t, s, token) != "pending" {
			t.Errorf("the refused invite must stay pending")
		}
	})

	t.Run("after leaving, the user can join another hogar", func(t *testing.T) {
		a, b, guest := s.Register(t), s.Register(t), s.Register(t)
		ha := s.CreateHousehold(t, a, "A")
		hb := s.CreateHousehold(t, b, "B")
		accept(t, s, guest, invite(t, a, ha.ID, guest.Email)).Status(t, http.StatusOK)
		s.Do(t, req(http.MethodDelete, "/households/"+ha.ID+"/members/"+guest.ID, guest.Token, nil)).Status(t, http.StatusNoContent)
		accept(t, s, guest, invite(t, b, hb.ID, guest.Email)).Status(t, http.StatusOK)
	})

	t.Run("a deleted hogar does not count", func(t *testing.T) {
		u := s.Register(t)
		h := s.CreateHousehold(t, u, "Vieja")
		s.Do(t, req(http.MethodDelete, "/households/"+h.ID, u.Token, nil)).Status(t, http.StatusNoContent)
		s.CreateHousehold(t, u, "Nueva")
	})

	t.Run("Mi plata cannot be deleted or shared", func(t *testing.T) {
		u := s.Register(t)
		id := personalSpace(t, u)
		s.Do(t, req(http.MethodDelete, "/households/"+id, u.Token, nil)).WantError(t, http.StatusUnprocessableEntity, "validation", "household")
		s.Do(t, req(http.MethodPost, "/households/"+id+"/invites", u.Token, map[string]string{"email": apitest.UniqueEmail()})).
			WantError(t, http.StatusUnprocessableEntity, "validation", "household")
	})

	t.Run("Mi plata does not block a hogar", func(t *testing.T) {
		u := s.Register(t)
		personalSpace(t, u)
		s.CreateHousehold(t, u, "Casa")
	})

	t.Run("admin restore refuses when a member joined another hogar", func(t *testing.T) {
		admin := s.Register(t)
		s.MakeSuperadmin(t, admin)
		owner, member, other := s.Register(t), s.Register(t), s.Register(t)
		old := s.CreateHousehold(t, owner, "Vieja")
		accept(t, s, member, invite(t, owner, old.ID, member.Email)).Status(t, http.StatusOK)
		s.Do(t, req(http.MethodDelete, "/households/"+old.ID, owner.Token, nil)).Status(t, http.StatusNoContent)

		h := s.CreateHousehold(t, other, "Nueva")
		accept(t, s, member, invite(t, other, h.ID, member.Email)).Status(t, http.StatusOK)

		e := s.Do(t, req(http.MethodPost, "/admin/households/"+old.ID+"/restore", admin.Token, nil)).
			WantError(t, http.StatusConflict, "conflict", "")
		if e.Message != "No se puede restaurar: un miembro ya está en otro hogar." {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("register creates Mi plata, listed first", func(t *testing.T) {
		u := s.Register(t)
		h := s.CreateHousehold(t, u, "Casa")
		got := list(t, u)
		if len(got) != 2 || got[0].Kind != "personal" || got[0].Name != "Mi plata" || got[0].BaseCurrency != "ARS" || got[1].ID != h.ID {
			t.Fatalf("households = %+v", got)
		}
		if roles := members(t, s, u, got[0].ID); len(roles) != 1 || roles[u.ID] != "owner" {
			t.Errorf("Mi plata members = %v", roles)
		}
		if n := count(t, s, `SELECT count(*) FROM categories WHERE household_id = $1`, got[0].ID); n == 0 {
			t.Errorf("Mi plata has no default categories")
		}
	})

	t.Run("accounts from before 1.2 get Mi plata on first list, once", func(t *testing.T) {
		u := s.Register(t)
		if _, err := s.Pool.Exec(t.Context(), `DELETE FROM households WHERE created_by = $1 AND kind = 'personal'`, u.ID); err != nil {
			t.Fatal(err)
		}
		done := make(chan []householdJSON, 2)
		for range 2 {
			go func() {
				var out []householdJSON
				s.Do(t, req(http.MethodGet, "/households", u.Token, nil)).Status(t, http.StatusOK).JSON(t, &out)
				done <- out
			}()
		}
		for range 2 {
			if got := <-done; len(got) != 1 || got[0].Kind != "personal" {
				t.Errorf("households = %+v", got)
			}
		}
		if n := count(t, s, `SELECT count(*) FROM households WHERE created_by = $1 AND kind = 'personal'`, u.ID); n != 1 {
			t.Errorf("personal spaces = %d, want 1", n)
		}
	})
}
