package reports

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// allHouseholdsLister: necesitamos iterar todos los hogares para el envío.
type allHouseholdsLister interface {
	ListAllIDs(ctx context.Context) ([]uuid.UUID, error)
	ListMembers(ctx context.Context, householdID uuid.UUID) ([]domain.HouseholdMemberDetail, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.Household, error)
}

// Worker: corre el día 1 de cada mes a las HH:MM local (default 08:00).
// Genera el reporte del mes anterior y lo manda a los miembros del hogar.
// Si RESEND_API_KEY no está, loguea y no envía (el worker sigue corriendo).
type Worker struct {
	svc        *Service
	households allHouseholdsLister
	sender     *ResendSender
	hour       int
	minute     int
	logger     *slog.Logger
}

func NewWorker(svc *Service, households allHouseholdsLister, sender *ResendSender, hour, minute int, logger *slog.Logger) *Worker {
	if hour < 0 || hour > 23 {
		hour = 8
	}
	if minute < 0 || minute > 59 {
		minute = 0
	}
	return &Worker{svc: svc, households: households, sender: sender, hour: hour, minute: minute, logger: logger}
}

func (w *Worker) Start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)

	go func() {
		for {
			now := time.Now()
			next := w.nextTick(now)
			timer := time.NewTimer(next.Sub(now))
			select {
			case <-ctx.Done():
				timer.Stop()
				w.logger.Info("reports worker: detenido")
				return
			case <-timer.C:
				w.runOnce(ctx, time.Now())
			}
		}
	}()

	return cancel
}

// nextTick: próximo "día 1 HH:MM" estrictamente después de from. Si hoy
// es día 1 y aún no pasó la hora, ese es el siguiente tick.
func (w *Worker) nextTick(from time.Time) time.Time {
	candidate := time.Date(from.Year(), from.Month(), 1, w.hour, w.minute, 0, 0, from.Location())
	if !candidate.After(from) {
		candidate = candidate.AddDate(0, 1, 0)
	}
	return candidate
}

func (w *Worker) runOnce(ctx context.Context, at time.Time) {
	if !w.sender.Configured() {
		w.logger.InfoContext(ctx, "reports worker: skip (RESEND_API_KEY no configurada)")
		return
	}

	// Mes anterior: si corre el 1-may, reporta abril.
	prevMonth := at.AddDate(0, -1, 0)

	runCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	ids, err := w.households.ListAllIDs(runCtx)
	if err != nil {
		w.logger.ErrorContext(runCtx, "reports worker: listAllIDs falló", "error", err)
		return
	}

	sent, failed := 0, 0
	for _, hhID := range ids {
		if err := w.sendForHousehold(runCtx, hhID, prevMonth); err != nil {
			failed++
			w.logger.WarnContext(runCtx, "reports worker: envío falló",
				"householdId", hhID.String(), "error", err)
			continue
		}
		sent++
	}
	w.logger.InfoContext(runCtx, "reports worker: ok",
		"sent", sent, "failed", failed, "month", prevMonth.Format("2006-01"))
}

func (w *Worker) sendForHousehold(ctx context.Context, householdID uuid.UUID, month time.Time) error {
	rep, err := w.svc.Monthly(ctx, householdID, month)
	if err != nil {
		return err
	}
	hh, err := w.households.GetByID(ctx, householdID)
	if err != nil {
		return err
	}
	members, err := w.households.ListMembers(ctx, householdID)
	if err != nil {
		return err
	}
	recipients := make([]string, 0, len(members))
	for _, m := range members {
		if m.User.Email != "" {
			recipients = append(recipients, m.User.Email)
		}
	}
	if len(recipients) == 0 {
		return nil // nada que enviar
	}

	html, err := RenderMonthlyHTML(hh.Name, rep)
	if err != nil {
		return err
	}
	subject := "Ahorra — Resumen " + rep.Month + " · " + hh.Name
	return w.sender.Send(ctx, recipients, subject, html)
}
