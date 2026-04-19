package recurringexpenses

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/expenses"
)

// householdLookup: para validar que el creator sea miembro del hogar.
type householdLookup interface {
	IsMember(ctx context.Context, householdID, userID uuid.UUID) (bool, error)
}

// expensesCreator: la plantilla no materializa expenses sola — delega en el
// service de expenses (cuotas, shares, FX, credit_card_periods). Así todo
// el flujo de creación queda centralizado y no se duplica la lógica.
type expensesCreator interface {
	Create(ctx context.Context, in expenses.CreateInput) (domain.ExpenseDetail, error)
}

type Service struct {
	repo       *Repository
	households householdLookup
	expenses   expensesCreator
	logger     *slog.Logger
}

func NewService(repo *Repository, households householdLookup, expenses expensesCreator, logger *slog.Logger) *Service {
	return &Service{repo: repo, households: households, expenses: expenses, logger: logger}
}

// ===================== CRUD =====================

type CreateInput struct {
	HouseholdID     uuid.UUID
	CreatedBy       uuid.UUID
	CategoryID      *uuid.UUID
	PaymentMethodID uuid.UUID
	Amount          float64
	Currency        string
	Description     string
	Installments    int
	IsShared        bool
	Frequency       string
	DayOfMonth      *int
	DayOfWeek       *int
	MonthOfYear     *int
	StartsAt        time.Time
	EndsAt          *time.Time
}

func (s *Service) Create(ctx context.Context, in CreateInput) (domain.RecurringExpense, error) {
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Frequency = strings.ToLower(strings.TrimSpace(in.Frequency))
	in.Description = strings.TrimSpace(in.Description)

	if in.Amount <= 0 {
		return domain.RecurringExpense{}, domain.NewValidationError("amount", "debe ser mayor a cero")
	}
	if in.Description == "" {
		return domain.RecurringExpense{}, domain.NewValidationError("description", "no puede estar vacío")
	}
	if in.Installments < 1 || in.Installments > 60 {
		return domain.RecurringExpense{}, domain.NewValidationError("installments", "debe estar entre 1 y 60")
	}
	if err := validateRecurrence(in.Frequency, in.DayOfMonth, in.DayOfWeek, in.MonthOfYear); err != nil {
		return domain.RecurringExpense{}, err
	}
	ok, err := s.households.IsMember(ctx, in.HouseholdID, in.CreatedBy)
	if err != nil {
		return domain.RecurringExpense{}, err
	}
	if !ok {
		return domain.RecurringExpense{}, domain.NewValidationError("createdBy", "no es miembro del hogar")
	}
	if in.StartsAt.IsZero() {
		in.StartsAt = time.Now()
	}
	return s.repo.Create(ctx, CreateParams{
		HouseholdID:     in.HouseholdID,
		CreatedBy:       in.CreatedBy,
		CategoryID:      in.CategoryID,
		PaymentMethodID: in.PaymentMethodID,
		Amount:          in.Amount,
		Currency:        in.Currency,
		Description:     in.Description,
		Installments:    in.Installments,
		IsShared:        in.IsShared,
		Frequency:       in.Frequency,
		DayOfMonth:      in.DayOfMonth,
		DayOfWeek:       in.DayOfWeek,
		MonthOfYear:     in.MonthOfYear,
		IsActive:        true,
		StartsAt:        in.StartsAt,
		EndsAt:          in.EndsAt,
	})
}

func (s *Service) Get(ctx context.Context, householdID, id uuid.UUID) (domain.RecurringExpense, error) {
	re, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.RecurringExpense{}, err
	}
	if re.HouseholdID != householdID {
		return domain.RecurringExpense{}, domain.ErrNotFound
	}
	return re, nil
}

func (s *Service) List(ctx context.Context, householdID uuid.UUID) ([]domain.RecurringExpense, error) {
	return s.repo.ListByHousehold(ctx, householdID)
}

type UpdateInput struct {
	Amount          float64
	Currency        string
	Description     string
	Installments    int
	IsShared        bool
	Frequency       string
	DayOfMonth      *int
	DayOfWeek       *int
	MonthOfYear     *int
	EndsAt          *time.Time
	CategoryID      *uuid.UUID
	PaymentMethodID uuid.UUID
}

func (s *Service) Update(ctx context.Context, householdID, id uuid.UUID, in UpdateInput) (domain.RecurringExpense, error) {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.RecurringExpense{}, err
	}
	if existing.HouseholdID != householdID {
		return domain.RecurringExpense{}, domain.ErrNotFound
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Frequency = strings.ToLower(strings.TrimSpace(in.Frequency))
	in.Description = strings.TrimSpace(in.Description)

	if in.Amount <= 0 {
		return domain.RecurringExpense{}, domain.NewValidationError("amount", "debe ser mayor a cero")
	}
	if in.Description == "" {
		return domain.RecurringExpense{}, domain.NewValidationError("description", "no puede estar vacío")
	}
	if in.Installments < 1 || in.Installments > 60 {
		return domain.RecurringExpense{}, domain.NewValidationError("installments", "debe estar entre 1 y 60")
	}
	if err := validateRecurrence(in.Frequency, in.DayOfMonth, in.DayOfWeek, in.MonthOfYear); err != nil {
		return domain.RecurringExpense{}, err
	}
	return s.repo.Update(ctx, id, UpdateParams{
		Amount:          in.Amount,
		Currency:        in.Currency,
		Description:     in.Description,
		Installments:    in.Installments,
		IsShared:        in.IsShared,
		Frequency:       in.Frequency,
		DayOfMonth:      in.DayOfMonth,
		DayOfWeek:       in.DayOfWeek,
		MonthOfYear:     in.MonthOfYear,
		EndsAt:          in.EndsAt,
		CategoryID:      in.CategoryID,
		PaymentMethodID: in.PaymentMethodID,
	})
}

