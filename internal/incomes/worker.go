package incomes

import (
	"context"
	"log/slog"
	"time"
)

// Worker: genera incomes recurrentes una vez al día a las 00:30 local.
// Idempotente gracias a last_generated en la tabla, así que si el server
// reinicia a las 00:45 y el worker ya había corrido, no duplica nada.
//
// Además corre una vez al arrancar: cubre el caso de que el server haya
// estado caído durante la ventana diaria — GenerateDue solo creará lo que
// falta porque los templates con last_generated == today se skipean.
type Worker struct {
	svc    *Service
	hour   int
	minute int
	logger *slog.Logger
}

// NewWorker: hour/minute en hora local del server. Defaults 00:30 si se pasa inválido.
func NewWorker(svc *Service, hour, minute int, logger *slog.Logger) *Worker {
	if hour < 0 || hour > 23 {
		hour = 0
	}
	if minute < 0 || minute > 59 {
		minute = 30
	}
	return &Worker{svc: svc, hour: hour, minute: minute, logger: logger}
}

// Start arranca el loop y devuelve un cancel para shutdown.
// Tick inicial inmediato (catch-up), después sleep hasta el próximo 00:30.
func (w *Worker) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		w.runOnce(ctx, time.Now())

		for {
			now := time.Now()
			next := w.nextTick(now)
			d := next.Sub(now)

			timer := time.NewTimer(d)
			select {
			case <-ctx.Done():
				timer.Stop()
				w.logger.Info("incomes worker: detenido")
				return
			case <-timer.C:
				w.runOnce(ctx, time.Now())
			}
		}
	}()

	return cancel
}

// nextTick calcula el próximo disparo (hh:mm de hoy, o de mañana si ya pasó).
func (w *Worker) nextTick(from time.Time) time.Time {
	next := time.Date(from.Year(), from.Month(), from.Day(), w.hour, w.minute, 0, 0, from.Location())
	if !next.After(from) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (w *Worker) runOnce(ctx context.Context, at time.Time) {
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	// La fecha para GenerateDue es el día calendario en local del server.
	today := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, at.Location())

	start := time.Now()
	created, err := w.svc.GenerateDue(runCtx, today)
	if err != nil {
		w.logger.WarnContext(runCtx, "incomes worker: generateDue falló",
			"error", err, "created", created, "date", today.Format("2006-01-02"))
		return
	}
	w.logger.InfoContext(runCtx, "incomes worker: ok",
		"created", created,
		"date", today.Format("2006-01-02"),
		"took_ms", time.Since(start).Milliseconds())
}
