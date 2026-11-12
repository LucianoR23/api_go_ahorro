package notifprefs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Service reads and stores preferences.
type Service struct {
	q      *sqlcgen.Queries
	logger *slog.Logger
}

func NewService(pool *pgxpool.Pool, logger *slog.Logger) *Service {
	return &Service{q: sqlcgen.New(pool), logger: logger}
}

const microsPerMinute = 60 * 1_000_000

func toTime(t TimeOfDay) pgtype.Time {
	return pgtype.Time{Microseconds: int64(t) * microsPerMinute, Valid: true}
}

func fromTime(t pgtype.Time) TimeOfDay { return TimeOfDay(t.Microseconds / microsPerMinute) }

func fromRow(row sqlcgen.NotificationPreference) Prefs {
	disabled := row.Disabled
	if disabled == nil {
		disabled = []string{}
	}
	return Prefs{
		Disabled: disabled,
		Quiet:    QuietHours{Enabled: row.QuietEnabled, Start: fromTime(row.QuietStart), End: fromTime(row.QuietEnd)},
		Reminder: Reminder{Enabled: row.ReminderEnabled, At: fromTime(row.ReminderAt)},
	}
}

// Get returns the person's preferences, or the defaults if they never saved any.
func (s *Service) Get(ctx context.Context, userID uuid.UUID) (Prefs, error) {
	row, err := s.q.GetNotificationPreferences(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Defaults(), nil
	}
	if err != nil {
		return Prefs{}, fmt.Errorf("notifprefs.Get: %w", err)
	}
	return fromRow(row), nil
}

// ForDelivery is Get for the senders: a failed read is logged and the
// defaults apply, so a database hiccup never drops a notice.
func (s *Service) ForDelivery(ctx context.Context, userID uuid.UUID) Prefs {
	p, err := s.Get(ctx, userID)
	if err != nil {
		s.logger.WarnContext(ctx, "notifprefs: read failed, using defaults", "user_id", userID, "error", err)
		return Defaults()
	}
	return p
}

// Save validates and stores p, and returns what was stored.
func (s *Service) Save(ctx context.Context, userID uuid.UUID, p Prefs) (Prefs, error) {
	for _, key := range p.Disabled {
		if !validKey(key) {
			return Prefs{}, domain.NewValidationError("kinds", "tipo o canal desconocido: "+key)
		}
	}
	if p.Quiet.Start < 0 || p.Quiet.Start >= minutesPerDay || p.Quiet.End < 0 || p.Quiet.End >= minutesPerDay {
		return Prefs{}, domain.NewValidationError("quietHours", "horario inválido")
	}
	if p.Quiet.Start == p.Quiet.End {
		return Prefs{}, domain.NewValidationError("quietHours.end", "tiene que ser distinto del inicio")
	}
	if p.Reminder.At < 0 || p.Reminder.At >= minutesPerDay || p.Reminder.At%ReminderStep != 0 {
		return Prefs{}, domain.NewValidationError("dailyReminder.at", "tiene que ser una hora en punto, y cuarto, y media o menos cuarto")
	}
	disabled := slices.Clone(p.Disabled)
	slices.Sort(disabled)
	disabled = slices.Compact(disabled)
	if disabled == nil {
		disabled = []string{}
	}
	row, err := s.q.UpsertNotificationPreferences(ctx, sqlcgen.UpsertNotificationPreferencesParams{
		UserID:          userID,
		Disabled:        disabled,
		QuietEnabled:    p.Quiet.Enabled,
		QuietStart:      toTime(p.Quiet.Start),
		QuietEnd:        toTime(p.Quiet.End),
		ReminderEnabled: p.Reminder.Enabled,
		ReminderAt:      toTime(p.Reminder.At),
	})
	if err != nil {
		return Prefs{}, fmt.Errorf("notifprefs.Save: %w", err)
	}
	return fromRow(row), nil
}

// DueReminders lists who gets the daily reminder in the slot [from, to)
// of the day that starts at dayStart: reminder on, set in the slot, and
// nothing loaded since dayStart (plan 10.2).
func (s *Service) DueReminders(ctx context.Context, from, to TimeOfDay, dayStart time.Time) ([]uuid.UUID, error) {
	ids, err := s.q.DueDailyReminders(ctx, sqlcgen.DueDailyRemindersParams{
		FromTime: toTime(from),
		ToTime:   toTime(to),
		DayStart: pgtype.Timestamptz{Time: dayStart, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("notifprefs.DueReminders: %w", err)
	}
	return ids, nil
}
