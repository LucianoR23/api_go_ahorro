// Package recurringexpenses: plantillas de gastos recurrentes. La ejecución
// real (crear el expense del día) la hace expenses.Service.Create — este
// paquete solo almacena la plantilla y expone el CRUD.
package recurringexpenses

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

type CreateParams struct {
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
	IsActive        bool
	StartsAt        time.Time
	EndsAt          *time.Time
}

func (r *Repository) Create(ctx context.Context, p CreateParams) (domain.RecurringExpense, error) {
	amountN, err := db.NumericFromFloat(p.Amount, 2)
	if err != nil {
		return domain.RecurringExpense{}, err
	}
	row, err := r.q.CreateRecurringExpense(ctx, sqlcgen.CreateRecurringExpenseParams{
		HouseholdID:     p.HouseholdID,
		CreatedBy:       p.CreatedBy,
		CategoryID:      p.CategoryID,
		PaymentMethodID: p.PaymentMethodID,
		Amount:          amountN,
		Currency:        p.Currency,
		Description:     p.Description,
		Installments:    int32(p.Installments),
		IsShared:        p.IsShared,
		Frequency:       p.Frequency,
		DayOfMonth:      intPtrToInt4(p.DayOfMonth),
		DayOfWeek:       intPtrToInt4(p.DayOfWeek),
		MonthOfYear:     intPtrToInt4(p.MonthOfYear),
		IsActive:        p.IsActive,
		StartsAt:        pgtype.Date{Time: p.StartsAt, Valid: true},
		EndsAt:          datePtr(p.EndsAt),
	})
	if err != nil {
		return domain.RecurringExpense{}, fmt.Errorf("recurringexpenses.Create: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.RecurringExpense, error) {
	row, err := r.q.GetRecurringExpenseByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RecurringExpense{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RecurringExpense{}, fmt.Errorf("recurringexpenses.GetByID: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) ListByHousehold(ctx context.Context, householdID uuid.UUID) ([]domain.RecurringExpense, error) {
	rows, err := r.q.ListRecurringExpensesByHousehold(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("recurringexpenses.List: %w", err)
	}
	out := make([]domain.RecurringExpense, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

func (r *Repository) ListActive(ctx context.Context, date time.Time) ([]domain.RecurringExpense, error) {
	rows, err := r.q.ListActiveRecurringExpenses(ctx, pgtype.Date{Time: date, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("recurringexpenses.ListActive: %w", err)
	}
	out := make([]domain.RecurringExpense, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

type UpdateParams struct {
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

func (r *Repository) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (domain.RecurringExpense, error) {
	amountN, err := db.NumericFromFloat(p.Amount, 2)
	if err != nil {
		return domain.RecurringExpense{}, err
	}
	row, err := r.q.UpdateRecurringExpense(ctx, sqlcgen.UpdateRecurringExpenseParams{
		ID:              id,
		Amount:          amountN,
		Currency:        p.Currency,
		Description:     p.Description,
		Installments:    int32(p.Installments),
		IsShared:        p.IsShared,
		Frequency:       p.Frequency,
		DayOfMonth:      intPtrToInt4(p.DayOfMonth),
		DayOfWeek:       intPtrToInt4(p.DayOfWeek),
		MonthOfYear:     intPtrToInt4(p.MonthOfYear),
		EndsAt:          datePtr(p.EndsAt),
		CategoryID:      p.CategoryID,
		PaymentMethodID: p.PaymentMethodID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RecurringExpense{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.RecurringExpense{}, fmt.Errorf("recurringexpenses.Update: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	if err := r.q.SetRecurringExpenseActive(ctx, sqlcgen.SetRecurringExpenseActiveParams{ID: id, IsActive: active}); err != nil {
		return fmt.Errorf("recurringexpenses.SetActive: %w", err)
	}
	return nil
}

func (r *Repository) MarkGenerated(ctx context.Context, id uuid.UUID, date time.Time) error {
	if err := r.q.MarkRecurringExpenseGenerated(ctx, sqlcgen.MarkRecurringExpenseGeneratedParams{
		ID:      id,
		Column2: pgtype.Date{Time: date, Valid: true},
	}); err != nil {
		return fmt.Errorf("recurringexpenses.MarkGenerated: %w", err)
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.q.DeleteRecurringExpense(ctx, id); err != nil {
		return fmt.Errorf("recurringexpenses.Delete: %w", err)
	}
	return nil
}

// ===================== mappers =====================

func toDomain(row sqlcgen.RecurringExpense) domain.RecurringExpense {
	var endsAt, lastGen *time.Time
	if row.EndsAt.Valid {
		t := row.EndsAt.Time
		endsAt = &t
	}
	if row.LastGenerated.Valid {
		t := row.LastGenerated.Time
		lastGen = &t
	}
	return domain.RecurringExpense{
		ID:              row.ID,
		HouseholdID:     row.HouseholdID,
		CreatedBy:       row.CreatedBy,
		CategoryID:      row.CategoryID,
		PaymentMethodID: row.PaymentMethodID,
		Amount:          db.FloatFromNumeric(row.Amount),
		Currency:        row.Currency,
		Description:     row.Description,
		Installments:    int(row.Installments),
		IsShared:        row.IsShared,
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
