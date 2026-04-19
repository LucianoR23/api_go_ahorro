package recurringexpenses

import (
	"context"
	"log/slog"
	"time"
)

// Worker: genera expenses recurrentes una vez al día a las 00:30 local.
// Idempotente via last_generated. Catch-up al arrancar cubre downtime.
type Worker struct {
	svc    *Service
	hour   int
	minute int
	logger *slog.Logger
}

func NewWorker(svc *Service, hour, minute int, logger *slog.Logger) *Worker {
	if hour < 0 || hour > 23 {
		hour = 0
	}
	if minute < 0 || minute > 59 {
		minute = 30
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
				w.logger.Info("recurring expenses worker: detenido")
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
	// 5min es generoso: si un hogar tiene 50 recurrentes con FX + credit
	// period lookup, cada Create puede tomar ~50ms, total <5s. Pero si
	// hay timeouts de red contra fxrates, mejor dejarlo amplio.
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	today := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())
	start := time.Now()
	created, failed, err := w.svc.GenerateDue(runCtx, today)
	if err != nil {
		w.logger.WarnContext(runCtx, "recurring expenses worker: generateDue falló",
			"error", err, "created", created, "failed", failed,
			"date", today.Format("2006-01-02"))
		return
	}
	w.logger.InfoContext(runCtx, "recurring expenses worker: ok",
		"created", created,
		"failed", failed,
		"date", today.Format("2006-01-02"),
		"took_ms", time.Since(start).Milliseconds())
}
