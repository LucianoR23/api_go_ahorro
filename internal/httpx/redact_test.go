package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/httpx"
)

func TestRedactQueryInLogs(t *testing.T) {
	var gotURI, gotTicket string
	h := httpx.RedactQueryInLogs(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURI, gotTicket = r.RequestURI, r.URL.Query().Get("ticket")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/insights/stream?ticket=secret&x=1", nil))
	if gotURI != "/insights/stream" {
		t.Errorf("RequestURI = %q, want the path only", gotURI)
	}
	if gotTicket != "secret" {
		t.Errorf("the handler lost the query: ticket = %q", gotTicket)
	}

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if gotURI != "/health/live" {
		t.Errorf("RequestURI without query = %q", gotURI)
	}
}
