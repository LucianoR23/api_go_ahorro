// Package incomes: registro de ingresos (cobros) y plantillas recurrentes.
// Simétrico a expenses en estructura, pero sin cuotas ni shares — un ingreso
// es un evento puntual que suma a la billetera del user que lo recibió.
package incomes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db"
	sqlcgen "github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// ===================== incomes =====================

// CreateParams: payload crudo al repo. El service ya hizo fx + validación.
type CreateParams struct {
	HouseholdID     uuid.UUID
	ReceivedBy      uuid.UUID
	PaymentMethodID *uuid.UUID
	Amount          float64
	Currency        string
	AmountBase      float64
	BaseCurrency    string
	RateUsed        *float64
	RateAt          *time.Time
	Source          string
	Description     string
	ReceivedAt      time.Time
}

func (r *Repository) Create(ctx context.Context, p CreateParams) (domain.Income, error) {
	amountN, err := db.NumericFromFloat(p.Amount, 2)
	if err != nil {
		return domain.Income{}, err
	}
	amountBaseN, err := db.NumericFromFloat(p.AmountBase, 2)
	if err != nil {
		return domain.Income{}, err
	}
	var rateN pgtype.Numeric
	if p.RateUsed != nil {
		rateN, err = db.NumericFromFloat(*p.RateUsed, 4)
		if err != nil {
			return domain.Income{}, err
		}
	}
	var rateAtTS pgtype.Timestamptz
	if p.RateAt != nil {
		rateAtTS = pgtype.Timestamptz{Time: *p.RateAt, Valid: true}
	}

	row, err := r.q.CreateIncome(ctx, sqlcgen.CreateIncomeParams{
		HouseholdID:     p.HouseholdID,
		ReceivedBy:      p.ReceivedBy,
		PaymentMethodID: p.PaymentMethodID,
		Amount:          amountN,
		Currency:        p.Currency,
		AmountBase:      amountBaseN,
		BaseCurrency:    p.BaseCurrency,
		RateUsed:        rateN,
		RateAt:          rateAtTS,
		Source:          p.Source,
		Description:     p.Description,
		ReceivedAt:      pgtype.Date{Time: p.ReceivedAt, Valid: true},
	})
	if err != nil {
		return domain.Income{}, fmt.Errorf("incomes.Create: %w", err)
	}
	return toIncome(row), nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.Income, error) {
	row, err := r.q.GetIncomeByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Income{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Income{}, fmt.Errorf("incomes.GetByID: %w", err)
	}
	return toIncome(row), nil
}

// ListFilter: todos los campos son opcionales — el handler arma la combinación
// según la vista. source es pgtype.Text porque sqlc lo generó así (TEXT en
// la query con sqlc.narg).
type ListFilter struct {
	HouseholdID     uuid.UUID
	ReceivedBy      *uuid.UUID
	PaymentMethodID *uuid.UUID
	Source          *string
	FromDate        *time.Time
	ToDate          *time.Time
	Limit           int32
	Offset          int32
}

