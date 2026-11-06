package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

const newPassword = "Brand-new-pass-9"

// requestReset asks for a reset email and returns the token in its link.
func requestReset(t testing.TB, s *apitest.Server, email string) string {
	t.Helper()
	s.Do(t, post("/auth/forgot-password", map[string]string{"email": email})).Status(t, http.StatusNoContent)
	return s.Mail.LastTo(t, email).Token(t)
}

func resetPassword(t testing.TB, s *apitest.Server, token, password string) *apitest.Res {
	t.Helper()
	return s.Do(t, post("/auth/reset-password", map[string]string{"token": token, "newPassword": password}))
}

func changePassword(t testing.TB, s *apitest.Server, token, current, next string) *apitest.Res {
	t.Helper()
	return s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/change-password", Token: token,
		Body: map[string]string{"currentPassword": current, "newPassword": next}})
}

func TestForgotAndResetPassword(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("reset link sets a new password", func(t *testing.T) {
		u := s.Register(t)
		session := s.Login(t, u.Email, u.Password).RefreshCookieValue(t)

		s.Do(t, post("/auth/forgot-password", map[string]string{"email": "  " + strings.ToUpper(u.Email)})).
			Status(t, http.StatusNoContent)
		mail := s.Mail.LastTo(t, u.Email)
		if mail.Subject != "Restablecer tu contraseña de TestBrand" || !strings.Contains(mail.HTML, "tu cuenta de TestBrand") {
			t.Errorf("reset mail brand: subject %q", mail.Subject)
		}
		if mail.From != s.Config.PasswordResetFromEmail || !strings.Contains(mail.HTML, s.Config.AppBaseURL+"/reset-password?token=") {
			t.Errorf("reset mail = from %q, html %q", mail.From, mail.HTML)
		}

		res := resetPassword(t, s, mail.Token(t), newPassword).Status(t, http.StatusNoContent)
		if c := res.Cookie(apitest.RefreshCookie); c == nil || c.MaxAge >= 0 {
			t.Errorf("reset did not clear the caller's refresh cookie: %+v", c)
		}
		s.Login(t, u.Email, newPassword).Status(t, http.StatusOK)
		wantError(t, s.Login(t, u.Email, u.Password), http.StatusUnauthorized, "unauthorized", "")

		t.Run("token is single-use", func(t *testing.T) {
			wantError(t, resetPassword(t, s, mail.Token(t), "Another-pass-77"), http.StatusUnauthorized, "unauthorized", "")
		})

		s.Refresh(t, session).Status(t, http.StatusUnauthorized)
	})

	t.Run("unknown email answers 204 and sends nothing", func(t *testing.T) {
		email := apitest.UniqueEmail()
		s.Do(t, post("/auth/forgot-password", map[string]string{"email": email})).Status(t, http.StatusNoContent)
		for _, m := range s.Mail.Sent() {
			if m.To[0] == email {
				t.Errorf("mail sent to an unknown address")
			}
		}
	})

	t.Run("empty email", func(t *testing.T) {
		wantError(t, s.Do(t, post("/auth/forgot-password", map[string]string{"email": " "})),
			http.StatusUnprocessableEntity, "validation", "email")
	})

	t.Run("a new request invalidates the previous link", func(t *testing.T) {
		u := s.Register(t)
		first := requestReset(t, s, u.Email)
		second := requestReset(t, s, u.Email)
		wantError(t, resetPassword(t, s, first, newPassword), http.StatusUnauthorized, "unauthorized", "")
		resetPassword(t, s, second, newPassword).Status(t, http.StatusNoContent)
	})

	t.Run("weak new password is rejected without burning the token", func(t *testing.T) {
		u := s.Register(t)
		token := requestReset(t, s, u.Email)
		wantError(t, resetPassword(t, s, token, "short"), http.StatusUnprocessableEntity, "validation", "password")
		resetPassword(t, s, token, newPassword).Status(t, http.StatusNoContent)
	})

	t.Run("expired token", func(t *testing.T) {
		u := s.Register(t)
		token := requestReset(t, s, u.Email)
		if _, err := s.Pool.Exec(t.Context(),
			`UPDATE password_resets SET expires_at = now() - interval '1 second' WHERE user_id = $1`, u.ID); err != nil {
			t.Fatal(err)
		}
		wantError(t, resetPassword(t, s, token, newPassword), http.StatusUnauthorized, "unauthorized", "")
	})

	t.Run("unknown and empty tokens", func(t *testing.T) {
		wantError(t, resetPassword(t, s, "made-up-token", newPassword), http.StatusUnauthorized, "unauthorized", "")
		wantError(t, resetPassword(t, s, " ", newPassword), http.StatusUnprocessableEntity, "validation", "token")
	})

	t.Run("a Google-only account can set a password by reset", func(t *testing.T) {
		email := apitest.UniqueEmail()
		s.Google.Add("tok-reset", googleClaims("sub-reset", email))
		googleLogin(t, s, "tok-reset").Status(t, http.StatusOK)

		resetPassword(t, s, requestReset(t, s, email), newPassword).Status(t, http.StatusNoContent)
		s.Login(t, email, newPassword).Status(t, http.StatusOK)
	})
}