func (s *Service) SetActive(ctx context.Context, householdID, id uuid.UUID, active bool) error {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return s.repo.SetActive(ctx, id, active)
}

func (s *Service) Delete(ctx context.Context, householdID, id uuid.UUID) error {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return s.repo.Delete(ctx, id)
}

// ===================== worker API =====================

// GenerateDue: lo llama el worker. Itera plantillas activas cuyo calendario
// toca `date`, llama a expenses.Service.Create (que resuelve cuotas +
// shares + FX + credit_card_periods por su cuenta), y marca last_generated.
//
// Idempotente: si last_generated == date se skipea. Si Create falla para
// una plantilla, se loguea y se sigue con las demás — no queremos que un
// payment_method roto frene todas las demás.
//
// Devuelve (creadas, saltadas_por_error). El caller loguea totales.
func (s *Service) GenerateDue(ctx context.Context, date time.Time) (int, int, error) {
	templates, err := s.repo.ListActive(ctx, date)
	if err != nil {
		return 0, 0, err
	}
	created, failed := 0, 0
	for _, t := range templates {
		if t.LastGenerated != nil && sameDay(*t.LastGenerated, date) {
			continue
		}
		if !recurrenceMatches(t, date) {
			continue
		}
		_, err := s.expenses.Create(ctx, expenses.CreateInput{
			HouseholdID:     t.HouseholdID,
			CreatedBy:       t.CreatedBy,
			CategoryID:      t.CategoryID,
			PaymentMethodID: t.PaymentMethodID,
			Amount:          t.Amount,
			Currency:        t.Currency,
			Description:     t.Description,
			SpentAt:         date,
			Installments:    t.Installments,
			IsShared:        t.IsShared,
		})
		if err != nil {
			failed++
			if s.logger != nil {
				s.logger.WarnContext(ctx, "recurring expense create falló",
					"templateId", t.ID.String(), "error", err)
			}
			continue
		}
		if err := s.repo.MarkGenerated(ctx, t.ID, date); err != nil {
			// El expense ya se creó — si esto falla, el próximo tick
			// intentaría duplicar. Lo logueamos pero no revertimos: el
			// user puede borrar el duplicado manualmente si pasa.
			failed++
			if s.logger != nil {
				s.logger.WarnContext(ctx, "recurring expense markGenerated falló",
					"templateId", t.ID.String(), "error", err)
			}
			continue
		}
		created++
	}
	return created, failed, nil
}

// ===================== helpers =====================

func validateRecurrence(frequency string, dom, dow, moy *int) error {
	switch frequency {
	case "monthly":
		if dom == nil || *dom < 1 || *dom > 31 {
			return domain.NewValidationError("dayOfMonth", "requerido (1..31) para frequency=monthly")
		}
	case "weekly":
		if dow == nil || *dow < 0 || *dow > 6 {
			return domain.NewValidationError("dayOfWeek", "requerido (0..6) para frequency=weekly")
		}
	case "yearly":
		if dom == nil || *dom < 1 || *dom > 31 {
			return domain.NewValidationError("dayOfMonth", "requerido (1..31) para frequency=yearly")
		}
		if moy == nil || *moy < 1 || *moy > 12 {
			return domain.NewValidationError("monthOfYear", "requerido (1..12) para frequency=yearly")
		}
	default:
		return domain.NewValidationError("frequency", "debe ser monthly/weekly/yearly")
	}
	return nil
}

func recurrenceMatches(t domain.RecurringExpense, date time.Time) bool {
	year, month, day := date.Date()
	switch t.Frequency {
	case "monthly":
		if t.DayOfMonth == nil {
			return false
		}
		return day == clampDay(*t.DayOfMonth, year, int(month))
	case "weekly":
		if t.DayOfWeek == nil {
			return false
		}
		return int(date.Weekday()) == *t.DayOfWeek
	case "yearly":
		if t.DayOfMonth == nil || t.MonthOfYear == nil {
			return false
		}
		return int(month) == *t.MonthOfYear && day == clampDay(*t.DayOfMonth, year, *t.MonthOfYear)
	}
	return false
}

// clampDay: si el user configuró day_of_month=31, en febrero cae al 28/29.
func clampDay(d, year, month int) int {
	last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if d > last {
		return last
	}
	return d
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

