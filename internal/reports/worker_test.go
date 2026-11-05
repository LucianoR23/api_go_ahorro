package reports

// Schedule tests (plan 0.11): the monthly tick and the clock seam.

import (
	"testing"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
)

func TestNewWorkerDefaults(t *testing.T) {
	w := NewWorker(nil, nil, nil, -1, 99, nil)
	if w.hour != 8 || w.minute != 0 {
		t.Errorf("invalid hour/minute fall back to 08:00, got %02d:%02d", w.hour, w.minute)
	}
	if w.now == nil {
		t.Fatal("NewWorker must set the clock")
	}
}

func TestNextTick(t *testing.T) {
	loc := testutil.Location(t)
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, loc) }
	w := NewWorker(nil, nil, nil, 8, 0, nil)

	tests := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"day 1 before the hour: today", at(2026, 11, 1, 7, 59), at(2026, 11, 1, 8, 0)},
		{"day 1 exactly at the hour: next month", at(2026, 11, 1, 8, 0), at(2026, 12, 1, 8, 0)},
		{"mid month: next month's day 1", at(2026, 11, 15, 12, 0), at(2026, 12, 1, 8, 0)},
		{"December rolls into January", at(2026, 12, 2, 0, 0), at(2027, 1, 1, 8, 0)},
	}
	for _, tt := range tests {
		if got := w.nextTick(tt.from); !got.Equal(tt.want) {
			t.Errorf("%s: nextTick(%s) = %s, want %s", tt.name, tt.from, got, tt.want)
		}
	}
}
