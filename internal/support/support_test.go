package support_test

// Characterization tests of the support proxy (item 0.21c). The upstream
// support service is a local httptest server; nothing leaves the machine.

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/config"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

const apiKey = "support-test-key"

// upstream records what the proxy sent and answers with a canned response.
type upstream struct {
	mu      sync.Mutex
	got     []seen
	status  int
	body    string
	headers http.Header
}

type seen struct {
	Method, Path, Query, ContentType string
	Header                           http.Header
	Body                             []byte
}

func (u *upstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.got = append(u.got, seen{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), r.Header.Clone(), body})
	status, respBody, headers := u.status, u.body, u.headers
	u.mu.Unlock()
	for k, vs := range headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, respBody)
}

func (u *upstream) last(t testing.TB) seen {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if len(u.got) == 0 {
		t.Fatal("upstream received nothing")
	}
	return u.got[len(u.got)-1]
}

func (u *upstream) reply(status int, body string, headers http.Header) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.status, u.body, u.headers = status, body, headers
}

func withUpstream(t *testing.T) (*apitest.Server, *upstream) {
	up := &upstream{headers: http.Header{"Content-Type": {"application/json"}}, body: `{"ok":true}`}
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	s := apitest.New(t, apitest.WithConfig(func(c *config.Config) {
		c.SupportAPIURL = srv.URL
		c.SupportAPIKey = apiKey
	}))
	return s, up
}

func TestSupportProxy(t *testing.T) {
	t.Parallel()
	s, up := withUpstream(t)
	u := s.Register(t)

	t.Run("forwards identity and the API key, never the user's token", func(t *testing.T) {
		res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/support/tickets/mine?status=open&page=2", Token: u.Token}).
			Status(t, http.StatusOK)
		if string(res.Body) != `{"ok":true}` || res.Header.Get("Content-Type") != "application/json" {
			t.Errorf("proxied response = %q %q", res.Body, res.Header.Get("Content-Type"))
		}
		got := up.last(t)
		if got.Method != http.MethodGet || got.Path != "/tickets/mine" || got.Query != "status=open&page=2" {
			t.Errorf("upstream request = %s %s?%s", got.Method, got.Path, got.Query)
		}
		if got.Header.Get("X-App-Key") != apiKey || got.Header.Get("X-Reporter-User-Id") != u.ID || got.Header.Get("X-Reporter-Email") != u.Email {
			t.Errorf("identity headers = %v", got.Header)
		}
		if got.Header.Get("Authorization") != "" {
			t.Errorf("user's bearer token leaked upstream")
		}
	})

	t.Run("ticket detail and new message", func(t *testing.T) {
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/support/tickets/t-123", Token: u.Token}).Status(t, http.StatusOK)
		if got := up.last(t); got.Path != "/tickets/t-123" {
			t.Errorf("detail path = %s", got.Path)
		}
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/support/tickets/t-123/messages", Token: u.Token,
			Body: map[string]string{"body": "hola"}}).Status(t, http.StatusOK)
		got := up.last(t)
		if got.Method != http.MethodPost || got.Path != "/tickets/t-123/messages" || got.ContentType != "application/json" ||
			string(got.Body) != `{"body":"hola"}` {
			t.Errorf("message = %+v", got)
		}
	})

	t.Run("create ticket streams the multipart body", func(t *testing.T) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("subject", "No anda")
		fw, _ := mw.CreateFormFile("attachment", "captura.png")
		_, _ = fw.Write([]byte("fake-png"))
		_ = mw.Close()

		up.reply(http.StatusCreated, `{"id":"t-9"}`, http.Header{"Content-Type": {"application/json"}})
		defer up.reply(http.StatusOK, `{"ok":true}`, http.Header{"Content-Type": {"application/json"}})
		res := s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/support/tickets", Token: u.Token, Body: buf.Bytes(),
			Header: http.Header{"Content-Type": {mw.FormDataContentType()}}})
		res.Status(t, http.StatusCreated)

		got := up.last(t)
		if got.Path != "/tickets" || got.ContentType != mw.FormDataContentType() || !bytes.Contains(got.Body, []byte("fake-png")) {
			t.Errorf("upstream ticket = %s %s (%d bytes)", got.Path, got.ContentType, len(got.Body))
		}
	})

	t.Run("upstream status, body and Retry-After pass through", func(t *testing.T) {
		up.reply(http.StatusTooManyRequests, `{"code":"rate_limited"}`, http.Header{"Content-Type": {"application/json"}, "Retry-After": {"30"}})
		defer up.reply(http.StatusOK, `{"ok":true}`, http.Header{"Content-Type": {"application/json"}})
		res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/support/tickets/mine", Token: u.Token})
		res.Status(t, http.StatusTooManyRequests)
		if res.Header.Get("Retry-After") != "30" || !strings.Contains(string(res.Body), "rate_limited") {
			t.Errorf("passthrough = %v %s", res.Header, res.Body)
		}
	})

	t.Run("an upstream 401 reaches the app as a session error", func(t *testing.T) {
		up.reply(http.StatusUnauthorized, `{"code":"unauthorized"}`, http.Header{"Content-Type": {"application/json"}})
		defer up.reply(http.StatusOK, `{"ok":true}`, http.Header{"Content-Type": {"application/json"}})
		res := s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/support/tickets/mine", Token: u.Token})
		testutil.KnownBug(t, "0.1", "the support proxy passes upstream 401 through, so the app logs the user out", func(t testing.TB) {
			res.Status(t, http.StatusBadGateway)
		})
	})

	t.Run("requires auth", func(t *testing.T) {
		for _, r := range []apitest.Req{
			{Method: http.MethodGet, Path: "/support/tickets/mine"},
			{Method: http.MethodGet, Path: "/support/tickets/t-1"},
			{Method: http.MethodPost, Path: "/support/tickets/t-1/messages", Body: map[string]string{"body": "x"}},
			{Method: http.MethodPost, Path: "/support/tickets", Body: "x"},
		} {
			s.Do(t, r).WantError(t, http.StatusUnauthorized, "unauthorized", "")
		}
	})
}

func TestSupportUnavailable(t *testing.T) {
	t.Parallel()

	t.Run("not configured: 503", func(t *testing.T) {
		t.Parallel()
		s := apitest.New(t) // no SUPPORT API key
		u := s.Register(t)
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/support/tickets/mine", Token: u.Token}).
			WantError(t, http.StatusServiceUnavailable, "internal", "")
	})

	t.Run("upstream down: 502", func(t *testing.T) {
		t.Parallel()
		dead := httptest.NewServer(http.NotFoundHandler())
		url := dead.URL
		dead.Close()
		s := apitest.New(t, apitest.WithConfig(func(c *config.Config) {
			c.SupportAPIURL = url
			c.SupportAPIKey = apiKey
		}))
		u := s.Register(t)
		s.Do(t, apitest.Req{Method: http.MethodGet, Path: "/support/tickets/mine", Token: u.Token}).
			WantError(t, http.StatusBadGateway, "internal", "")
	})
}