func (r *Repository) List(ctx context.Context, f ListFilter) ([]domain.Income, int64, error) {
	var src pgtype.Text
	if f.Source != nil {
		src = pgtype.Text{String: *f.Source, Valid: true}
	}
	var fromD, toD pgtype.Date
	if f.FromDate != nil {
		fromD = pgtype.Date{Time: *f.FromDate, Valid: true}
	}
	if f.ToDate != nil {
		toD = pgtype.Date{Time: *f.ToDate, Valid: true}
	}

	rows, err := r.q.ListIncomesByHousehold(ctx, sqlcgen.ListIncomesByHouseholdParams{
		HouseholdID:     f.HouseholdID,
		Limit:           f.Limit,
		Offset:          f.Offset,
		ReceivedBy:      f.ReceivedBy,
		PaymentMethodID: f.PaymentMethodID,
		Source:          src,
		FromDate:        fromD,
		ToDate:          toD,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("incomes.List: %w", err)
	}
	count, err := r.q.CountIncomesByHousehold(ctx, sqlcgen.CountIncomesByHouseholdParams{
		HouseholdID:     f.HouseholdID,
		ReceivedBy:      f.ReceivedBy,
		PaymentMethodID: f.PaymentMethodID,
		Source:          src,
		FromDate:        fromD,
		ToDate:          toD,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("incomes.Count: %w", err)
	}
	out := make([]domain.Income, len(rows))
	for i, row := range rows {
		out[i] = toIncome(row)
	}
	return out, count, nil
}

// UpdateParams: solo campos editables (source/description/received_at).
type UpdateParams struct {
	Source      string
	Description string
	ReceivedAt  time.Time
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (domain.Income, error) {
	row, err := r.q.UpdateIncome(ctx, sqlcgen.UpdateIncomeParams{
		ID:          id,
		Source:      p.Source,
		Description: p.Description,
		ReceivedAt:  pgtype.Date{Time: p.ReceivedAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Income{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Income{}, fmt.Errorf("incomes.Update: %w", err)
	}
	return toIncome(row), nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.q.DeleteIncome(ctx, id); err != nil {
		return fmt.Errorf("incomes.Delete: %w", err)
	}
	return nil
}

// SumBaseInRange devuelve el total amount_base (en base_currency del hogar)
// recibido en [from, to]. Usado por /totals/income.
func (r *Repository) SumBaseInRange(ctx context.Context, householdID uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumIncomesByHouseholdInRange(ctx, sqlcgen.SumIncomesByHouseholdInRangeParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("incomes.SumBaseInRange: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

// ===================== recurring_incomes =====================

type CreateRecurringParams struct {
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
	IsActive        bool
	StartsAt        time.Time
	EndsAt          *time.Time
}

func (r *Repository) CreateRecurring(ctx context.Context, p CreateRecurringParams) (domain.RecurringIncome, error) {
	amountN, err := db.NumericFromFloat(p.Amount, 2)
	if err != nil {
		return domain.RecurringIncome{}, err
	}
	row, err := r.q.CreateRecurringIncome(ctx, sqlcgen.CreateRecurringIncomeParams{
		HouseholdID:     p.HouseholdID,
		ReceivedBy:      p.ReceivedBy,
		PaymentMethodID: p.PaymentMethodID,
		Amount:          amountN,
		Currency:        p.Currency,
		Description:     p.Description,
		Source:          p.Source,
		Frequency:       p.Frequency,
		DayOfMonth:      intPtrToInt4(p.DayOfMonth),
		DayOfWeek:       intPtrToInt4(p.DayOfWeek),
		MonthOfYear:     intPtrToInt4(p.MonthOfYear),
		IsActive:        p.IsActive,
		StartsAt:        pgtype.Date{Time: p.StartsAt, Valid: true},
		EndsAt:          datePtr(p.EndsAt),
	})
	if err != nil {
		return domain.RecurringIncome{}, fmt.Errorf("incomes.CreateRecurring: %w", err)
	}
	return toRecurring(row), nil
}

func (r *Repository) GetRecurringByID(ctx context.Context, id uuid.UUID) (domain.RecurringIncome, error) {
	row, err := r.q.GetRecurringIncomeByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RecurringIncome{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RecurringIncome{}, fmt.Errorf("incomes.GetRecurringByID: %w", err)
	}
	return toRecurring(row), nil
}

func (r *Repository) ListRecurringByHousehold(ctx context.Context, householdID uuid.UUID) ([]domain.RecurringIncome, error) {
	rows, err := r.q.ListRecurringIncomesByHousehold(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("incomes.ListRecurring: %w", err)
	}
	out := make([]domain.RecurringIncome, len(rows))
	for i, row := range rows {
		out[i] = toRecurring(row)
	}
	return out, nil
}

// ListActiveRecurring lo usa el worker: trae plantillas activas cuyo rango
// cubre `date`. El filtro fino por frequency/day_of_* se hace en Go.
func (r *Repository) ListActiveRecurring(ctx context.Context, date time.Time) ([]domain.RecurringIncome, error) {
	rows, err := r.q.ListActiveRecurringIncomes(ctx, pgtype.Date{Time: date, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("incomes.ListActiveRecurring: %w", err)
	}
	out := make([]domain.RecurringIncome, len(rows))
	for i, row := range rows {
		out[i] = toRecurring(row)
	}
	return out, nil
}

type UpdateRecurringParams struct {
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

func (r *Repository) UpdateRecurring(ctx context.Context, id uuid.UUID, p UpdateRecurringParams) (domain.RecurringIncome, error) {
	amountN, err := db.NumericFromFloat(p.Amount, 2)
	if err != nil {
		return domain.RecurringIncome{}, err
	}
	row, err := r.q.UpdateRecurringIncome(ctx, sqlcgen.UpdateRecurringIncomeParams{
		ID:              id,
		Amount:          amountN,
		Currency:        p.Currency,
		Description:     p.Description,
		Source:          p.Source,
		Frequency:       p.Frequency,
		DayOfMonth:      intPtrToInt4(p.DayOfMonth),
		DayOfWeek:       intPtrToInt4(p.DayOfWeek),
		MonthOfYear:     intPtrToInt4(p.MonthOfYear),
		EndsAt:          datePtr(p.EndsAt),
		PaymentMethodID: p.PaymentMethodID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RecurringIncome{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RecurringIncome{}, fmt.Errorf("incomes.UpdateRecurring: %w", err)
	}
	return toRecurring(row), nil
}

func (r *Repository) SetRecurringActive(ctx context.Context, id uuid.UUID, active bool) error {
	if err := r.q.SetRecurringIncomeActive(ctx, sqlcgen.SetRecurringIncomeActiveParams{ID: id, IsActive: active}); err != nil {
		return fmt.Errorf("incomes.SetRecurringActive: %w", err)
	}
	return nil
}

func (r *Repository) MarkRecurringGenerated(ctx context.Context, id uuid.UUID, date time.Time) error {
	if err := r.q.MarkRecurringIncomeGenerated(ctx, sqlcgen.MarkRecurringIncomeGeneratedParams{
		ID:      id,
		Column2: pgtype.Date{Time: date, Valid: true},
	}); err != nil {
		return fmt.Errorf("incomes.MarkRecurringGenerated: %w", err)
	}
	return nil
}

func (r *Repository) DeleteRecurring(ctx context.Context, id uuid.UUID) error {
	if err := r.q.DeleteRecurringIncome(ctx, id); err != nil {
		return fmt.Errorf("incomes.DeleteRecurring: %w", err)
	}
	return nil
}

// ===================== mappers + helpers =====================

func toIncome(row sqlcgen.Income) domain.Income {
	var rateUsed *float64
	if row.RateUsed.Valid {
		f := db.FloatFromNumeric(row.RateUsed)
		rateUsed = &f
	}
	var rateAt *time.Time
	if row.RateAt.Valid {
		t := row.RateAt.Time
		rateAt = &t
	}
	return domain.Income{
		ID:              row.ID,
		HouseholdID:     row.HouseholdID,
		ReceivedBy:      row.ReceivedBy,
		PaymentMethodID: row.PaymentMethodID,
		Amount:          db.FloatFromNumeric(row.Amount),
		Currency:        row.Currency,
		AmountBase:      db.FloatFromNumeric(row.AmountBase),
		BaseCurrency:    row.BaseCurrency,
		RateUsed:        rateUsed,
		RateAt:          rateAt,
		Source:          row.Source,
		Description:     row.Description,
		ReceivedAt:      row.ReceivedAt.Time,
		CreatedAt:       row.CreatedAt.Time,
	}
}

func toRecurring(row sqlcgen.RecurringIncome) domain.RecurringIncome {
	var endsAt, lastGen *time.Time
	if row.EndsAt.Valid {
		t := row.EndsAt.Time
		endsAt = &t
	}
	if row.LastGenerated.Valid {
		t := row.LastGenerated.Time
		lastGen = &t
	}
	return domain.RecurringIncome{
		ID:              row.ID,
		HouseholdID:     row.HouseholdID,
		ReceivedBy:      row.ReceivedBy,
		PaymentMethodID: row.PaymentMethodID,
		Amount:          db.FloatFromNumeric(row.Amount),
		Currency:        row.Currency,
		Description:     row.Description,
		Source:          row.Source,
		Frequency:       row.Frequency,
		DayOfMonth:      int4ToIntPtr(row.DayOfMonth),
		DayOfWeek:       int4ToIntPtr(row.DayOfWeek),
		MonthOfYear:     int4ToIntPtr(row.MonthOfYear),
		IsActive:        row.IsActive,
		StartsAt:        row.StartsAt.Time,
		EndsAt:          endsAt,
		LastGenerated:   lastGen,
		CreatedAt:       row.CreatedAt.Time,
	}
}

func intPtrToInt4(p *int) pgtype.Int4 {
	if p == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*p), Valid: true}
}

func int4ToIntPtr(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

func datePtr(p *time.Time) pgtype.Date {
	if p == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *p, Valid: true}
}
