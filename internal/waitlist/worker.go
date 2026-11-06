package waitlist

import (
	"context"
	"log/slog"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/dates"
)

// Worker deletes unconfirmed sign-ups older than 30 days, once a day
// (contract section 5).
type Worker struct {
	svc          *Service
	hour, minute int
	logger       *slog.Logger
}

func NewWorker(svc *Service, hour, minute int, logger *slog.Logger) *Worker {
	return &Worker{svc: svc, hour: hour, minute: minute, logger: logger}
}

func (w *Worker) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		w.runOnce(ctx)
		for {
			now := dates.Now()
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
				w.runOnce(ctx)
			}
		}
	}()
	return cancel
}

func (w *Worker) runOnce(ctx context.Context) {
	n, err := w.svc.Cleanup(ctx)
	if err != nil {
		w.logger.WarnContext(ctx, "waitlist cleanup failed", "error", err)
		return
	}
	w.logger.InfoContext(ctx, "waitlist cleanup: ok", "deleted", n)
}
