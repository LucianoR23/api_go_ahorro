package apitest

import (
	"net/http"
	"testing"
)

// RefreshCookie is the refresh-token cookie name outside prod (apitest runs
// with ENV=dev; prod uses the __Host- prefixed name).
const RefreshCookie = "ahorra_refresh"

// Cookie returns the Set-Cookie named name, or nil when the response did not
// set it.
func (r *Res) Cookie(name string) *http.Cookie {
	for _, c := range (&http.Response{Header: r.Header}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// RefreshCookieValue returns the refresh token the response set, failing the
// test when there is none.
func (r *Res) RefreshCookieValue(t testing.TB) string {
	t.Helper()
	c := r.Cookie(RefreshCookie)
	if c == nil || c.Value == "" {
		t.Fatalf("apitest: response set no %s cookie; headers: %v", RefreshCookie, r.Header.Values("Set-Cookie"))
	}
	return c.Value
}

// WithRefreshCookie returns a header that sends token as the refresh cookie.
func WithRefreshCookie(token string) http.Header {
	return http.Header{"Cookie": {RefreshCookie + "=" + token}}
}

// Login posts the credentials to /auth/login and returns the raw response.
func (s *Server) Login(t testing.TB, email, password string, client ...*http.Client) *Res {
	t.Helper()
	r := Req{Method: http.MethodPost, Path: "/auth/login",
		Body: map[string]string{"email": email, "password": password}}
	if len(client) > 0 {
		r.Client = client[0]
	}
	return s.Do(t, r)
}

// Refresh posts to /auth/refresh with token as the refresh cookie.
func (s *Server) Refresh(t testing.TB, token string) *Res {
	t.Helper()
	return s.Do(t, Req{Method: http.MethodPost, Path: "/auth/refresh", Header: WithRefreshCookie(token)})
}

// APIError is the body of every error response.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

// WantError fails unless the response is an error with this status, code and
// (when field is not empty) field. It returns the decoded error.
func (r *Res) WantError(t testing.TB, status int, code, field string) APIError {
	t.Helper()
	r.Status(t, status)
	var e APIError
	r.JSON(t, &e)
	if e.Code != code {
		t.Errorf("error code = %q, want %q (body %s)", e.Code, code, r.Body)
	}
	if field != "" && e.Field != field {
		t.Errorf("error field = %q, want %q (body %s)", e.Field, field, r.Body)
	}
	return e
}
