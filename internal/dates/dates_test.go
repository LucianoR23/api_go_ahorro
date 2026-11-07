package dates_test

import (
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/dates"
)

func TestDateOf(t *testing.T) {
	ba := dates.Location()
	if ba.String() != dates.DefaultTimezone {
		t.Fatalf("default location = %s", ba)
	}
	for _, tc := range []struct {
		at   time.Time
		want string
	}{
		// 22:00 in Buenos Aires is 01:00 UTC of the next day: still the 30th here.
		{time.Date(2026, 11, 30, 22, 0, 0, 0, ba), "2026-11-30"},
		{time.Date(2026, 12, 1, 1, 0, 0, 0, time.UTC), "2026-11-30"},
		{time.Date(2026, 12, 1, 3, 0, 0, 0, time.UTC), "2026-12-01"},
		{time.Date(2026, 12, 1, 0, 5, 0, 0, ba), "2026-12-01"},
	} {
		got := dates.DateOf(tc.at)
		if got.Format("2006-01-02") != tc.want || got.Location() != time.UTC || got.Hour() != 0 {
			t.Errorf("DateOf(%s) = %s, want %s at midnight UTC", tc.at, got, tc.want)
		}
	}
	if !dates.Today().Equal(dates.DateOf(time.Now())) {
		t.Error("Today and DateOf(now) disagree")
	}
	if dates.Now().Location() != ba {
		t.Errorf("Now is in %s", dates.Now().Location())
	}
}

func TestSetLocation(t *testing.T) {
	ba := dates.Location()
	t.Cleanup(func() { dates.SetLocation(ba) })
	mx, err := time.LoadLocation("America/Mexico_City")
	if err != nil {
		t.Fatal(err)
	}
	dates.SetLocation(mx)
	// 22:00 in Mexico City (UTC-6) is 04:00 UTC next day; still the 30th there.
	if got := dates.DateOf(time.Date(2026, 12, 1, 4, 0, 0, 0, time.UTC)).Format("2006-01-02"); got != "2026-11-30" {
		t.Errorf("Mexico City date = %s", got)
	}
	dates.SetLocation(nil) // ignored
	if dates.Location() != mx {
		t.Error("SetLocation(nil) changed the location")
	}
}
