package auth_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

func TestLogin(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	u := s.Register(t)

	t.Run("valid credentials open a session", func(t *testing.T) {
		res := s.Login(t, "  "+strings.ToUpper(u.Email)+" ", u.Password).Status(t, http.StatusOK)
		var a authJSON
		res.JSON(t, &a)
		if a.User.ID != u.ID || a.AccessToken == "" {
			t.Fatalf("login = %s", res.Body)
		}
		res.RefreshCookieValue(t)
		if got := getMe(t, s, a.AccessToken); got.Email != u.Email {
			t.Errorf("/me email = %q", got.Email)
		}
	})

	t.Run("wrong password and unknown email give the same answer", func(t *testing.T) {
		wrong := s.Login(t, u.Email, "Wrong-password-1")
		unknown := s.Login(t, apitest.UniqueEmail(), u.Password)
		wantError(t, wrong, http.StatusUnauthorized, "unauthorized", "")
		wantError(t, unknown, http.StatusUnauthorized, "unauthorized", "")
		if !bytes.Equal(wrong.Body, unknown.Body) {
			t.Errorf("bodies differ (account enumeration): %s vs %s", wrong.Body, unknown.Body)
		}
		if wrong.Cookie(apitest.RefreshCookie) != nil {
			t.Errorf("failed login set a refresh cookie")
		}
	})

	t.Run("Google-only account cannot log in with a password", func(t *testing.T) {
		email := apitest.UniqueEmail()
		s.Google.Add("tok-login-"+email, googleClaims("sub-"+email, email))
		s.Do(t, post("/auth/google", map[string]string{"idToken": "tok-login-" + email})).Status(t, http.StatusOK)

		res := s.Login(t, email, "Any-password-1")
		wantError(t, res, http.StatusUnauthorized, "unauthorized", "")
		if e := decodeError(t, res); e.Message != "email o contraseña incorrectos" {
			t.Errorf("message = %q, want the generic one", e.Message)
		}
	})

	t.Run("malformed body", func(t *testing.T) {
		wantError(t, s.Do(t, post("/auth/login", "[]")), http.StatusUnprocessableEntity, "validation", "")
	})
}

func TestAccessTokenIsRequired(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	u := s.Register(t)
	refresh := s.Login(t, u.Email, u.Password).Status(t, http.StatusOK).RefreshCookieValue(t)

	sign := func(secret string, claims jwt.MapClaims) string {
		tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	now := time.Now()
	claims := func(typ string, exp time.Time) jwt.MapClaims {
		return jwt.MapClaims{"typ": typ, "sub": u.ID, "iss": "ahorra-api",
			"iat": now.Add(-time.Hour).Unix(), "nbf": now.Add(-time.Hour).Unix(), "exp": exp.Unix()}
	}

	cases := []struct {
		name   string
		header string
	}{
		{"no Authorization header", ""},
		{"not a Bearer scheme", "Basic " + u.Token},
		{"empty Bearer", "Bearer "},
		{"garbage token", "Bearer not.a.jwt"},
		{"refresh token used as access", "Bearer " + refresh},
		{"signed with another secret", "Bearer " + sign(strings.Repeat("z", 40), claims("access", now.Add(time.Hour)))},
		{"expired", "Bearer " + sign(s.Config.JWTSecret, claims("access", now.Add(-time.Minute)))},
		{"wrong typ claim", "Bearer " + sign(s.Config.JWTSecret, claims("refresh", now.Add(time.Hour)))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.header != "" {
				h.Set("Authorization", tc.header)
			}
			for _, r := range []apitest.Req{
				{Method: http.MethodGet, Path: "/me", Header: h},
				{Method: http.MethodPatch, Path: "/me", Header: h, Body: map[string]string{"firstName": "X"}},
				{Method: http.MethodDelete, Path: "/me", Header: h},
				{Method: http.MethodPost, Path: "/auth/change-password", Header: h,
					Body: map[string]string{"currentPassword": u.Password, "newPassword": "Other-password-1"}},
				{Method: http.MethodPost, Path: "/auth/resend-verification-email", Header: h},
			} {
				wantError(t, s.Do(t, r), http.StatusUnauthorized, "unauthorized", "")
			}
		})
	}

	t.Run("a valid hand-signed access token is accepted", func(t *testing.T) {
		tok := sign(s.Config.JWTSecret, claims("access", now.Add(time.Hour)))
		if got := getMe(t, s, tok); got.ID != u.ID {
			t.Errorf("/me id = %s", got.ID)
		}
	})
}

