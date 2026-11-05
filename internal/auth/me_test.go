package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

func patchMe(t testing.TB, s *apitest.Server, token string, body any) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodPatch, Path: "/me", Token: token, Body: body})
}

func deleteMe(t testing.TB, s *apitest.Server, token string) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/me", Token: token})
}

func TestMe(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("returns the caller only", func(t *testing.T) {
		a, b := s.Register(t), s.Register(t)
		if got := getMe(t, s, a.Token); got.ID != a.ID || got.Email != a.Email || got.FirstName != a.FirstName {
			t.Errorf("/me for a = %+v", got)
		}
		if got := getMe(t, s, b.Token); got.ID != b.ID {
			t.Errorf("/me for b = %+v", got)
		}
	})
}

func TestUpdateMe(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("partial update trims and keeps the rest", func(t *testing.T) {
		u := s.Register(t, apitest.RegisterOpts{LastName: "Viejo"})
		var got userJSON
		patchMe(t, s, u.Token, map[string]string{"firstName": "  Nueva "}).Status(t, http.StatusOK).JSON(t, &got)
		if got.FirstName != "Nueva" || got.LastName != "Viejo" || got.Email != u.Email {
			t.Errorf("after patch = %+v", got)
		}
		if me := getMe(t, s, u.Token); me.FirstName != "Nueva" {
			t.Errorf("not persisted: %+v", me)
		}
	})

	t.Run("lastName can be cleared", func(t *testing.T) {
		u := s.Register(t)
		var got userJSON
		patchMe(t, s, u.Token, map[string]string{"lastName": ""}).Status(t, http.StatusOK).JSON(t, &got)
		if got.LastName != "" {
			t.Errorf("lastName = %q", got.LastName)
		}
	})

	t.Run("empty patch is a no-op", func(t *testing.T) {
		u := s.Register(t)
		var got userJSON
		patchMe(t, s, u.Token, map[string]string{}).Status(t, http.StatusOK).JSON(t, &got)
		if got.ID != u.ID || got.FirstName != u.FirstName {
			t.Errorf("no-op patch = %+v", got)
		}
	})

	t.Run("email change is normalized and logs in with the new address", func(t *testing.T) {
		u := s.Register(t)
		next := apitest.UniqueEmail()
		var got userJSON
		patchMe(t, s, u.Token, map[string]string{"email": " " + strings.ToUpper(next)}).Status(t, http.StatusOK).JSON(t, &got)
		if got.Email != next {
			t.Errorf("email = %q, want %q", got.Email, next)
		}
		s.Login(t, next, u.Password).Status(t, http.StatusOK)
	})

	t.Run("email change keeps the verified flag", func(t *testing.T) {
		u := s.Register(t, apitest.RegisterOpts{Verify: true})
		var got userJSON
		patchMe(t, s, u.Token, map[string]string{"email": apitest.UniqueEmail()}).Status(t, http.StatusOK).JSON(t, &got)
		testutil.KnownBug(t, "0.5", "changing the email skips re-verification", func(t testing.TB) {
			if got.Email != u.Email && got.EmailVerifiedAt != nil {
				t.Errorf("new address %s is marked verified without a token", got.Email)
			}
		})
	})

	t.Run("email taken by another user is a conflict", func(t *testing.T) {
		a, b := s.Register(t), s.Register(t)
		wantError(t, patchMe(t, s, a.Token, map[string]string{"email": strings.ToUpper(b.Email)}), http.StatusConflict, "conflict", "")
	})

	cases := []struct {
		name  string
		body  map[string]string
		field string
	}{
		{"empty firstName", map[string]string{"firstName": " "}, "firstName"},
		{"firstName too long", map[string]string{"firstName": strings.Repeat("a", 101)}, "firstName"},
		{"lastName too long", map[string]string{"lastName": strings.Repeat("a", 101)}, "lastName"},
		{"malformed email", map[string]string{"email": "nope"}, "email"},
		{"empty email", map[string]string{"email": ""}, "email"},
		{"unknown field", map[string]string{"password": "Sneaky-pass-1"}, "body"},
	}
	for _, tc := range cases {
		t.Run("validation: "+tc.name, func(t *testing.T) {
			u := s.Register(t)
			wantError(t, patchMe(t, s, u.Token, tc.body), http.StatusUnprocessableEntity, "validation", tc.field)
			if me := getMe(t, s, u.Token); me.FirstName != u.FirstName || me.Email != u.Email {
				t.Errorf("rejected patch changed the profile: %+v", me)
			}
		})
	}
}

func TestDeleteMe(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("soft-deletes the account and frees the email", func(t *testing.T) {
		u := s.Register(t)
		session := s.Login(t, u.Email, u.Password).RefreshCookieValue(t)

		res := deleteMe(t, s, u.Token).Status(t, http.StatusNoContent)
		if c := res.Cookie(apitest.RefreshCookie); c == nil || c.MaxAge >= 0 {
			t.Errorf("delete did not clear the refresh cookie: %+v", c)
		}

		res = s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/me", Token: u.Token})
		wantError(t, res, http.StatusUnauthorized, "unauthorized", "")
		wantError(t, s.Login(t, u.Email, u.Password), http.StatusUnauthorized, "unauthorized", "")

		var email string
		var deleted bool
		if err := s.Pool.QueryRow(t.Context(),
			`SELECT email, deleted_at IS NOT NULL FROM users WHERE id = $1`, u.ID).Scan(&email, &deleted); err != nil {
			t.Fatal(err)
		}
		if !deleted || email != "deleted+"+u.ID+"@ahorra.deleted" {
			t.Errorf("row after delete: email %q, deleted %v", email, deleted)
		}

		again := s.Register(t, apitest.RegisterOpts{Email: u.Email})
		if again.ID == u.ID {
			t.Errorf("re-registration reused the deleted id")
		}

		testutil.KnownBug(t, "0.5", "refresh does not check that the user still exists", func(t testing.TB) {
			s.Refresh(t, session).Status(t, http.StatusUnauthorized)
		})
	})

	t.Run("household owner must transfer first", func(t *testing.T) {
		u := s.Register(t, apitest.RegisterOpts{Verify: true})
		s.CreateHousehold(t, u, "")
		wantError(t, deleteMe(t, s, u.Token), http.StatusConflict, "conflict", "")
		getMe(t, s, u.Token) // still there
	})
}
