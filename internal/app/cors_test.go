package app_test

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

func TestCORS(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	origin := s.Config.AllowedOrigins[0]

	t.Run("allowed origin gets credentials and the request id", func(t *testing.T) {
		res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/health/live", Header: http.Header{"Origin": {origin}}})
		res.Status(t, http.StatusOK)
		if res.Header.Get("Access-Control-Allow-Origin") != origin || res.Header.Get("Access-Control-Allow-Credentials") != "true" {
			t.Errorf("CORS headers = %v", res.Header)
		}
		exposed := strings.ToLower(res.Header.Get("Access-Control-Expose-Headers"))
		if !strings.Contains(exposed, "x-request-id") {
			t.Errorf("exposed = %q", exposed)
		}
		testutil.KnownBug(t, "0.1", "CORS does not expose Retry-After, so the app cannot show the retry delay", func(t testing.TB) {
			if !strings.Contains(exposed, "retry-after") {
				t.Errorf("exposed headers = %q", exposed)
			}
		})
	})

	t.Run("preflight allows the household header", func(t *testing.T) {
		res := s.Do(t, apitest.Req{Method: http.MethodOptions, Path: "/expenses", Header: http.Header{
			"Origin": {origin}, "Access-Control-Request-Method": {"POST"},
			"Access-Control-Request-Headers": {"authorization,content-type,x-household-id"},
		}})
		if res.Code >= 300 || !strings.Contains(strings.ToLower(res.Header.Get("Access-Control-Allow-Headers")), "x-household-id") {
			t.Errorf("preflight = %d %v", res.Code, res.Header)
		}
	})

	t.Run("other origins get no CORS headers", func(t *testing.T) {
		res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/health/live", Header: http.Header{"Origin": {"https://evil.example"}}})
		if res.Header.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("evil origin allowed: %v", res.Header)
		}
	})
}
