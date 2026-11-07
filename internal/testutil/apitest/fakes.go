package apitest

import (
	"context"
	"regexp"
	"sync"
	"testing"

	"github.com/LucianoR23/valuo_api/internal/app"
	"github.com/LucianoR23/valuo_api/internal/auth/google"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Mail is one email the API tried to send.
type Mail struct {
	From    string
	To      []string
	Subject string
	HTML    string
	Headers map[string]string
}

// FakeMailer records outgoing email instead of calling Resend.
type FakeMailer struct {
	mu   sync.Mutex
	sent []Mail
	// Fail makes every Send return this error (to test delivery failures).
	Fail error
}

func (m *FakeMailer) from(from string) app.EmailSender { return &mailerFrom{m: m, from: from} }

type mailerFrom struct {
	m    *FakeMailer
	from string
}

func (s *mailerFrom) Configured() bool { return true }

func (s *mailerFrom) Send(ctx context.Context, to []string, subject, html string) error {
	return s.SendWithHeaders(ctx, to, subject, html, nil)
}

func (s *mailerFrom) SendWithHeaders(_ context.Context, to []string, subject, html string, headers map[string]string) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if s.m.Fail != nil {
		return s.m.Fail
	}
	s.m.sent = append(s.m.sent, Mail{From: s.from, To: append([]string(nil), to...), Subject: subject, HTML: html, Headers: headers})
	return nil
}

// Sent returns a copy of every recorded email.
func (m *FakeMailer) Sent() []Mail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Mail(nil), m.sent...)
}

// LastTo returns the most recent email to addr, failing if there is none.
func (m *FakeMailer) LastTo(t testing.TB, addr string) Mail {
	t.Helper()
	sent := m.Sent()
	for i := len(sent) - 1; i >= 0; i-- {
		for _, to := range sent[i].To {
			if to == addr {
				return sent[i]
			}
		}
	}
	t.Fatalf("apitest: no email sent to %s (sent: %d)", addr, len(sent))
	return Mail{}
}

var tokenRe = regexp.MustCompile(`[?&]token=([A-Za-z0-9_\-]+)`)

// Token extracts the ?token= value from the email's links.
func (m Mail) Token(t testing.TB) string {
	t.Helper()
	match := tokenRe.FindStringSubmatch(m.HTML)
	if match == nil {
		t.Fatalf("apitest: no token link in email %q", m.Subject)
	}
	return match[1]
}

// FakeGoogle accepts the ID tokens registered with Add.
type FakeGoogle struct {
	mu     sync.Mutex
	tokens map[string]google.Claims
}

// Add makes idToken valid, resolving to claims.
func (g *FakeGoogle) Add(idToken string, claims google.Claims) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tokens == nil {
		g.tokens = map[string]google.Claims{}
	}
	g.tokens[idToken] = claims
}

func (g *FakeGoogle) Verify(_ context.Context, idToken string) (*google.Claims, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c, ok := g.tokens[idToken]
	if !ok {
		// Same error the real verifier returns for a bad token.
		return nil, domain.NewAuthError("token de Google inválido")
	}
	return &c, nil
}

// FakeTurnstile stands in for Cloudflare's siteverify: tokens added with Add
// pass once (like the real service), anything else fails.
type FakeTurnstile struct {
	mu     sync.Mutex
	tokens map[string]string // token -> hostname
}

// Add makes token valid once, as if the widget ran on hostname.
func (f *FakeTurnstile) Add(token, hostname string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.tokens == nil {
		f.tokens = map[string]string{}
	}
	f.tokens[token] = hostname
}

func (f *FakeTurnstile) Verify(_ context.Context, token, _ string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	host, ok := f.tokens[token]
	delete(f.tokens, token)
	return host, ok, nil
}
