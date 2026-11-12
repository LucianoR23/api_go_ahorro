package jobs

import (
	"time"

	"github.com/LucianoR23/valuo_api/internal/dates"
)

// daily fires every day at hour:minute in the app timezone.
type daily struct{ hour, minute int }

func (s daily) at(t time.Time) time.Time {
	t = t.In(dates.Location())
	return time.Date(t.Year(), t.Month(), t.Day(), s.hour, s.minute, 0, 0, t.Location())
}

// Next is the first firing strictly after t (river.PeriodicSchedule).
func (s daily) Next(t time.Time) time.Time {
	n := s.at(t)
	if !n.After(t) {
		n = n.AddDate(0, 0, 1)
	}
	return n
}

// lastDay is the calendar day (midnight UTC, as dates.DateOf) of the latest
// firing at or before t.
func (s daily) lastDay(t time.Time) time.Time {
	p := s.at(t)
	if p.After(t) {
		p = p.AddDate(0, 0, -1)
	}
	return dates.DateOf(p)
}

// monthly fires on day 1 of every month at hour:minute in the app timezone.
type monthly struct{ hour, minute int }

func (s monthly) at(t time.Time) time.Time {
	t = t.In(dates.Location())
	return time.Date(t.Year(), t.Month(), 1, s.hour, s.minute, 0, 0, t.Location())
}

func (s monthly) Next(t time.Time) time.Time {
	n := s.at(t)
	if !n.After(t) {
		n = n.AddDate(0, 1, 0)
	}
	return n
}

// lastReported is the month (day 1, midnight UTC) that the latest firing at
// or before t reports on: a run on 1 May reports April.
func (s monthly) lastReported(t time.Time) time.Time {
	p := s.at(t)
	if p.After(t) {
		p = p.AddDate(0, -1, 0)
	}
	return time.Date(p.Year(), p.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
}

// every fires at each multiple of step since midnight in the app timezone
// (every 15 minutes: 00:00, 00:15, ...). step must divide a day.
type every struct{ step time.Duration }

// last is the latest firing at or before t.
func (s every) last(t time.Time) time.Time {
	t = t.In(dates.Location())
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	return midnight.Add(t.Sub(midnight).Truncate(s.step))
}

// Next is the first firing strictly after t (river.PeriodicSchedule).
func (s every) Next(t time.Time) time.Time { return s.last(t).Add(s.step) }
