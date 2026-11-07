package monitoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// fakeTransport keeps the events instead of sending them.
type fakeTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (f *fakeTransport) Flush(time.Duration) bool              { return true }
func (f *fakeTransport) FlushWithContext(context.Context) bool { return true }
func (f *fakeTransport) Configure(sentry.ClientOptions)        {}
func (f *fakeTransport) Close()                                {}
func (f *fakeTransport) SendEvent(e *sentry.Event) {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
}
func (f *fakeTransport) all() []*sentry.Event { f.mu.Lock(); defer f.mu.Unlock(); return f.events }

func newTestLogger(t *testing.T) (*slog.Logger, *fakeTransport) {
	t.Helper()
	ft := &fakeTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn:            "https://public@glitchtip.example/1",
		Transport:      ft,
		BeforeSend:     beforeSend,
		MaxBreadcrumbs: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	hub := sentry.NewHub(client, sentry.NewScope())
	base := slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(NewHandler(base, hub)), ft
}

func TestHandlerReportsErrorsWithoutPersonalData(t *testing.T) {
	t.Parallel()
	logger, ft := newTestLogger(t)

	userID := "3f2b8c1e-9a4d-4c7e-8f00-1b2c3d4e5f60"
	cause := fmt.Errorf("settlements.Create: %w",
		errors.New(`ERROR: duplicate key (SQLSTATE 23505): Key (email)=(ana@example.com) amount 1234.56`))
	logger.With("user_id", userID).ErrorContext(context.Background(), "error interno no manejado",
		"error", cause,
		"method", "POST",
		"path", "/households/"+userID+"/settlements",
		"amount", "1234.56",
		"email", "ana@example.com",
		"description", "cena con Ana",
	)
	logger.Info("not an error", "amount", 5)
	logger.Warn("not an error either")

	evs := ft.all()
	if len(evs) != 1 {
		t.Fatalf("%d events, want 1 (only ERROR is reported)", len(evs))
	}
	ev := evs[0]
	if ev.Message != "error interno no manejado" {
		t.Errorf("message %q", ev.Message)
	}
	if ev.Tags["method"] != "POST" || ev.Tags["path"] != "/households/[id]/settlements" {
		t.Errorf("tags %v", ev.Tags)
	}
	for _, k := range []string{"user_id", "amount", "email", "description"} {
		if _, ok := ev.Tags[k]; ok {
			t.Errorf("tag %q leaked", k)
		}
	}
	if len(ev.Exception) == 0 || ev.Exception[len(ev.Exception)-1].Stacktrace == nil {
		t.Fatalf("no exception with a stack trace: %+v", ev.Exception)
	}
	if got := ev.Exception[len(ev.Exception)-1].Type; got != "error interno no manejado" {
		t.Errorf("exception type %q, want the log message (the issue title)", got)
	}
	if !strings.Contains(ev.Exception[len(ev.Exception)-1].Value, "SQLSTATE 23505") {
		t.Errorf("SQLSTATE dropped: %q", ev.Exception[len(ev.Exception)-1].Value)
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{userID, "ana@example.com", "1234.56", "cena con Ana"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("event carries %q: %s", secret, raw)
		}
	}
}

func TestHandlerWithoutErrorAttr(t *testing.T) {
	t.Parallel()
	logger, ft := newTestLogger(t)
	logger.Error("jobs: river start failed")
	evs := ft.all()
	if len(evs) != 1 || len(evs[0].Exception) != 1 || evs[0].Exception[0].Stacktrace == nil {
		t.Fatalf("want one event with a stack trace, got %+v", evs)
	}
}

func TestHandlerLogsEvenWhenNextFiltersLevel(t *testing.T) {
	t.Parallel()
	var buf strings.Builder
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})
	logger := slog.New(NewHandler(base, nil)) // no client on the current hub: report is a no-op
	logger.Info("hidden")
	logger.Error("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Errorf("log output %q", buf.String())
	}
}

func TestBeforeSendDropsRequestAndUser(t *testing.T) {
	t.Parallel()
	ev := sentry.NewEvent()
	ev.Message = "falló para ana@example.com"
	ev.Request = &sentry.Request{URL: "https://api/x?email=ana@example.com", Cookies: "s=1"}
	ev.User = sentry.User{Email: "ana@example.com", ID: "1"}
	ev.Breadcrumbs = []*sentry.Breadcrumb{{Message: "x"}}
	ev.Contexts = map[string]sentry.Context{"os": {"name": "linux"}, "custom": {"amount": 5}}
	ev.Exception = []sentry.Exception{{Type: "*errors.errorString", Value: "monto 300"}}

	out := beforeSend(ev, nil)
	if out.Request != nil || out.User.Email != "" || out.User.ID != "" || out.Breadcrumbs != nil {
		t.Errorf("request/user/breadcrumbs kept: %+v", out)
	}
	if _, ok := out.Contexts["custom"]; ok {
		t.Error("custom context kept")
	}
	if _, ok := out.Contexts["os"]; !ok {
		t.Error("os context dropped")
	}
	if out.Message != "falló para [email]" || out.Exception[0].Value != "monto [n]" {
		t.Errorf("not scrubbed: %q / %q", out.Message, out.Exception[0].Value)
	}
}

func TestInit(t *testing.T) {
	flush, err := Init(Options{})
	if err != nil {
		t.Fatal(err)
	}
	flush()
	if _, err := Init(Options{DSN: "not a dsn"}); err == nil {
		t.Error("invalid DSN accepted")
	}

	ft := &fakeTransport{}
	flush, err = Init(Options{DSN: "https://public@glitchtip.example/1", Environment: "staging", Transport: ft})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sentry.CurrentHub().BindClient(nil) })
	slog.New(NewHandler(slog.NewTextHandler(io.Discard, nil), nil)).Error("boom", "path", "/x/42")
	flush()
	evs := ft.all()
	if len(evs) != 1 || evs[0].Environment != "staging" || evs[0].ServerName != "api" || evs[0].Tags["path"] != "/x/[n]" {
		t.Fatalf("events %+v", evs)
	}
}
