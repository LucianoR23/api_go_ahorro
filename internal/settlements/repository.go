// Package settlements: pagos de deuda entre miembros del hogar. No tocan
// payment_methods ni expenses — solo registran "X le pagó Y pesos a Z".
// El balance se recalcula on-demand contra el paquete balances.
package settlements

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

// CreateParams: input crudo al repo. El service valida (pair, amount <= deuda)
// antes de llegar acá.
type CreateParams struct {
	HouseholdID  uuid.UUID
	FromUser     uuid.UUID
	ToUser       uuid.UUID
	AmountBase   float64
	BaseCurrency string
	Note         *string
	PaidAt       time.Time
}

func (r *Repository) Create(ctx context.Context, p CreateParams) (domain.SettlementPayment, error) {
	amountN, err := db.NumericFromFloat(p.AmountBase, 2)
	if err != nil {
		return domain.SettlementPayment{}, fmt.Errorf("settlements.Create/amount: %w", err)
	}
	note := pgtype.Text{}
	if p.Note != nil {
		note = pgtype.Text{String: *p.Note, Valid: true}
	}
	row, err := r.q.CreateSettlement(ctx, sqlcgen.CreateSettlementParams{
		HouseholdID:  p.HouseholdID,
		FromUser:     p.FromUser,
		ToUser:       p.ToUser,
		AmountBase:   amountN,
		BaseCurrency: p.BaseCurrency,
		Note:         note,
		PaidAt:       pgtype.Date{Time: p.PaidAt, Valid: true},
	})
	if err != nil {
		return domain.SettlementPayment{}, fmt.Errorf("settlements.Create: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.SettlementPayment, error) {
	row, err := r.q.GetSettlementByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SettlementPayment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.SettlementPayment{}, fmt.Errorf("settlements.GetByID: %w", err)
	}
	return toDomain(row), nil
}

// ListFilter: filtros opcionales para listar pagos del hogar. Todos son
// nullable porque el frontend arma combinaciones según la vista.
type ListFilter struct {
	HouseholdID uuid.UUID
	FromUser    *uuid.UUID
	ToUser      *uuid.UUID
	FromDate    *time.Time
	ToDate      *time.Time
	Limit       int32
	Offset      int32
}

func (r *Repository) List(ctx context.Context, f ListFilter) ([]domain.SettlementPayment, error) {
	var fromDate, toDate pgtype.Date
	if f.FromDate != nil {
		fromDate = pgtype.Date{Time: *f.FromDate, Valid: true}
	}
	if f.ToDate != nil {
		toDate = pgtype.Date{Time: *f.ToDate, Valid: true}
	}
	rows, err := r.q.ListSettlementsByHousehold(ctx, sqlcgen.ListSettlementsByHouseholdParams{
		HouseholdID: f.HouseholdID,
		Limit:       f.Limit,
		Offset:      f.Offset,
		FromUser:    f.FromUser,
		ToUser:      f.ToUser,
		FromDate:    fromDate,
		ToDate:      toDate,
	})
	if err != nil {
		return nil, fmt.Errorf("settlements.List: %w", err)
	}
	out := make([]domain.SettlementPayment, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.q.DeleteSettlement(ctx, id); err != nil {
		return fmt.Errorf("settlements.Delete: %w", err)
	}
	return nil
}

func toDomain(row sqlcgen.SettlementPayment) domain.SettlementPayment {
	var note *string
	if row.Note.Valid {
		s := row.Note.String
		note = &s
	}
	return domain.SettlementPayment{
		ID:           row.ID,
		HouseholdID:  row.HouseholdID,
		FromUser:     row.FromUser,
		ToUser:       row.ToUser,
		AmountBase:   db.FloatFromNumeric(row.AmountBase),
		BaseCurrency: row.BaseCurrency,
		Note:         note,
		PaidAt:       row.PaidAt.Time,
		CreatedAt:    row.CreatedAt.Time,
	}
}
