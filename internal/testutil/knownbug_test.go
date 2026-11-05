package testutil

import (
	"fmt"
	"testing"
)

type fakeReporter struct {
	logs   []string
	fatals []string
}

func (f *fakeReporter) Helper() {}
func (f *fakeReporter) Logf(format string, a ...any) {
	f.logs = append(f.logs, fmt.Sprintf(format, a...))
}
func (f *fakeReporter) Fatalf(format string, a ...any) {
	f.fatals = append(f.fatals, fmt.Sprintf(format, a...))
}

func TestKnownBug(t *testing.T) {
	tests := []struct {
		name      string
		check     func(t testing.TB)
		wantFatal bool
	}{
		{name: "Errorf: bug reproduces", check: func(t testing.TB) { t.Errorf("wrong") }},
		{name: "Fatalf stops the check", check: func(t testing.TB) {
			t.Fatalf("wrong")
			panic("unreachable: Fatalf must stop the check")
		}},
		{name: "Skip is treated as a failure, not a pass", check: func(t testing.TB) { t.Skip("nope") }},
		{name: "check passes: bug fixed, test must fail", check: func(t testing.TB) {}, wantFatal: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeReporter{}
			knownBug(f, t, "x.y", "demo", tt.check)
			if gotFatal := len(f.fatals) > 0; gotFatal != tt.wantFatal {
				t.Fatalf("fatal = %v, want %v (logs %q, fatals %q)", gotFatal, tt.wantFatal, f.logs, f.fatals)
			}
		})
	}
}
