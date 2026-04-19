package fxrates

import (
	"context"
	"log/slog"
	"time"
)

// Worker corre Refresh cada `interval`. Se arranca en main con Start()
// que devuelve un cancel para el shutdown limpio.
type Worker struct {
	svc      *Service
	interval time.Duration
	logger   *slog.Logger
}

func NewWorker(svc *Service, interval time.Duration, logger *slog.Logger) *Worker {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	return &Worker{svc: svc, interval: interval, logger: logger}
}

// Start arranca el loop en una goroutine y devuelve una func para frenarlo.
// La primera ejecución es inmediata (para poblar caché si DB estaba vacía);
// las siguientes cada `interval`.
func (w *Worker) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		// Fetch inicial con timeout propio, sin bloquear el arranque.
		w.runOnce(ctx)

		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				w.logger.Info("fxrates worker: detenido")
				return
			case <-ticker.C:
				w.runOnce(ctx)
			}
		}
	}()

	return cancel
}

func (w *Worker) runOnce(ctx context.Context) {
	// Cada corrida usa un timeout acotado para no colgar si bluelytics tarda.
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	start := time.Now()
	if err := w.svc.Refresh(runCtx); err != nil {
		w.logger.WarnContext(runCtx, "fxrates worker: refresh falló", "error", err)
		return
	}
	w.logger.InfoContext(runCtx, "fxrates worker: refresh ok", "took_ms", time.Since(start).Milliseconds())
}
