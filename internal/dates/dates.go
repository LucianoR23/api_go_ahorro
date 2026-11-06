// Package dates is the one place that decides which calendar day it is.
//
// Calendar dates (spent_at, billing and due dates, goal periods, report
// months) are timezone-free: the database stores DATE, and pgx returns it as
// midnight UTC. "Today" depends on where the user is, though: at 22:00 in
// Buenos Aires it is already tomorrow in UTC. So every "today" or "current
// month" goes through here, in the app's timezone (APP_TIMEZONE, default
// America/Argentina/Buenos_Aires), and comes back as midnight UTC of that
// calendar day, comparable with the dates read from the database.
//
// A per-household timezone (for other countries) would replace Location()
// with the household's; see the plan, 0.2.
package dates

import (
	"sync/atomic"
	"time"
	_ "time/tzdata" // the zone database inside the binary: no dependency on the OS
)

// DefaultTimezone is the app timezone when APP_TIMEZONE is not set.
const DefaultTimezone = "America/Argentina/Buenos_Aires"

var current atomic.Pointer[time.Location]

func init() {
	loc, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		// time/tzdata is embedded above, so this cannot fail at runtime.
		panic("dates: load " + DefaultTimezone + ": " + err.Error())
	}
	current.Store(loc)
}

// SetLocation sets the app timezone; called once at startup from config.
func SetLocation(loc *time.Location) {
	if loc != nil {
		current.Store(loc)
	}
}

// Location is the app timezone.
func Location() *time.Location { return current.Load() }

// Now is the current instant in the app timezone (for schedules and logs).
func Now() time.Time { return time.Now().In(Location()) }

// DateOf is the calendar day of instant t in the app timezone, as midnight UTC.
func DateOf(t time.Time) time.Time {
	y, m, d := t.In(Location()).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Today is the current calendar day in the app timezone, as midnight UTC.
func Today() time.Time { return DateOf(time.Now()) }
