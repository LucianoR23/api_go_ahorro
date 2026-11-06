package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

func TestSecurityHeaders(t *testing.T) {
	h := httpx.SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	for k, want := range map[string]string{
		"Strict-Transport-Security": "max-age=63072000; includeSubDomains; preload",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "no-referrer",
		"X-Frame-Options":           "DENY",
		"Content-Security-Policy":   "default-src 'none'; frame-ancestors 'none'; base-uri 'none'",
	} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestSameOriginCookies(t *testing.T) {
	mw := httpx.SameOriginCookies([]string{"https://app.example/"})
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		name, origin, site string
		want               int
	}{
		{"allowed origin", "https://app.example", "same-site", http.StatusNoContent},
		{"foreign origin", "https://evil.example", "cross-site", http.StatusForbidden},
		{"null origin", "null", "", http.StatusForbidden},
		{"no origin, cross-site fetch", "", "cross-site", http.StatusForbidden},
		{"no origin, same-origin fetch", "", "same-origin", http.StatusNoContent},
		{"non-browser client", "", "", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}
