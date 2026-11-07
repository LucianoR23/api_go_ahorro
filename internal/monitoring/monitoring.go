package monitoring

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
)

// Options configures Init. An empty DSN turns reporting off.
type Options struct {
	DSN         string
	Environment string
	// Transport replaces the HTTP transport (tests).
	Transport sentry.Transport
}

// Init sets up the global Sentry client. It returns a flush function to
// call before the process exits, so queued events are sent. With an empty
// DSN it does nothing and returns a no-op flush.
func Init(o Options) (flush func(), err error) {
	if o.DSN == "" {
		return func() {}, nil
	}
	err = sentry.Init(sentry.ClientOptions{
		Dsn:         o.DSN,
		Environment: o.Environment,
		ServerName:  "api",
		// Breadcrumbs could carry anything a dependency logs; events carry
		// only what the slog bridge builds.
		MaxBreadcrumbs: -1,
		BeforeSend:     beforeSend,
		Transport:      o.Transport,
	})
	if err != nil {
		return func() {}, fmt.Errorf("monitoring: %w", err)
	}
	return func() { sentry.Flush(2 * time.Second) }, nil
}

// keptContexts are the event contexts that describe the runtime, not a user.
var keptContexts = map[string]bool{"os": true, "runtime": true, "device": true, "trace": true}

// beforeSend is the last filter before an event leaves the process,
// whoever captured it: it drops request, user and free-form data and
// scrubs every remaining string.
func beforeSend(ev *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	ev.Request = nil
	ev.User = sentry.User{}
	ev.Breadcrumbs = nil
	ev.ServerName = "api"
	for k := range ev.Contexts {
		if !keptContexts[k] {
			delete(ev.Contexts, k)
		}
	}
	ev.Message = Scrub(ev.Message)
	for i := range ev.Exception {
		ev.Exception[i].Type = Scrub(ev.Exception[i].Type)
		ev.Exception[i].Value = Scrub(ev.Exception[i].Value)
	}
	for k, v := range ev.Tags {
		ev.Tags[k] = Scrub(v)
	}
	return ev
}

// tagKeys are the log attributes copied to events as tags. Anything else a
// log line carries (user IDs, amounts, e-mails) stays out of the event.
var tagKeys = map[string]bool{
	"method": true,
	"path":   true,
	"kind":   true,
	"job":    true,
	"queue":  true,
}

// Handler wraps a slog handler: records at ERROR or above are also sent to
// GlitchTip. It holds the hub it reports to; nil means sentry.CurrentHub().
type Handler struct {
	next  slog.Handler
	hub   *sentry.Hub
	attrs []slog.Attr
}

// NewHandler returns a Handler that logs through next and reports errors.
func NewHandler(next slog.Handler, hub *sentry.Hub) *Handler {
	return &Handler{next: next, hub: hub}
}

// Enabled reports whether next logs the level; errors are always reported.
func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= slog.LevelError || h.next.Enabled(ctx, l)
}

// Handle logs the record and, at ERROR or above, reports it.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelError {
		h.report(r)
	}
	if !h.next.Enabled(ctx, r.Level) {
		return nil
	}
	return h.next.Handle(ctx, r)
}

// WithAttrs keeps the attributes for both the log and the event.
func (h *Handler) WithAttrs(as []slog.Attr) slog.Handler {
	return &Handler{next: h.next.WithAttrs(as), hub: h.hub, attrs: append(h.attrs[:len(h.attrs):len(h.attrs)], as...)}
}

// WithGroup groups the log attributes. Grouped attributes are not tags.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{next: h.next.WithGroup(name), hub: h.hub}
}

func (h *Handler) report(r slog.Record) {
	hub := h.hub
	if hub == nil {
		hub = sentry.CurrentHub()
	}
	if hub.Client() == nil {
		return
	}
	ev := sentry.NewEvent()
	ev.Level = sentry.LevelError
	if r.Level > slog.LevelError {
		ev.Level = sentry.LevelFatal
	}
	ev.Message = r.Message
	ev.Logger = "slog"
	ev.Tags = map[string]string{}

	var cause error
	visit := func(a slog.Attr) {
		v := a.Value.Resolve()
		switch {
		case a.Key == "error" && v.Kind() == slog.KindAny:
			if err, ok := v.Any().(error); ok {
				cause = err
			}
		case tagKeys[a.Key] && v.Kind() != slog.KindGroup:
			ev.Tags[a.Key] = v.String()
		}
	}
	for _, a := range h.attrs {
		visit(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		visit(a)
		return true
	})

	if cause != nil {
		ev.SetException(cause, 10)
	} else {
		ev.Exception = []sentry.Exception{{Type: r.Message, Value: r.Message}}
	}
	// The error chain rarely carries a stack: the place it was logged is
	// the most useful one we have.
	last := &ev.Exception[len(ev.Exception)-1]
	// The issue title is "type: value". Wrapped errors are *fmt.wrapError,
	// which says nothing; the log message says what failed.
	last.Type = r.Message
	if last.Stacktrace == nil {
		last.Stacktrace = sentry.NewStacktrace()
	}
	hub.CaptureEvent(ev)
}
