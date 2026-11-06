package httpx

import (
	"net/http"
	"strings"
)

// SecurityHeaders sets the headers every API response carries (plan 0.16).
// The API answers JSON, so its CSP allows nothing and no framing; the few
// HTML pages it serves (waitlist unsubscribe) set their own CSP.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-site")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r)
	})
}

// SameOriginCookies guards the endpoints that act on the refresh cookie
// (/auth/refresh, /auth/logout) against CSRF (plan 0.16). A browser always
// sends Origin on a POST: it must be one of allowed. Without Origin, a
// browser's Sec-Fetch-Site must not be "cross-site". Requests with neither
// (non-browser clients) carry no ambient cookie risk and pass.
func SameOriginCookies(allowed []string) func(http.Handler) http.Handler {
	ok := make(map[string]bool, len(allowed))
	for _, o := range allowed {
		ok[strings.TrimRight(o, "/")] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			site := r.Header.Get("Sec-Fetch-Site")
			if (origin != "" && !ok[origin]) || (origin == "" && site == "cross-site") {
				WriteJSON(w, http.StatusForbidden, ErrorResponse{Code: "forbidden", Message: "origen no permitido"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
