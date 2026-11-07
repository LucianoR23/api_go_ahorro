package auth_test

import (
	"net/http"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

func verifyEmail(t testing.TB, s *apitest.Server, token string) *apitest.Res {
	t.Helper()
	return s.Do(t, post("/auth/verify-email", map[string]string{"token": token}))
}

func resendVerification(t testing.TB, s *apitest.Server, token string) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/resend-verification-email", Token: token})
}

func TestEmailVerification(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("the emailed token verifies the account, once", func(t *testing.T) {
		u := s.Register(t)
		token := s.Mail.LastTo(t, u.Email).Token(t)

		// Public endpoint: no access token needed.
		verifyEmail(t, s, token).Status(t, http.StatusNoContent)
		if me := getMe(t, s, u.Token); me.EmailVerifiedAt == nil {
			t.Errorf("emailVerifiedAt still empty after verifying")
		}
		wantError(t, verifyEmail(t, s, token), http.StatusUnauthorized, "unauthorized", "")
	})

	t.Run("resend issues a new token and invalidates the old one", func(t *testing.T) {
		u := s.Register(t)
		old := s.Mail.LastTo(t, u.Email).Token(t)

		resendVerification(t, s, u.Token).Status(t, http.StatusNoContent)
		fresh := s.Mail.LastTo(t, u.Email).Token(t)
		if fresh == old {
			t.Fatal("resend sent the same token")
		}
		wantError(t, verifyEmail(t, s, old), http.StatusUnauthorized, "unauthorized", "")
		verifyEmail(t, s, fresh).Status(t, http.StatusNoContent)

		t.Run("resend after verifying is a conflict", func(t *testing.T) {
			wantError(t, resendVerification(t, s, u.Token), http.StatusConflict, "conflict", "")
		})
	})

	t.Run("expired token", func(t *testing.T) {
		u := s.Register(t)
		token := s.Mail.LastTo(t, u.Email).Token(t)
		if _, err := s.Pool.Exec(t.Context(),
			`UPDATE email_verifications SET expires_at = now() - interval '1 second' WHERE user_id = $1`, u.ID); err != nil {
			t.Fatal(err)
		}
		wantError(t, verifyEmail(t, s, token), http.StatusUnauthorized, "unauthorized", "")
		if me := getMe(t, s, u.Token); me.EmailVerifiedAt != nil {
			t.Errorf("expired token verified the account")
		}
	})

	t.Run("unknown and empty tokens", func(t *testing.T) {
		wantError(t, verifyEmail(t, s, "made-up"), http.StatusUnauthorized, "unauthorized", "")
		wantError(t, verifyEmail(t, s, ""), http.StatusUnprocessableEntity, "validation", "token")
	})

	t.Run("delivery failure does not block registration", func(t *testing.T) {
		t.Parallel()
		s := apitest.New(t)
		s.Mail.Fail = errSMTP{}
		u := s.Register(t)
		if me := getMe(t, s, u.Token); me.EmailVerifiedAt != nil {
			t.Errorf("user verified without an email")
		}
	})
}

type errSMTP struct{}

func (errSMTP) Error() string { return "smtp down" }