func TestRefreshAndLogout(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)

	t.Run("cookie session refreshes and rotates the cookie", func(t *testing.T) {
		u := s.Register(t)
		jar := apitest.NewSession(t)
		s.Login(t, u.Email, u.Password, jar).Status(t, http.StatusOK)

		res := s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/refresh", Client: jar}).Status(t, http.StatusOK)
		var body struct {
			AccessToken     string    `json:"accessToken"`
			AccessExpiresAt time.Time `json:"accessExpiresAt"`
		}
		res.JSON(t, &body)
		if body.AccessToken == "" || body.AccessExpiresAt.Before(time.Now()) {
			t.Fatalf("refresh = %s", res.Body)
		}
		res.RefreshCookieValue(t)
		if got := getMe(t, s, body.AccessToken); got.ID != u.ID {
			t.Errorf("/me id = %s", got.ID)
		}
	})

	t.Run("no cookie", func(t *testing.T) {
		wantError(t, s.Do(t, post("/auth/refresh", nil)), http.StatusUnauthorized, "unauthorized", "")
	})

	t.Run("invalid cookie is rejected and cleared", func(t *testing.T) {
		u := s.Register(t)
		for name, tok := range map[string]string{"garbage": "nope", "access token": u.Token} {
			res := s.Refresh(t, tok)
			wantError(t, res, http.StatusUnauthorized, "unauthorized", "")
			if c := res.Cookie(apitest.RefreshCookie); c == nil || c.Value != "" || c.MaxAge >= 0 {
				t.Errorf("%s: cookie not cleared: %+v", name, c)
			}
		}
	})

	t.Run("logout clears the cookie", func(t *testing.T) {
		u := s.Register(t)
		jar := apitest.NewSession(t)
		s.Login(t, u.Email, u.Password, jar).Status(t, http.StatusOK)

		res := s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/logout", Client: jar}).Status(t, http.StatusNoContent)
		if c := res.Cookie(apitest.RefreshCookie); c == nil || c.MaxAge >= 0 {
			t.Errorf("logout cookie = %+v, want deletion", c)
		}
		wantError(t, s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/refresh", Client: jar}),
			http.StatusUnauthorized, "unauthorized", "")
	})

	t.Run("logout without a session is still 204", func(t *testing.T) {
		s.Do(t, post("/auth/logout", nil)).Status(t, http.StatusNoContent)
	})

	t.Run("a refresh token copied before logout keeps working", func(t *testing.T) {
		u := s.Register(t)
		stolen := s.Login(t, u.Email, u.Password).RefreshCookieValue(t)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/logout", Header: apitest.WithRefreshCookie(stolen)}).
			Status(t, http.StatusNoContent)

		testutil.KnownBug(t, "0.5", "logout does not revoke the refresh token server-side", func(t testing.TB) {
			s.Refresh(t, stolen).Status(t, http.StatusUnauthorized)
		})
	})
}

func TestAuthRateLimits(t *testing.T) {
	t.Parallel()

	t.Run("login: 5 per minute per IP", func(t *testing.T) {
		t.Parallel()
		s := apitest.New(t, apitest.WithRealRateLimits())
		for i := range 5 {
			if c := s.Login(t, "nobody@example.test", "x").Code; c != http.StatusUnauthorized {
				t.Fatalf("attempt %d: status %d", i+1, c)
			}
		}
		s.Login(t, "nobody@example.test", "x").Status(t, http.StatusTooManyRequests)
	})

	t.Run("forgot-password: 3 per hour per IP", func(t *testing.T) {
		t.Parallel()
		s := apitest.New(t, apitest.WithRealRateLimits())
		body := map[string]string{"email": "nobody@example.test"}
		for range 3 {
			s.Do(t, post("/auth/forgot-password", body)).Status(t, http.StatusNoContent)
		}
		s.Do(t, post("/auth/forgot-password", body)).Status(t, http.StatusTooManyRequests)
	})

	t.Run("a spoofed X-Forwarded-For gets around the limit", func(t *testing.T) {
		t.Parallel()
		s := apitest.New(t, apitest.WithRealRateLimits())
		testutil.KnownBug(t, "0.6", "RealIP trusts X-Forwarded-For from any client", func(t testing.TB) {
			var last int
			for i := range 6 {
				last = s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/login",
					Body:   map[string]string{"email": "nobody@example.test", "password": "x"},
					Header: http.Header{"X-Forwarded-For": {fmt.Sprintf("203.0.113.%d", i+1)}}}).Code
			}
			if last != http.StatusTooManyRequests {
				t.Errorf("6th login from one client = %d, want 429", last)
			}
		})
	})
}
