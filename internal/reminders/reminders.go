// Package reminders sends the optional daily logging nudge (plan 10.2):
// at the time each person picked, a push asking whether they spent
// anything today, only to those who loaded nothing that day.
package reminders

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/notifprefs"
	"github.com/LucianoR23/valuo_api/internal/push"
)

// Slot is how often the job runs and the width of each window of reminder
// times it covers.
const Slot = time.Duration(notifprefs.ReminderStep) * time.Minute

// staleAfter: a slot worked this late (a stuck queue) is skipped. A
// reminder an hour late is noise.
const staleAfter = 30 * time.Minute

// Due finds who gets the reminder in a slot.
type Due interface {
	DueReminders(ctx context.Context, from, to notifprefs.TimeOfDay, dayStart time.Time) ([]uuid.UUID, error)
}

// Notifier sends the push; quiet hours drop it (notifprefs.Kind.Expires).
type Notifier interface {
	NotifyUsersNow(ctx context.Context, userIDs []uuid.UUID, payload push.Payload)
}

type Service struct {
	due    Due
	push   Notifier
	loc    *time.Location
	now    func() time.Time
	logger *slog.Logger
}

func NewService(due Due, notifier Notifier, loc *time.Location, now func() time.Time, logger *slog.Logger) *Service {
	return &Service{due: due, push: notifier, loc: loc, now: now, logger: logger}
}

// Payload is the reminder. No amounts, so the lock screen is safe.
var Payload = push.Payload{
	Title: "¿Hoy no gastaste nada?",
	Body:  "Si gastaste algo, cargalo ahora que te acordás.",
	URL:   "/agregar",
	Tag:   "daily-reminder",
	Kind:  notifprefs.DailyReminder,
}

// Run sends the reminders of the slot that starts at slot (app timezone)
// and returns how many people it went to.
func (s *Service) Run(ctx context.Context, slot time.Time) (int, error) {
	slot = slot.In(s.loc)
	if late := s.now().Sub(slot); late > staleAfter {
		s.logger.WarnContext(ctx, "reminders: slot too old, skipped", "slot", slot, "late", late.String())
		return 0, nil
	}
	dayStart := time.Date(slot.Year(), slot.Month(), slot.Day(), 0, 0, 0, 0, s.loc)
	from := notifprefs.TimeOfDay(slot.Hour()*60 + slot.Minute())
	ids, err := s.due.DueReminders(ctx, from, from+notifprefs.ReminderStep, dayStart)
	if err != nil {
		return 0, fmt.Errorf("reminders: %w", err)
	}
	if len(ids) > 0 {
		s.push.NotifyUsersNow(ctx, ids, Payload)
	}
	return len(ids), nil
}
