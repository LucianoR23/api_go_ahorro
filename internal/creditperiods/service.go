package creditperiods

import (
	"context"
	"regexp"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/paymethods"
)

// Service gestiona credit_card_periods. Se apoya en paymethods.Service
// para validar ownership del payment_method y resolver el credit_card_id
// (el endpoint viene parametrizado por paymentMethodId — más fácil para
// el frontend).
type Service struct {
	repo    *Repository
	paymSvc *paymethods.Service
}

func NewService(repo *Repository, paymSvc *paymethods.Service) *Service {
	return &Service{repo: repo, paymSvc: paymSvc}
}

var periodYMRegex = regexp.MustCompile(`^\d{4}-\d{2}$`)

// List retorna todos los períodos cargados de la tarjeta.
func (s *Service) List(ctx context.Context, ownerID, paymentMethodID uuid.UUID) ([]domain.CreditCardPeriod, error) {
	ccID, err := s.paymSvc.RequireCreditCardOwner(ctx, ownerID, paymentMethodID)
	if err != nil {
		return nil, err
	}
	return s.repo.List(ctx, ccID)
}

// UpsertInput: fechas concretas para closing/due. period_ym es el de la URL.
type UpsertInput struct {
	PeriodYM    string
	ClosingDate time.Time
	DueDate     time.Time
}

// Upsert crea o actualiza el período. Validaciones:
//   - period_ym con formato "YYYY-MM"
//   - closing/due no zero
//   - due >= closing
//   - period_ym derivado de closingDate coincide con el de la URL
//     (así evitamos que el cliente cargue "2026-05" con un closingDate de 2026-06).
func (s *Service) Upsert(ctx context.Context, ownerID, paymentMethodID uuid.UUID, in UpsertInput) (domain.CreditCardPeriod, error) {
	if !periodYMRegex.MatchString(in.PeriodYM) {
		return domain.CreditCardPeriod{}, domain.NewValidationError("periodYm", "formato debe ser YYYY-MM")
	}
	if in.ClosingDate.IsZero() {
		return domain.CreditCardPeriod{}, domain.NewValidationError("closingDate", "requerido")
	}
	if in.DueDate.IsZero() {
		return domain.CreditCardPeriod{}, domain.NewValidationError("dueDate", "requerido")
	}
	if in.DueDate.Before(in.ClosingDate) {
		return domain.CreditCardPeriod{}, domain.NewValidationError("dueDate", "debe ser >= closingDate")
	}
	if domain.PeriodYMFromDate(in.ClosingDate) != in.PeriodYM {
		return domain.CreditCardPeriod{}, domain.NewValidationError("closingDate", "no coincide con el periodYm de la URL")
	}

	ccID, err := s.paymSvc.RequireCreditCardOwner(ctx, ownerID, paymentMethodID)
	if err != nil {
		return domain.CreditCardPeriod{}, err
	}
	return s.repo.Upsert(ctx, ccID, in.PeriodYM, in.ClosingDate, in.DueDate)
}

// Delete remueve el override. Si una expense posterior cae en ese mes, el
// resolver usará los defaults de la credit_card.
func (s *Service) Delete(ctx context.Context, ownerID, paymentMethodID uuid.UUID, periodYM string) error {
	if !periodYMRegex.MatchString(periodYM) {
		return domain.NewValidationError("periodYm", "formato debe ser YYYY-MM")
	}
	ccID, err := s.paymSvc.RequireCreditCardOwner(ctx, ownerID, paymentMethodID)
	if err != nil {
		return err
	}
	return s.repo.Delete(ctx, ccID, periodYM)
}

// Status: reporta si el usuario tiene que cargar el próximo período.
//
// Señales:
//   - noPeriodsLoaded: tarjeta sin ningún período cargado todavía
//     (el frontend puede mostrar onboarding).
//   - dueDatePassed: el último período ya venció (today > due_date).
//     El frontend debería pedir closing/due del próximo mes.
//   - latestPeriod: el período más reciente (o null si noPeriodsLoaded).
type Status struct {
	NoPeriodsLoaded bool
	DueDatePassed   bool
	LatestPeriod    *domain.CreditCardPeriod
}

func (s *Service) Status(ctx context.Context, ownerID, paymentMethodID uuid.UUID, now time.Time) (Status, error) {
	ccID, err := s.paymSvc.RequireCreditCardOwner(ctx, ownerID, paymentMethodID)
	if err != nil {
		return Status{}, err
	}
	latest, err := s.repo.GetLatest(ctx, ccID)
	if err != nil {
		if err == domain.ErrNotFound {
			return Status{NoPeriodsLoaded: true}, nil
		}
		return Status{}, err
	}
	today := now.UTC().Truncate(24 * time.Hour)
	return Status{
		DueDatePassed: today.After(latest.DueDate),
		LatestPeriod:  &latest,
	}, nil
}
