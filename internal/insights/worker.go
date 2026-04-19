package insights

import (
	"context"
	"log/slog"
	"time"
)

// Worker: corre una vez al día a las HH:MM local (default 01:00). Idempotente
// via UNIQUE(household, user, date, type) + ON CONFLICT DO NOTHING en Create.
// Catch-up al arrancar: genera para `today` aunque haya pasado el horario.
type Worker struct {
	svc    *Service
	hour   int
	minute int
	logger *slog.Logger
}

func NewWorker(svc *Service, hour, minute int, logger *slog.Logger) *Worker {
	if hour < 0 || hour > 23 {
		hour = 1
	}
	if minute < 0 || minute > 59 {
		minute = 0
	}
	return &Worker{svc: svc, hour: hour, minute: minute, logger: logger}
}

func (w *Worker) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		w.runOnce(ctx, time.Now())

		for {
			now := time.Now()
			next := w.nextTick(now)
			timer := time.NewTimer(next.Sub(now))
			select {
			case <-ctx.Done():
				timer.Stop()
				w.logger.Info("insights worker: detenido")
				return
			case <-timer.C:
				w.runOnce(ctx, time.Now())
			}
		}
	}()

	return cancel
}

func (w *Worker) nextTick(from time.Time) time.Time {
	next := time.Date(from.Year(), from.Month(), from.Day(), w.hour, w.minute, 0, 0, from.Location())
	if !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (w *Worker) runOnce(ctx context.Context, at time.Time) {
	// 10min es generoso pero razonable: generación iterando hogares +
	// goals + insights metadata JSON. A escala 100 hogares tarda <30s.
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	start := time.Now()
	created, failed, err := w.svc.GenerateAll(runCtx, at)
	if err != nil {
		w.logger.WarnContext(runCtx, "insights worker: generateAll falló",
			"error", err, "created", created, "failed", failed)
		return
	}
	w.logger.InfoContext(runCtx, "insights worker: ok",
		"created", created,
		"failed", failed,
		"date", at.Format("2006-01-02"),
		"took_ms", time.Since(start).Milliseconds())
}
