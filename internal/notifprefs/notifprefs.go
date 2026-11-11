// Package notifprefs holds what each person wants to hear about (plan 10.1):
// which kinds of notice reach them on which channel, and their quiet hours.
// Preferences belong to the person and apply to every space they are in.
package notifprefs

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Kind is a family of notices the person can mute.
type Kind string

const (
	// SharedExpense: someone added, changed, removed or moved an expense
	// that is split with you.
	SharedExpense Kind = "shared_expense"
	// Settlement: someone settled up with you.
	Settlement Kind = "settlement"
	// Cards: a card closes tomorrow, or its statement is due soon.
	Cards Kind = "cards"
	// Installments: an installment is due in three days.
	Installments Kind = "installments"
	// Goals: a budget or goal is close to its limit.
	Goals Kind = "goals"
	// Recurring: a recurring expense went up more than its alert.
	Recurring Kind = "recurring"
	// WeeklyReview: Sunday's summary of the week.
	WeeklyReview Kind = "weekly_review"
	// MonthlyReport: the email with last month's summary.
	MonthlyReport Kind = "monthly_report"
	// Invite: an invitation to a space. It can't be muted (it is rare and
	// waits for an answer), but quiet hours still hold its push.
	Invite Kind = "invite"
)

// Channel is where a notice reaches the person.
type Channel string

const (
	InApp Channel = "in_app"
	Push  Channel = "push"
	Email Channel = "email"
)

// Entry is a kind and the channels it can be muted on: only the channels
// the API really sends it through.
type Entry struct {
	Kind     Kind
	Channels []Channel
}

// Catalog lists the kinds that can be muted, in the order the app shows them.
var Catalog = []Entry{
	{SharedExpense, []Channel{InApp, Push}},
	{Settlement, []Channel{InApp, Push}},
	{Cards, []Channel{InApp, Push}},
	{Installments, []Channel{InApp, Push}},
	{Goals, []Channel{InApp}},
	{Recurring, []Channel{InApp}},
	{WeeklyReview, []Channel{InApp}},
	{MonthlyReport, []Channel{Email}},
}

// Key is the stored form of a muted kind and channel: "cards.push".
func Key(k Kind, c Channel) string { return string(k) + "." + string(c) }

// validKey reports whether key names a kind and channel of the catalog.
func validKey(key string) bool {
	k, c, ok := strings.Cut(key, ".")
	if !ok {
		return false
	}
	for _, e := range Catalog {
		if string(e.Kind) == k {
			return slices.Contains(e.Channels, Channel(c))
		}
	}
	return false
}

// TimeOfDay is minutes since midnight, 0..1439.
type TimeOfDay int

const minutesPerDay = 24 * 60

// ParseTimeOfDay reads "HH:MM" (24 hours).
func ParseTimeOfDay(s string) (TimeOfDay, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("notifprefs: %q is not HH:MM", s)
	}
	return TimeOfDay(t.Hour()*60 + t.Minute()), nil
}

func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", int(t)/60, int(t)%60) }

// QuietHours is a daily window, in the app timezone, when push notices wait.
// Start after End means the window crosses midnight (23:00 to 08:00).
type QuietHours struct {
	Enabled bool
	Start   TimeOfDay
	End     TimeOfDay
}

// Until returns when the window that holds now ends, or false when now is
// outside the window (or quiet hours are off).
func (q QuietHours) Until(now time.Time, loc *time.Location) (time.Time, bool) {
	if !q.Enabled || q.Start == q.End {
		return time.Time{}, false
	}
	local := now.In(loc)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	m := TimeOfDay(local.Hour()*60 + local.Minute())
	at := func(dayOffset int, t TimeOfDay) time.Time {
		d := midnight.AddDate(0, 0, dayOffset)
		return time.Date(d.Year(), d.Month(), d.Day(), int(t)/60, int(t)%60, 0, 0, loc)
	}
	if q.Start < q.End { // same day: 13:00 to 15:00
		if m >= q.Start && m < q.End {
			return at(0, q.End), true
		}
		return time.Time{}, false
	}
	switch { // crosses midnight: 23:00 to 08:00
	case m >= q.Start:
		return at(1, q.End), true
	case m < q.End:
		return at(0, q.End), true
	}
	return time.Time{}, false
}

// Prefs is one person's choices.
type Prefs struct {
	// Disabled holds the muted Key values.
	Disabled []string
	Quiet    QuietHours
}

// Defaults: everything on, quiet hours from 23:00 to 08:00.
func Defaults() Prefs {
	return Prefs{Disabled: []string{}, Quiet: QuietHours{Enabled: true, Start: 23 * 60, End: 8 * 60}}
}

// Allows reports whether kind reaches the person on channel. Kinds outside
// the catalog (invites) always do.
func (p Prefs) Allows(k Kind, c Channel) bool {
	return !slices.Contains(p.Disabled, Key(k, c))
}
