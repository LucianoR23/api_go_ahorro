package audit

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/dates"
	sqlcgen "github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
)

// Worker applies the retention every day (plan 0.17, privacy policy): IPs
// older than 7 days are cleared and events older than 12 months deleted.
type Worker struct {
	q            *sqlcgen.Queries
	hour, minute int
	logger       *slog.Logger
	now          func() time.Time
}

func NewWorker(pool *pgxpool.Pool, hour, minute int, logger *slog.Logger) *Worker {
	return &Worker{q: sqlcgen.New(pool), hour: hour, minute: minute, logger: logger, now: dates.Now}
}

// Start runs once now and then every day at hour:minute (app timezone).
func (w *Worker) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		w.RunOnce(ctx)
		for {
			now := w.now()
			next := time.Date(now.Year(), now.Month(), now.Day(), w.hour, w.minute, 0, 0, now.Location())
			if !next.After(now) {
				next = next.AddDate(0, 0, 1)
			}
			timer := time.NewTimer(next.Sub(now))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				w.RunOnce(ctx)
			}
		}
	}()
	return cancel
}

// RunOnce applies the retention once.
func (w *Worker) RunOnce(ctx context.Context) {
	ips, err := w.q.ClearOldAuditIPs(ctx)
	if err != nil {
		w.logger.WarnContext(ctx, "audit retention: clear IPs failed", "error", err)
	}
	gone, err := w.q.DeleteOldAuditEvents(ctx)
	if err != nil {
		w.logger.WarnContext(ctx, "audit retention: delete old events failed", "error", err)
	}
	w.logger.InfoContext(ctx, "audit retention: ok", "ips_cleared", ips, "events_deleted", gone)
}
