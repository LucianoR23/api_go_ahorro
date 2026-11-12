package notifprefs

import (
	"testing"
	"time"
)

func TestQuietHoursUntil(t *testing.T) {
	t.Parallel()
	ba, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatal(err)
	}
	at := func(day, hh, mm int) time.Time { return time.Date(2026, 11, day, hh, mm, 0, 0, ba) }
	night := QuietHours{Enabled: true, Start: 23 * 60, End: 8 * 60}
	nap := QuietHours{Enabled: true, Start: 13 * 60, End: 15*60 + 30}

	cases := []struct {
		name  string
		q     QuietHours
		now   time.Time
		want  time.Time
		quiet bool
	}{
		{"before midnight waits for tomorrow morning", night, at(11, 23, 30), at(12, 8, 0), true},
		{"the start minute is already quiet", night, at(11, 23, 0), at(12, 8, 0), true},
		{"after midnight waits for this morning", night, at(12, 2, 0), at(12, 8, 0), true},
		{"the end minute is no longer quiet", night, at(12, 8, 0), time.Time{}, false},
		{"the afternoon is not quiet", night, at(12, 15, 0), time.Time{}, false},
		{"a window inside the day", nap, at(12, 14, 0), at(12, 15, 30), true},
		{"outside a window inside the day", nap, at(12, 16, 0), time.Time{}, false},
		{"turned off", QuietHours{Start: 23 * 60, End: 8 * 60}, at(12, 2, 0), time.Time{}, false},
		// 05:00 UTC is 02:00 in Buenos Aires: the window is read in loc.
		{"read in the app timezone", night, time.Date(2026, 11, 12, 5, 0, 0, 0, time.UTC), at(12, 8, 0), true},
	}
	for _, c := range cases {
		got, quiet := c.q.Until(c.now, ba)
		if quiet != c.quiet || !got.Equal(c.want) {
			t.Errorf("%s: Until = %v %v, want %v %v", c.name, got, quiet, c.want, c.quiet)
		}
	}
}

func TestTimeOfDay(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]TimeOfDay{"00:00": 0, "08:05": 485, "23:59": 1439} {
		got, err := ParseTimeOfDay(in)
		if err != nil || got != want || got.String() != in {
			t.Errorf("ParseTimeOfDay(%q) = %d (%s), %v", in, got, got, err)
		}
	}
	for _, bad := range []string{"", "24:00", "8:00pm", "12:60", "1200"} {
		if _, err := ParseTimeOfDay(bad); err == nil {
			t.Errorf("ParseTimeOfDay(%q) did not fail", bad)
		}
	}
}

func TestCatalogKeys(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"shared_expense.push", "cards.in_app", "monthly_report.email", "weekly_review.in_app", "weekly_review.push", "monthly_report.in_app"} {
		if !validKey(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	// Only channels the API really sends a kind through, and invites never.
	for _, bad := range []string{"shared_expense.email", "monthly_report.push", "goals.push", "invite.push", "cards", "nope.push", ""} {
		if validKey(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
	p := Prefs{Disabled: []string{"cards.push"}}
	if p.Allows(Cards, Push) || !p.Allows(Cards, InApp) || !p.Allows(Invite, Push) {
		t.Errorf("Allows does not follow Disabled")
	}
}
