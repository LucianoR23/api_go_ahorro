package jobs

import (
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/testutil"
)

func TestDailySchedule(t *testing.T) {
	loc := testutil.Location(t)
	at := func(m time.Month, d, h, min int) time.Time { return time.Date(2026, m, d, h, min, 0, 0, loc) }
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 0, 0, 0, 0, time.UTC) }
	s := daily{0, 30}

	tests := []struct {
		name     string
		from     time.Time
		wantNext time.Time
		wantLast time.Time
	}{
		{"before the hour", at(3, 10, 0, 29), at(3, 10, 0, 30), day(3, 9)},
		{"exactly at the hour", at(3, 10, 0, 30), at(3, 11, 0, 30), day(3, 10)},
		{"later that day", at(3, 10, 15, 0), at(3, 11, 0, 30), day(3, 10)},
		{"month end", at(3, 31, 23, 0), at(4, 1, 0, 30), day(3, 31)},
		// 02:00 UTC on the 11th is still 23:00 on the 10th in Buenos Aires.
		{"UTC input uses the app timezone", time.Date(2026, 3, 11, 2, 0, 0, 0, time.UTC), at(3, 11, 0, 30), day(3, 10)},
	}
	for _, tt := range tests {
		if got := s.Next(tt.from); !got.Equal(tt.wantNext) {
			t.Errorf("%s: Next = %s, want %s", tt.name, got, tt.wantNext)
		}
		if got := s.lastDay(tt.from); !got.Equal(tt.wantLast) {
			t.Errorf("%s: lastDay = %s, want %s", tt.name, got, tt.wantLast)
		}
	}
}

func TestMonthlySchedule(t *testing.T) {
	loc := testutil.Location(t)
	at := func(y int, m time.Month, d, h, min int) time.Time { return time.Date(y, m, d, h, min, 0, 0, loc) }
	month := func(y int, m time.Month) time.Time { return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC) }
	s := monthly{8, 0}

	tests := []struct {
		name     string
		from     time.Time
		wantNext time.Time
		wantLast time.Time // month reported by the latest firing
	}{
		{"day 1 before the hour", at(2026, 11, 1, 7, 59), at(2026, 11, 1, 8, 0), month(2026, 9)},
		{"day 1 exactly at the hour", at(2026, 11, 1, 8, 0), at(2026, 12, 1, 8, 0), month(2026, 10)},
		{"mid month", at(2026, 11, 15, 12, 0), at(2026, 12, 1, 8, 0), month(2026, 10)},
		{"December into January", at(2026, 12, 2, 0, 0), at(2027, 1, 1, 8, 0), month(2026, 11)},
		{"January reports December", at(2027, 1, 5, 0, 0), at(2027, 2, 1, 8, 0), month(2026, 12)},
	}
	for _, tt := range tests {
		if got := s.Next(tt.from); !got.Equal(tt.wantNext) {
			t.Errorf("%s: Next = %s, want %s", tt.name, got, tt.wantNext)
		}
		if got := s.lastReported(tt.from); !got.Equal(tt.wantLast) {
			t.Errorf("%s: lastReported = %s, want %s", tt.name, got, tt.wantLast)
		}
	}
}
