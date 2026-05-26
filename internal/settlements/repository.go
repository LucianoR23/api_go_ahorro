// Package settlements: pagos de deuda entre miembros del hogar. No tocan
// payment_methods ni expenses — solo registran "X le pagó Y pesos a Z".
// El balance se recalcula on-demand contra el paquete balances.
package settlements

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// WithUser: match si user_id aparece como from O como to. Útil para
	// la vista "deudas con X" desde el frontend, donde no nos importa
	// quién pagó a quién, sólo que X estuvo involucrado.
	WithUser *uuid.UUID
	FromDate *time.Time
	ToDate   *time.Time
	Limit    int32
	Offset   int32
}

// List: query directa via pgx para soportar el filtro WithUser (OR)
// sin regenerar sqlc.
func (r *Repository) List(ctx context.Context, f ListFilter) ([]domain.SettlementPayment, error) {
	args := []any{f.HouseholdID}
	where := []string{"household_id = $1"}
	add := func(clause string, val any) {
		args = append(args, val)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.FromUser != nil {
		add("from_user = $%d", *f.FromUser)
	}
	if f.ToUser != nil {
		add("to_user = $%d", *f.ToUser)
	}
	if f.WithUser != nil {
		args = append(args, *f.WithUser)
		where = append(where, fmt.Sprintf("(from_user = $%d OR to_user = $%d)", len(args), len(args)))
	}
	if f.FromDate != nil {
		add("paid_at >= $%d", pgtype.Date{Time: *f.FromDate, Valid: true})
	}
	if f.ToDate != nil {
		add("paid_at <= $%d", pgtype.Date{Time: *f.ToDate, Valid: true})
	}

	args = append(args, f.Limit, f.Offset)
	sql := fmt.Sprintf(
		"SELECT id, household_id, from_user, to_user, amount_base, base_currency, note, paid_at, created_at FROM settlement_payments WHERE %s ORDER BY paid_at DESC, created_at DESC LIMIT $%d OFFSET $%d",
		strings.Join(where, " AND "), len(args)-1, len(args),
	)
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("settlements.List: %w", err)
	}
	defer rows.Close()
	var out []domain.SettlementPayment
	for rows.Next() {
		var sp sqlcgen.SettlementPayment
		if err := rows.Scan(
			&sp.ID, &sp.HouseholdID, &sp.FromUser, &sp.ToUser,
			&sp.AmountBase, &sp.BaseCurrency, &sp.Note, &sp.PaidAt, &sp.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("settlements.List scan: %w", err)
		}
		out = append(out, toDomain(sp))
	}
	return out, rows.Err()
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
