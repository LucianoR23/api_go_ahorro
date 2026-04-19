// Package splitrules: pesos por miembro para dividir gastos compartidos.
// Tabla household_split_rules (household_id, user_id, weight).
// weight decimal libre; al repartir un share, el service normaliza
// dividiendo por SUM(weights).
package splitrules

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// Upsert inserta o actualiza el peso de un miembro (fuera de tx).
// Usado por PATCH /households/{id}/split.
func (r *Repository) Upsert(ctx context.Context, householdID, userID uuid.UUID, weight float64) (domain.SplitRule, error) {
	return r.upsert(ctx, r.q, householdID, userID, weight)
}

// UpsertTx inserta o actualiza dentro de una tx existente. Usado por el
// hook de bootstrap desde households.Repository (CreateWithOwner y AddMember).
func (r *Repository) UpsertTx(ctx context.Context, tx pgx.Tx, householdID, userID uuid.UUID, weight float64) error {
	_, err := r.upsert(ctx, r.q.WithTx(tx), householdID, userID, weight)
	return err
}

func (r *Repository) upsert(ctx context.Context, q *sqlcgen.Queries, householdID, userID uuid.UUID, weight float64) (domain.SplitRule, error) {
	w, err := db.NumericFromFloat(weight, 4)
	if err != nil {
		return domain.SplitRule{}, fmt.Errorf("splitrules.upsert weight: %w", err)
	}
	row, err := q.UpsertSplitRule(ctx, sqlcgen.UpsertSplitRuleParams{
		HouseholdID: householdID,
		UserID:      userID,
		Weight:      w,
	})
	if err != nil {
		return domain.SplitRule{}, fmt.Errorf("splitrules.Upsert: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) Get(ctx context.Context, householdID, userID uuid.UUID) (domain.SplitRule, error) {
	row, err := r.q.GetSplitRule(ctx, sqlcgen.GetSplitRuleParams{
		HouseholdID: householdID,
		UserID:      userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SplitRule{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.SplitRule{}, fmt.Errorf("splitrules.Get: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) ListByHousehold(ctx context.Context, householdID uuid.UUID) ([]domain.SplitRule, error) {
	rows, err := r.q.ListSplitRulesByHousehold(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("splitrules.ListByHousehold: %w", err)
	}
	out := make([]domain.SplitRule, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

func (r *Repository) Delete(ctx context.Context, householdID, userID uuid.UUID) error {
	if err := r.q.DeleteSplitRule(ctx, sqlcgen.DeleteSplitRuleParams{
		HouseholdID: householdID,
		UserID:      userID,
	}); err != nil {
		return fmt.Errorf("splitrules.Delete: %w", err)
	}
	return nil
}

func toDomain(row sqlcgen.HouseholdSplitRule) domain.SplitRule {
	return domain.SplitRule{
		HouseholdID: row.HouseholdID,
		UserID:      row.UserID,
		Weight:      db.FloatFromNumeric(row.Weight),
		UpdatedAt:   row.UpdatedAt.Time,
	}
}
