package incomes

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// householdLookup: interface mínima que necesitamos de households.
// Usamos IsMember para validar que receivedBy pertenezca al hogar y
// GetByID para leer base_currency al convertir FX.
type householdLookup interface {
	IsMember(ctx context.Context, householdID, userID uuid.UUID) (bool, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.Household, error)
}

// fxConverter: convierte amount de una currency a otra. Lo implementa
// fxrates.Service. Definido acá para no acoplar.
type fxConverter interface {
	Convert(ctx context.Context, amount float64, from, to string) (converted, rate float64, err error)
}

type Service struct {
	repo       *Repository
	households householdLookup
	fx         fxConverter
	logger     *slog.Logger
}

func NewService(repo *Repository, households householdLookup, fx fxConverter, logger *slog.Logger) *Service {
	return &Service{repo: repo, households: households, fx: fx, logger: logger}
}

// CreateInput: payload parseado del handler.
type CreateInput struct {
	HouseholdID     uuid.UUID
	ReceivedBy      uuid.UUID
	PaymentMethodID *uuid.UUID
	Amount          float64
	Currency        string
	Source          string
	Description     string
	ReceivedAt      time.Time
}

// Create valida input, convierte a base_currency, y persiste.
// Reglas:
//   - amount > 0, currency válida, source válido, description no vacío
//   - receivedBy debe ser miembro del hogar (el header X-Household-ID ya
//     validó que el caller lo sea, pero en households multi-user el ingreso
//     puede ser de otro miembro — igual tiene que ser del hogar)
func (s *Service) Create(ctx context.Context, in CreateInput) (domain.Income, error) {
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Source = strings.ToLower(strings.TrimSpace(in.Source))
	in.Description = strings.TrimSpace(in.Description)

	if in.Amount <= 0 {
		return domain.Income{}, domain.NewValidationError("amount", "debe ser mayor a cero")
	}
	if !isValidSource(in.Source) {
		return domain.Income{}, domain.NewValidationError("source", fmt.Sprintf("debe ser uno de: %s", strings.Join(domain.IncomeSources, ", ")))
	}
	if in.Description == "" {
		return domain.Income{}, domain.NewValidationError("description", "no puede estar vacío")
	}
	if in.ReceivedAt.IsZero() {
		in.ReceivedAt = time.Now()
	}

	ok, err := s.households.IsMember(ctx, in.HouseholdID, in.ReceivedBy)
	if err != nil {
		return domain.Income{}, err
	}
	if !ok {
		return domain.Income{}, domain.NewValidationError("receivedBy", "no es miembro del hogar")
	}

	hh, err := s.households.GetByID(ctx, in.HouseholdID)
	if err != nil {
		return domain.Income{}, err
	}

	// Conversión FX. Mismo patrón que expenses: si la moneda del input
	// coincide con la base, no guardamos rate (convert devuelve 1).
	amountBase, rate, err := s.fx.Convert(ctx, in.Amount, in.Currency, hh.BaseCurrency)
	if err != nil {
		return domain.Income{}, err
	}
	var rateUsed *float64
	var rateAt *time.Time
	if in.Currency != hh.BaseCurrency {
		r := rate
		now := time.Now().UTC()
		rateUsed = &r
		rateAt = &now
	}

	return s.repo.Create(ctx, CreateParams{
		HouseholdID:     in.HouseholdID,
		ReceivedBy:      in.ReceivedBy,
		PaymentMethodID: in.PaymentMethodID,
		Amount:          in.Amount,
		Currency:        in.Currency,
		AmountBase:      amountBase,
		BaseCurrency:    hh.BaseCurrency,
		RateUsed:        rateUsed,
		RateAt:          rateAt,
		Source:          in.Source,
		Description:     in.Description,
		ReceivedAt:      in.ReceivedAt,
	})
}

// Get valida que el income pertenezca al hogar del caller.
func (s *Service) Get(ctx context.Context, householdID, id uuid.UUID) (domain.Income, error) {
	inc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Income{}, err
	}
	if inc.HouseholdID != householdID {
		return domain.Income{}, domain.ErrNotFound
	}
	return inc, nil
}

