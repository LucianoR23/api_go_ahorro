package testutil

import (
	"testing"
	"time"
)

// TZ is the timezone tests run in (plan §0 Testing policy).
const TZ = "America/Argentina/Buenos_Aires"

// Location loads TZ or fails the test.
func Location(t testing.TB) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(TZ)
	if err != nil {
		t.Fatalf("load %s: %v", TZ, err)
	}
	return loc
}

// FixedClock returns a clock that always reports at.
func FixedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}
