package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/httpx"
)

func TestRealIP(t *testing.T) {
	trusted, _ := httpx.ParsePrefixes([]string{"10.0.0.0/8"})
	cf, _ := httpx.ParsePrefixes(httpx.CloudflareRanges)
	mw := httpx.RealIP(trusted, cf)

	for _, tc := range []struct {
		name, remote, xff, cfip, want string
	}{
		{"direct client, headers ignored", "198.51.100.9:5555", "203.0.113.1", "203.0.113.2", "198.51.100.9"},
		{"via Cloudflare and Traefik", "10.0.1.5:443", "203.0.113.50, 172.70.1.1", "203.0.113.50", "203.0.113.50"},
		{"forged XFF prefix is skipped", "10.0.1.5:443", "1.2.3.4, 203.0.113.50, 172.70.1.1", "203.0.113.50", "203.0.113.50"},
		{"origin reached directly through Traefik: forged CF header ignored", "10.0.1.5:443", "198.51.100.9", "203.0.113.77", "198.51.100.9"},
		{"Cloudflare without CF header falls back to the edge", "10.0.1.5:443", "172.70.1.1", "", "172.70.1.1"},
		{"trusted peer without XFF", "10.0.1.5:443", "", "", "10.0.1.5"},
		{"garbage XFF", "10.0.1.5:443", "not-an-ip", "", "10.0.1.5"},
		{"IPv6 Cloudflare edge", "10.0.1.5:443", "2001:db8::1, 2606:4700::1", "2001:db8::1", "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.cfip != "" {
				r.Header.Set("CF-Connecting-IP", tc.cfip)
			}
			var got string
			mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { got = r.RemoteAddr })).ServeHTTP(httptest.NewRecorder(), r)
			if got != tc.want {
				t.Errorf("client = %s, want %s", got, tc.want)
			}
		})
	}

	if _, err := httpx.ParsePrefixes([]string{"10.0.0.0/33"}); err == nil {
		t.Error("an invalid range was accepted")
	}
	if ps, err := httpx.ParsePrefixes([]string{" 192.0.2.1 ", ""}); err != nil || len(ps) != 1 || ps[0].Bits() != 32 {
		t.Errorf("single IP = %v, %v", ps, err)
	}
}
