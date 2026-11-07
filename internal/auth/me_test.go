package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
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

	t.Run("an email change waits for the link sent to the new address", func(t *testing.T) {
		u := s.Register(t, apitest.RegisterOpts{Verify: true})
		next := apitest.UniqueEmail()
		var got userJSON
		patchMe(t, s, u.Token, map[string]string{"email": " " + strings.ToUpper(next)}).Status(t, http.StatusOK).JSON(t, &got)
		// Fixed in 0.5: the email used to change at once, still marked verified.
		if got.Email != u.Email || got.PendingEmail == nil || *got.PendingEmail != next || got.EmailVerifiedAt == nil {
			t.Fatalf("after PATCH = %+v, want the old email kept and %s pending", got, next)
		}
		wantError(t, s.Login(t, next, u.Password), http.StatusUnauthorized, "unauthorized", "")

		mail := s.Mail.LastTo(t, next)
		if !strings.Contains(mail.Subject, "Confirmá tu nuevo email") {
			t.Errorf("mail to the new address: %q", mail.Subject)
		}
		verifyEmail(t, s, mail.Token(t)).Status(t, http.StatusNoContent)

		me := getMe(t, s, u.Token)
		if me.Email != next || me.PendingEmail != nil || me.EmailVerifiedAt == nil {
			t.Errorf("after confirming = %+v", me)
		}
		s.Login(t, next, u.Password).Status(t, http.StatusOK)
		wantError(t, s.Login(t, u.Email, u.Password), http.StatusUnauthorized, "unauthorized", "")
	})

	t.Run("an address taken while the change was pending is a conflict at confirmation", func(t *testing.T) {
		u := s.Register(t)
		next := apitest.UniqueEmail()
		patchMe(t, s, u.Token, map[string]string{"email": next}).Status(t, http.StatusOK)
		token := s.Mail.LastTo(t, next).Token(t)
		s.Register(t, apitest.RegisterOpts{Email: next}) // someone else takes it
		wantError(t, verifyEmail(t, s, token), http.StatusConflict, "conflict", "")
		if me := getMe(t, s, u.Token); me.Email != u.Email {
			t.Errorf("email changed to a taken address: %s", me.Email)
		}
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
		if !deleted || email != "deleted+"+u.ID+"@deleted.invalid" {
			t.Errorf("row after delete: email %q, deleted %v", email, deleted)
		}
		var personalDeleted bool
		if err := s.Pool.QueryRow(t.Context(),
			`SELECT deleted_at IS NOT NULL FROM households WHERE created_by = $1 AND kind = 'personal'`, u.ID).Scan(&personalDeleted); err != nil {
			t.Fatal(err)
		}
		if !personalDeleted {
			t.Errorf("Mi plata must be soft-deleted with the account")
		}

		again := s.Register(t, apitest.RegisterOpts{Email: u.Email})
		if again.ID == u.ID {
			t.Errorf("re-registration reused the deleted id")
		}

		s.Refresh(t, session).Status(t, http.StatusUnauthorized)
	})

	t.Run("household owner must transfer first", func(t *testing.T) {
		u := s.Register(t, apitest.RegisterOpts{Verify: true})
		s.CreateHousehold(t, u, "")
		wantError(t, deleteMe(t, s, u.Token), http.StatusConflict, "conflict", "")
		getMe(t, s, u.Token) // still there
	})
}
