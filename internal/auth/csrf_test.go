package auth_test

import (
	"net/http"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

// Plan 0.16: the refresh-cookie endpoints reject cross-site browser requests.
func TestRefreshCookieCSRF(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	u := s.Register(t)
	token := s.Login(t, u.Email, u.Password).Status(t, http.StatusOK).RefreshCookieValue(t)

	for _, path := range []string{"/auth/refresh", "/auth/logout"} {
		h := apitest.WithRefreshCookie(token)
		h.Set("Origin", "https://evil.example")
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: path, Header: h}).
			WantError(t, http.StatusForbidden, "forbidden", "")
	}

	// The session survived both attempts; the app's own origin works.
	h := apitest.WithRefreshCookie(token)
	h.Set("Origin", "http://localhost:3005")
	res := s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/auth/refresh", Header: h}).Status(t, http.StatusOK)
	if res.Header.Get("Strict-Transport-Security") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("security headers missing: %v", res.Header)
	}
}
