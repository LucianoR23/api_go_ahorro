// Package testutil holds helpers shared by tests. Never import it from
// production code.
package testutil

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// KnownBug runs check, which asserts the CORRECT behavior of code that has a
// known bug (from the audit), and inverts the outcome:
//
//   - check fails  → the bug still reproduces: the test passes and logs it.
//   - check passes → the bug is gone: the test FAILS, so whoever fixed it
//     (the plan item in `item`) must turn this into a regular test.
//
// Go has no xfail; this keeps known bugs visible in CI without making it red.
func KnownBug(t *testing.T, item, description string, check func(t testing.TB)) {
	t.Helper()
	knownBug(t, t, item, description, check)
}

// reporter is the part of *testing.T that knownBug reports to; a fake in
// tests lets us observe the "bug fixed" failure without failing for real.
type reporter interface {
	Helper()
	Logf(format string, args ...any)
	Fatalf(format string, args ...any)
}

func knownBug(out reporter, base testing.TB, item, description string, check func(t testing.TB)) {
	out.Helper()
	rec := &recorder{TB: base}

	done := make(chan struct{})
	go func() {
		defer close(done)
		check(rec)
	}()
	<-done

	if rec.isFailed() {
		out.Logf("known bug [%s] still reproduces: %s\n%s", item, description, rec.messages())
		return
	}
	out.Fatalf("known bug [%s] no longer reproduces (%s): turn this KnownBug into a regular test", item, description)
}

// recorder captures failures instead of reporting them to the real test.
// Everything else (Helper, Name, TempDir, Cleanup...) is delegated.
type recorder struct {
	testing.TB

	mu     sync.Mutex
	failed bool
	msgs   []string
}

func (r *recorder) record(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = true
	if msg != "" {
		r.msgs = append(r.msgs, msg)
	}
}

func (r *recorder) isFailed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}

func (r *recorder) messages() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, m := range r.msgs {
		b.WriteString("    " + m + "\n")
	}
	return b.String()
}

func (r *recorder) Fail()                     { r.record("") }
func (r *recorder) Failed() bool              { return r.isFailed() }
func (r *recorder) Error(args ...any)         { r.record(fmt.Sprint(args...)) }
func (r *recorder) Errorf(f string, a ...any) { r.record(fmt.Sprintf(f, a...)) }

// FailNow / Fatal stop check like the real ones do: they end its goroutine.
func (r *recorder) FailNow()                  { r.record(""); runtime.Goexit() }
func (r *recorder) Fatal(args ...any)         { r.record(fmt.Sprint(args...)); runtime.Goexit() }
func (r *recorder) Fatalf(f string, a ...any) { r.record(fmt.Sprintf(f, a...)); runtime.Goexit() }

// Skip inside check is a misuse: a known bug must be asserted, not skipped.
func (r *recorder) Skip(args ...any)         { r.Fatal("KnownBug check called Skip:", fmt.Sprint(args...)) }
func (r *recorder) Skipf(f string, a ...any) { r.Fatalf("KnownBug check called Skipf: "+f, a...) }
func (r *recorder) SkipNow()                 { r.Fatal("KnownBug check called SkipNow") }