func TestChangePassword(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("changes the password with the current one", func(t *testing.T) {
		u := s.Register(t)
		session := s.Login(t, u.Email, u.Password).RefreshCookieValue(t)
		pendingReset := requestReset(t, s, u.Email)

		changePassword(t, s, u.Token, u.Password, newPassword).Status(t, http.StatusNoContent)
		s.Login(t, u.Email, newPassword).Status(t, http.StatusOK)
		wantError(t, s.Login(t, u.Email, u.Password), http.StatusUnauthorized, "unauthorized", "")

		t.Run("pending reset links stop working", func(t *testing.T) {
			wantError(t, resetPassword(t, s, pendingReset, "Another-pass-77"), http.StatusUnauthorized, "unauthorized", "")
		})

		s.Refresh(t, session).Status(t, http.StatusUnauthorized)
	})

	t.Run("the device that changes the password keeps its session", func(t *testing.T) {
		u := s.Register(t)
		res := s.Login(t, u.Email, u.Password)
		var login struct {
			AccessToken string `json:"accessToken"`
		}
		res.JSON(t, &login)
		here := res.RefreshCookieValue(t)
		elsewhere := s.Login(t, u.Email, u.Password).RefreshCookieValue(t)

		changePassword(t, s, login.AccessToken, u.Password, newPassword).Status(t, http.StatusNoContent)
		s.Refresh(t, here).Status(t, http.StatusOK)
		s.Refresh(t, elsewhere).Status(t, http.StatusUnauthorized)
	})

	t.Run("wrong current password", func(t *testing.T) {
		u := s.Register(t)
		res := changePassword(t, s, u.Token, "Not-my-pass-1", newPassword)
		wantError(t, res, http.StatusUnauthorized, "unauthorized", "")
		if e := decodeError(t, res); e.Message != "contraseña actual incorrecta" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("validation", func(t *testing.T) {
		u := s.Register(t)
		wantError(t, changePassword(t, s, u.Token, " ", newPassword), http.StatusUnprocessableEntity, "validation", "currentPassword")
		wantError(t, changePassword(t, s, u.Token, u.Password, "weak"), http.StatusUnprocessableEntity, "validation", "password")
		wantError(t, changePassword(t, s, u.Token, u.Password, u.Password), http.StatusUnprocessableEntity, "validation", "newPassword")
		// Nothing changed.
		s.Login(t, u.Email, u.Password).Status(t, http.StatusOK)
	})

	t.Run("Google-only account has no current password to give", func(t *testing.T) {
		email := apitest.UniqueEmail()
		s.Google.Add("tok-change", googleClaims("sub-change", email))
		var g googleJSON
		googleLogin(t, s, "tok-change").Status(t, http.StatusOK).JSON(t, &g)
		wantError(t, changePassword(t, s, g.AccessToken, "Anything-1", newPassword), http.StatusUnauthorized, "unauthorized", "")
	})
}
