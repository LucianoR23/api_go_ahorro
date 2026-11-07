package auth_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/auth/google"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

func googleClaims(subject, email string) google.Claims {
	return google.Claims{Subject: subject, Email: email, GivenName: "Gabi", FamilyName: "Gómez"}
}

type googleJSON struct {
	authJSON
	IsNewUser bool `json:"isNewUser"`
}

func googleLogin(t testing.TB, s *apitest.Server, idToken string) *apitest.Res {
	t.Helper()
	return s.Do(t, post("/auth/google", map[string]string{"idToken": idToken}))
}

func TestGoogleLogin(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("new email creates a verified, passwordless user", func(t *testing.T) {
		email := apitest.UniqueEmail()
		s.Google.Add("tok-new", googleClaims("sub-new", strings.ToUpper(email)))

		res := googleLogin(t, s, "tok-new").Status(t, http.StatusOK)
		var g googleJSON
		res.JSON(t, &g)
		if !g.IsNewUser || g.User.Email != email || g.User.EmailVerifiedAt == nil {
			t.Errorf("google new user = %s", res.Body)
		}
		if g.User.FirstName != "Gabi" || g.User.LastName != "Gómez" {
			t.Errorf("names = %q %q", g.User.FirstName, g.User.LastName)
		}
		res.RefreshCookieValue(t)

		var cash int
		if err := s.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM payment_methods WHERE owner_user_id = $1 AND kind = 'cash'`, g.User.ID).Scan(&cash); err != nil {
			t.Fatal(err)
		}
		if cash != 1 {
			t.Errorf("cash payment methods = %d, want the bootstrap Efectivo", cash)
		}
		var personal int
		if err := s.Pool.QueryRow(t.Context(),
			`SELECT count(*) FROM households WHERE created_by = $1 AND kind = 'personal'`, g.User.ID).Scan(&personal); err != nil {
			t.Fatal(err)
		}
		if personal != 1 {
			t.Errorf("Mi plata spaces = %d, want 1 (plan 1.2)", personal)
		}

		t.Run("same subject logs in again", func(t *testing.T) {
			var again googleJSON
			googleLogin(t, s, "tok-new").Status(t, http.StatusOK).JSON(t, &again)
			if again.IsNewUser || again.User.ID != g.User.ID {
				t.Errorf("second login = %+v, want same user, not new", again)
			}
		})
	})

	t.Run("missing given_name falls back to Usuario", func(t *testing.T) {
		s.Google.Add("tok-noname", google.Claims{Subject: "sub-noname", Email: apitest.UniqueEmail()})
		var g googleJSON
		googleLogin(t, s, "tok-noname").Status(t, http.StatusOK).JSON(t, &g)
		if g.User.FirstName != "Usuario" || g.User.LastName != "" {
			t.Errorf("names = %q %q", g.User.FirstName, g.User.LastName)
		}
	})

	t.Run("existing verified account is linked", func(t *testing.T) {
		u := s.Register(t, apitest.RegisterOpts{Verify: true})
		s.Google.Add("tok-link", googleClaims("sub-link", u.Email))

		var g googleJSON
		googleLogin(t, s, "tok-link").Status(t, http.StatusOK).JSON(t, &g)
		if g.IsNewUser || g.User.ID != u.ID {
			t.Errorf("link = %+v, want existing user %s", g, u.ID)
		}
		// The password keeps working after linking.
		s.Login(t, u.Email, u.Password).Status(t, http.StatusOK)
	})

	t.Run("an unverified account is not linked (pre-hijacking); a password reset unlocks it", func(t *testing.T) {
		victim := apitest.UniqueEmail()
		attacker := s.Register(t, apitest.RegisterOpts{Email: victim}) // never verified
		stolen := s.Login(t, victim, attacker.Password).RefreshCookieValue(t)
		s.Google.Add("tok-victim", googleClaims("sub-victim", victim))

		res := googleLogin(t, s, "tok-victim")
		wantError(t, res, http.StatusConflict, "conflict", "")
		if msg := decodeError(t, res).Message; !strings.Contains(msg, "Olvidé mi contraseña") {
			t.Errorf("message = %q", msg)
		}

		// The real owner resets the password from the mailbox: the email is
		// verified, the attacker's session ends, and Google links.
		token := requestReset(t, s, victim)
		resetPassword(t, s, token, newPassword).Status(t, http.StatusNoContent)
		s.Refresh(t, stolen).Status(t, http.StatusUnauthorized)
		var g googleJSON
		googleLogin(t, s, "tok-victim").Status(t, http.StatusOK).JSON(t, &g)
		if g.IsNewUser || g.User.ID != attacker.ID || g.User.EmailVerifiedAt == nil {
			t.Errorf("after the reset Google sign-in = %+v", g)
		}
	})

	t.Run("soft-deleted account behind a linked identity", func(t *testing.T) {
		email := apitest.UniqueEmail()
		s.Google.Add("tok-gone", googleClaims("sub-gone", email))
		var g googleJSON
		googleLogin(t, s, "tok-gone").Status(t, http.StatusOK).JSON(t, &g)
		s.Do(t, apitest.Req{Method: http.MethodDelete, Path: "/me", Token: g.AccessToken}).Status(t, http.StatusNoContent)

		res := googleLogin(t, s, "tok-gone")
		wantError(t, res, http.StatusUnauthorized, "unauthorized", "")
		if e := decodeError(t, res); e.Message != "cuenta no disponible" {
			t.Errorf("message = %q", e.Message)
		}
	})

	t.Run("invalid token", func(t *testing.T) {
		wantError(t, googleLogin(t, s, "forged"), http.StatusUnauthorized, "unauthorized", "")
	})

	t.Run("empty token", func(t *testing.T) {
		wantError(t, googleLogin(t, s, "   "), http.StatusUnprocessableEntity, "validation", "idToken")
	})
}
