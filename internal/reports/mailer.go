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

// Mailer sends the monthly summary of the previous month to the members of
// every household. The jobs package runs it on day 1 (plan 0.8). Without
// RESEND_API_KEY it logs and sends nothing.
type Mailer struct {
	svc        *Service
	households allHouseholdsLister
	sender     Sender
	brand      string
	logger     *slog.Logger
}

// Sender delivers the monthly email. Production passes the shared Resend
// sender (internal/email).
type Sender interface {
	Configured() bool
	Send(ctx context.Context, to []string, subject, html string) error
}

func NewMailer(svc *Service, households allHouseholdsLister, sender Sender, brand string, logger *slog.Logger) *Mailer {
	return &Mailer{svc: svc, households: households, sender: sender, brand: brand, logger: logger}
}

// SendMonthly sends last month's summary to every household as of "at" (a
// run on 1 May reports April). A household that fails is logged and
// skipped; the error is only for a run that could not start, so a retry
// never mails the households that already got it.
func (w *Mailer) SendMonthly(ctx context.Context, at time.Time) error {
	if !w.sender.Configured() {
		w.logger.InfoContext(ctx, "monthly report: skipped (RESEND_API_KEY not set)")
		return nil
	}
	prevMonth := at.AddDate(0, -1, 0)

	ids, err := w.households.ListAllIDs(ctx)
	if err != nil {
		return err
	}

	sent, failed := 0, 0
	for _, hhID := range ids {
		if err := w.sendForHousehold(ctx, hhID, prevMonth); err != nil {
			failed++
			w.logger.WarnContext(ctx, "monthly report: send failed",
				"householdId", hhID.String(), "error", err)
			continue
		}
		sent++
	}
	w.logger.InfoContext(ctx, "monthly report: done",
		"sent", sent, "failed", failed, "month", prevMonth.Format("2006-01"))
	return nil
}

func (w *Mailer) sendForHousehold(ctx context.Context, householdID uuid.UUID, month time.Time) error {
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

	html, err := RenderMonthlyHTML(w.brand, hh.Name, rep)
	if err != nil {
		return err
	}
	subject := w.brand + " — Resumen " + rep.Month + " · " + hh.Name
	return w.sender.Send(ctx, recipients, subject, html)
}