func (s *Service) List(ctx context.Context, f ListFilter) ([]domain.Income, int64, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	return s.repo.List(ctx, f)
}

// UpdateInput: solo campos editables.
type UpdateInput struct {
	Source      string
	Description string
	ReceivedAt  time.Time
}

func (s *Service) Update(ctx context.Context, householdID, id uuid.UUID, in UpdateInput) (domain.Income, error) {
	in.Source = strings.ToLower(strings.TrimSpace(in.Source))
	in.Description = strings.TrimSpace(in.Description)
	if !isValidSource(in.Source) {
		return domain.Income{}, domain.NewValidationError("source", fmt.Sprintf("debe ser uno de: %s", strings.Join(domain.IncomeSources, ", ")))
	}
	if in.Description == "" {
		return domain.Income{}, domain.NewValidationError("description", "no puede estar vacío")
	}
	// Validar pertenencia al hogar antes de actualizar.
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Income{}, err
	}
	if existing.HouseholdID != householdID {
		return domain.Income{}, domain.ErrNotFound
	}
	return s.repo.Update(ctx, id, UpdateParams{
		Source:      in.Source,
		Description: in.Description,
		ReceivedAt:  in.ReceivedAt,
	})
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

// TotalsInRange devuelve la suma de ingresos (amount_base) en [from, to].
// Usado por /totals/income. Si no hay ingresos devuelve 0.
func (s *Service) TotalsInRange(ctx context.Context, householdID uuid.UUID, from, to time.Time) (float64, string, error) {
	hh, err := s.households.GetByID(ctx, householdID)
	if err != nil {
		return 0, "", err
	}
	total, err := s.repo.SumBaseInRange(ctx, householdID, from, to)
	if err != nil {
		return 0, "", err
	}
	return total, hh.BaseCurrency, nil
}

// ===================== recurring =====================

// CreateRecurringInput: plantilla. Validaciones por frequency:
//   - monthly → DayOfMonth requerido (1..31)
//   - weekly  → DayOfWeek requerido (0..6, 0=dom)
//   - yearly  → DayOfMonth + MonthOfYear requeridos
type CreateRecurringInput struct {
	HouseholdID     uuid.UUID
	ReceivedBy      uuid.UUID
	PaymentMethodID *uuid.UUID
	Amount          float64
	Currency        string
	Description     string
	Source          string
	Frequency       string
	DayOfMonth      *int
	DayOfWeek       *int
	MonthOfYear     *int
	StartsAt        time.Time
	EndsAt          *time.Time
}

func (s *Service) CreateRecurring(ctx context.Context, in CreateRecurringInput) (domain.RecurringIncome, error) {
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Source = strings.ToLower(strings.TrimSpace(in.Source))
	in.Frequency = strings.ToLower(strings.TrimSpace(in.Frequency))
	in.Description = strings.TrimSpace(in.Description)

	if in.Amount <= 0 {
		return domain.RecurringIncome{}, domain.NewValidationError("amount", "debe ser mayor a cero")
	}
	if !isValidSource(in.Source) {
		return domain.RecurringIncome{}, domain.NewValidationError("source", "inválido")
	}
	if err := validateRecurrence(in.Frequency, in.DayOfMonth, in.DayOfWeek, in.MonthOfYear); err != nil {
		return domain.RecurringIncome{}, err
	}
	if in.Description == "" {
		return domain.RecurringIncome{}, domain.NewValidationError("description", "no puede estar vacío")
	}
	ok, err := s.households.IsMember(ctx, in.HouseholdID, in.ReceivedBy)
	if err != nil {
		return domain.RecurringIncome{}, err
	}
	if !ok {
		return domain.RecurringIncome{}, domain.NewValidationError("receivedBy", "no es miembro del hogar")
	}
	if in.StartsAt.IsZero() {
		in.StartsAt = time.Now()
	}
	ri, err := s.repo.CreateRecurring(ctx, CreateRecurringParams{
		HouseholdID:     in.HouseholdID,
		ReceivedBy:      in.ReceivedBy,
		PaymentMethodID: in.PaymentMethodID,
		Amount:          in.Amount,
		Currency:        in.Currency,
		Description:     in.Description,
		Source:          in.Source,
		Frequency:       in.Frequency,
		DayOfMonth:      in.DayOfMonth,
		DayOfWeek:       in.DayOfWeek,
		MonthOfYear:     in.MonthOfYear,
		IsActive:        true,
		StartsAt:        in.StartsAt,
		EndsAt:          in.EndsAt,
	})
	if err != nil {
		return domain.RecurringIncome{}, err
	}

	// "Cuenta este mes": si el calendario ya tiene una ocurrencia vencida en
	// el período en curso (el día del mes ya pasó, o el worker de las 00:30 ya
	// había corrido hoy), la materializamos al instante en vez de esperar al
	// próximo ciclo. Best-effort: si falla, la plantilla igual quedó creada.
	if occ, ok := firstDueOccurrence(ri, time.Now()); ok {
		if genErr := s.generateOneRecurring(ctx, ri, occ); genErr != nil {
			if s.logger != nil {
				s.logger.WarnContext(ctx, "recurring income: primera ocurrencia falló",
					"templateId", ri.ID.String(), "error", genErr)
			}
		} else {
			lg := occ
			ri.LastGenerated = &lg
		}
	}
	return ri, nil
}

