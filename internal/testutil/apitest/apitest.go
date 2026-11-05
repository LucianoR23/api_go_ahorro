// Package apitest runs the real API (internal/app router, middlewares and
// services) over HTTP against a fresh database, with fakes for the external
// services (email, Google, rate limiting). Use it from external test packages
// (package foo_test): importing it from package foo would be an import cycle.
//
//	s := apitest.New(t)
//	u := s.Register(t)
//	res := s.Do(t, apitest.Req{Method: "GET", Path: "/auth/me", Token: u.Token})
//	res.Status(t, http.StatusOK)
package apitest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/app"
	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/config"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

// Server is a running API backed by its own database.
type Server struct {
	URL    string
	Pool   *pgxpool.Pool
	Mail   *FakeMailer
	Google *FakeGoogle
	Config *config.Config
}

// Option tweaks the Config or the app.Options before the API is built.
type Option func(*config.Config, *app.Options)

// WithRealRateLimits keeps the production auth rate limiter (off by default
// so tests can create many users).
func WithRealRateLimits() Option {
	return func(_ *config.Config, o *app.Options) { o.AuthRateLimiter = auth.NewInMemoryRateLimiter() }
}

// WithConfig edits the Config (e.g. SupportAPIURL pointing at a fake server).
func WithConfig(f func(*config.Config)) Option {
	return func(c *config.Config, _ *app.Options) { f(c) }
}

// New starts the API on an httptest server with a fresh, migrated database.
// Background jobs are not started: tests call services or endpoints directly.
func New(t testing.TB, opts ...Option) *Server {
	t.Helper()
	pool := testdb.New(t)

	cfg := &config.Config{
		Port:                   "0",
		Env:                    "dev",
		LogLevel:               "error",
		JWTSecret:              strings.Repeat("a", 32) + "-test-access",
		JWTRefreshSecret:       strings.Repeat("b", 32) + "-test-refresh",
		AllowedOrigins:         []string{"http://localhost:3005"},
		ReportFromEmail:        "Test <reports@example.test>",
		InviteFromEmail:        "Test <invites@example.test>",
		PasswordResetFromEmail: "Test <reset@example.test>",
		VerificationFromEmail:  "Test <verify@example.test>",
		AppBaseURL:             "http://localhost:3005",
		GoogleOAuthClientID:    "test-client-id",
		VAPIDSubject:           "mailto:test@example.test",
		SupportAPIURL:          "http://127.0.0.1:1", // unreachable unless a test overrides it
	}
	mail := &FakeMailer{}
	g := &FakeGoogle{}
	appOpts := app.Options{
		NewEmailSender:  func(from string) app.EmailSender { return mail.from(from) },
		GoogleVerifier:  g,
		AuthRateLimiter: noLimits{},
		FXHTTPClient:    &http.Client{Transport: blockedTransport{}},
	}
	for _, o := range opts {
		o(cfg, &appOpts)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := app.New(context.Background(), cfg, pool, logger, appOpts)
	if err != nil {
		t.Fatalf("apitest: build app: %v", err)
	}
	srv := httptest.NewServer(a.Router())
	t.Cleanup(srv.Close)

	return &Server{URL: srv.URL, Pool: pool, Mail: mail, Google: g, Config: cfg}
}

// Req is one HTTP request against the server.
type Req struct {
	Method    string
	Path      string
	Body      any    // marshalled as JSON when not nil (string/[]byte sent raw)
	Token     string // Bearer access token
	Household string // X-Household-ID
	Header    http.Header
	Client    *http.Client // e.g. Session.Client to keep cookies; default: no cookies
}

// Res is the response, body already read.
type Res struct {
	Code   int
	Header http.Header
	Body   []byte
}

// Do sends the request and fails the test on transport errors.
func (s *Server) Do(t testing.TB, r Req) *Res {
	t.Helper()
	var body io.Reader
	switch b := r.Body.(type) {
	case nil:
	case string:
		body = strings.NewReader(b)
	case []byte:
		body = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("apitest: marshal body: %v", err)
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(r.Method, s.URL+r.Path, body)
	if err != nil {
		t.Fatalf("apitest: new request: %v", err)
	}
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	if r.Household != "" {
		req.Header.Set("X-Household-ID", r.Household)
	}
	for k, vs := range r.Header {
		req.Header.Del(k) // explicit headers replace the defaults above
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("apitest: %s %s: %v", r.Method, r.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("apitest: read body: %v", err)
	}
	return &Res{Code: resp.StatusCode, Header: resp.Header, Body: raw}
}

// Status fails the test unless the response has the given status code.
func (r *Res) Status(t testing.TB, want int) *Res {
	t.Helper()
	if r.Code != want {
		t.Fatalf("status %d, want %d; body: %s", r.Code, want, r.Body)
	}
	return r
}

// JSON decodes the body into v.
func (r *Res) JSON(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("apitest: decode %s: %v", r.Body, err)
	}
}

// ErrorCode returns the "code" field of an error response.
func (r *Res) ErrorCode(t testing.TB) string {
	t.Helper()
	var e struct {
		Code string `json:"code"`
	}
	r.JSON(t, &e)
	return e.Code
}

// NewSession returns an HTTP client that keeps cookies (refresh token).
func NewSession(t testing.TB) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar}
}

// UniqueEmail returns an email nobody else in the test uses.
func UniqueEmail() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "user-" + hex.EncodeToString(b) + "@example.test"
}

// noLimits disables the auth rate limits.
type noLimits struct{}

func pass(next http.Handler) http.Handler { return next }

func (noLimits) Login() func(http.Handler) http.Handler              { return pass }
func (noLimits) Register() func(http.Handler) http.Handler           { return pass }
func (noLimits) Refresh() func(http.Handler) http.Handler            { return pass }
func (noLimits) ForgotPassword() func(http.Handler) http.Handler     { return pass }
func (noLimits) ResetPassword() func(http.Handler) http.Handler      { return pass }
func (noLimits) ChangePassword() func(http.Handler) http.Handler     { return pass }
func (noLimits) VerifyEmail() func(http.Handler) http.Handler        { return pass }
func (noLimits) ResendVerification() func(http.Handler) http.Handler { return pass }

// blockedTransport makes any unexpected outbound HTTP call fail loudly:
// external services must be faked in tests (plan §0).
type blockedTransport struct{}

func (blockedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, &blockedError{url: r.URL.String()}
}

type blockedError struct{ url string }

func (e *blockedError) Error() string { return "apitest: outbound HTTP blocked in tests: " + e.url }
