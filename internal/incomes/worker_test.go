package incomes

// Schedule tests (plan 0.11): the daily tick and the clock seam.

import (
	"testing"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
)

func TestNewWorkerDefaults(t *testing.T) {
	w := NewWorker(nil, -1, 99, nil)
	if w.hour != 0 || w.minute != 30 {
		t.Errorf("invalid hour/minute fall back to %02d:%02d, got %02d:%02d", 0, 30, w.hour, w.minute)
	}
	if w.now == nil {
		t.Fatal("NewWorker must set the clock")
	}
	if NewWorker(nil, 5, 15, nil).hour != 5 {
		t.Error("valid hour must be kept")
	}
}

func TestNextTick(t *testing.T) {
	loc := testutil.Location(t)
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, loc) }
	w := NewWorker(nil, 0, 30, nil)
	w.now = testutil.FixedClock(at(2026, 11, 4, 0, 10))

	tests := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"before the hour: today", w.now(), at(2026, 11, 4, 0, 30)},
		{"exactly at the hour: tomorrow", at(2026, 11, 4, 0, 30), at(2026, 11, 5, 0, 30)},
		{"after the hour: tomorrow", at(2026, 11, 4, 23, 0), at(2026, 11, 5, 0, 30)},
		{"end of month rolls over", at(2026, 11, 30, 12, 0), at(2026, 12, 1, 0, 30)},
		{"end of year rolls over", at(2026, 12, 31, 12, 0), at(2027, 1, 1, 0, 30)},
	}
	for _, tt := range tests {
		if got := w.nextTick(tt.from); !got.Equal(tt.want) {
			t.Errorf("%s: nextTick(%s) = %s, want %s", tt.name, tt.from, got, tt.want)
		}
	}
}