func (s *Service) ListRecurring(ctx context.Context, householdID uuid.UUID) ([]domain.RecurringIncome, error) {
	return s.repo.ListRecurringByHousehold(ctx, householdID)
}

func (s *Service) GetRecurring(ctx context.Context, householdID, id uuid.UUID) (domain.RecurringIncome, error) {
	ri, err := s.repo.GetRecurringByID(ctx, id)
	if err != nil {
		return domain.RecurringIncome{}, err
	}
	if ri.HouseholdID != householdID {
		return domain.RecurringIncome{}, domain.ErrNotFound
	}
	return ri, nil
}

type UpdateRecurringInput struct {
	Amount          float64
	Currency        string
	Description     string
	Source          string
	Frequency       string
	DayOfMonth      *int
	DayOfWeek       *int
	MonthOfYear     *int
	EndsAt          *time.Time
	PaymentMethodID *uuid.UUID
}

func (s *Service) UpdateRecurring(ctx context.Context, householdID, id uuid.UUID, in UpdateRecurringInput) (domain.RecurringIncome, error) {
	existing, err := s.repo.GetRecurringByID(ctx, id)
	if err != nil {
		return domain.RecurringIncome{}, err
	}
	if existing.HouseholdID != householdID {
		return domain.RecurringIncome{}, domain.ErrNotFound
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.Source = strings.ToLower(strings.TrimSpace(in.Source))
	in.Frequency = strings.ToLower(strings.TrimSpace(in.Frequency))
	in.Description = strings.TrimSpace(in.Description)

	if in.Amount <= 0 {
		return domain.RecurringIncome{}, domain.NewValidationError("amount", "debe ser mayor a cero")
	}
	if !isValidSource(in.Source) {
		return domain.RecurringIncome{}, domain.NewValidationError("source", "inválido")
	}
	if err := validateRecurrence(in.Frequency, in.DayOfMonth, in.DayOfWeek, in.MonthOfYear); err != nil {
		return domain.RecurringIncome{}, err
	}
	if in.Description == "" {
		return domain.RecurringIncome{}, domain.NewValidationError("description", "no puede estar vacío")
	}
	return s.repo.UpdateRecurring(ctx, id, UpdateRecurringParams{
		Amount:          in.Amount,
		Currency:        in.Currency,
		Description:     in.Description,
		Source:          in.Source,
		Frequency:       in.Frequency,
		DayOfMonth:      in.DayOfMonth,
		DayOfWeek:       in.DayOfWeek,
		MonthOfYear:     in.MonthOfYear,
		EndsAt:          in.EndsAt,
		PaymentMethodID: in.PaymentMethodID,
	})
}

func (s *Service) SetRecurringActive(ctx context.Context, householdID, id uuid.UUID, active bool) error {
	existing, err := s.repo.GetRecurringByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return s.repo.SetRecurringActive(ctx, id, active)
}

func (s *Service) DeleteRecurring(ctx context.Context, householdID, id uuid.UUID) error {
	existing, err := s.repo.GetRecurringByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return s.repo.DeleteRecurring(ctx, id)
}

// ===================== worker API (interno) =====================

// GenerateDue lo llama el worker: busca plantillas activas cuyo calendario
// toca `date`, crea el income correspondiente y marca last_generated.
// Idempotente: si last_generated == date, se saltea.
//
// Devuelve la cantidad de incomes creados (métrica para logs).
func (s *Service) GenerateDue(ctx context.Context, date time.Time) (int, error) {
	templates, err := s.repo.ListActiveRecurring(ctx, date)
	if err != nil {
		return 0, err
	}
	created := 0
	for _, t := range templates {
		if t.LastGenerated != nil && sameDay(*t.LastGenerated, date) {
			continue
		}
		if !recurrenceMatches(t, date) {
			continue
		}
		if err := s.generateOneRecurring(ctx, t, date); err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

// generateOneRecurring materializa UNA ocurrencia de la plantilla `t` con fecha
// `date`: convierte FX contra la base_currency actual del hogar, crea el income
// y marca last_generated. Lo comparten el worker (GenerateDue) y CreateRecurring
// (primera ocurrencia "cuenta este mes").
func (s *Service) generateOneRecurring(ctx context.Context, t domain.RecurringIncome, date time.Time) error {
	hh, err := s.households.GetByID(ctx, t.HouseholdID)
	if err != nil {
		return err
	}
	amountBase, rate, err := s.fx.Convert(ctx, t.Amount, t.Currency, hh.BaseCurrency)
	if err != nil {
		return err
	}
	var rateUsed *float64
	var rateAt *time.Time
	if t.Currency != hh.BaseCurrency {
		r := rate
		now := time.Now().UTC()
		rateUsed = &r
		rateAt = &now
	}
	if _, err := s.repo.Create(ctx, CreateParams{
		HouseholdID:     t.HouseholdID,
		ReceivedBy:      t.ReceivedBy,
		PaymentMethodID: t.PaymentMethodID,
		Amount:          t.Amount,
		Currency:        t.Currency,
		AmountBase:      amountBase,
		BaseCurrency:    hh.BaseCurrency,
		RateUsed:        rateUsed,
		RateAt:          rateAt,
		Source:          t.Source,
		Description:     t.Description,
		ReceivedAt:      date,
	}); err != nil {
		return err
	}
	return s.repo.MarkRecurringGenerated(ctx, t.ID, date)
}

// ===================== helpers =====================

func isValidSource(s string) bool {
	for _, v := range domain.IncomeSources {
		if s == v {
			return true
		}
	}
	return false
}

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

// firstDueOccurrence devuelve la ocurrencia más reciente del calendario de la
// plantilla dentro de [startsAt, today], o (zero, false) si no hay ninguna
// vencida todavía. La usa CreateRecurring para materializar al instante el
// cargo del período en curso en vez de esperar al próximo ciclo del worker.
// Escanea hacia atrás desde hoy y corta en la primera coincidencia.
func firstDueOccurrence(t domain.RecurringIncome, today time.Time) (time.Time, bool) {
	loc := today.Location()
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
	start := time.Date(t.StartsAt.Year(), t.StartsAt.Month(), t.StartsAt.Day(), 0, 0, 0, 0, loc)
	if start.After(today) {
		return time.Time{}, false
	}
	for d := today; !d.Before(start); d = d.AddDate(0, 0, -1) {
		if recurrenceMatches(t, d) {
			return d, true
		}
	}
	return time.Time{}, false
}

// recurrenceMatches: ¿el calendario de la plantilla coincide con `date`?
// Para monthly con dayOfMonth > días del mes (ej: 31 en febrero), caemos
// al último día del mes — evita que "el 31" nunca se genere en meses cortos.
func recurrenceMatches(t domain.RecurringIncome, date time.Time) bool {
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

// clampDay reduce el día al último del mes si se pasa. Ej: dayOfMonth=31
// en febrero no-bisiesto → 28.
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
