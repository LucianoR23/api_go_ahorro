package incomes

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Move takes an income to another space of the member who received it (plan
// 9.1), for example a salary loaded in the hogar back to Mi plata. Incomes
// don't split, so only the space changes; its recurring series stays behind.
func (s *Service) Move(ctx context.Context, householdID, id, actor, target uuid.UUID) (domain.Income, error) {
	inc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.Income{}, err
	}
	if inc.HouseholdID != householdID {
		return domain.Income{}, domain.ErrNotFound
	}
	if inc.ReceivedBy != actor {
		return domain.Income{}, domain.ErrForbidden
	}
	if target == householdID {
		return domain.Income{}, domain.NewValidationError("householdId", "el ingreso ya está en ese espacio")
	}
	ok, err := s.households.IsMember(ctx, target, actor)
	if err != nil {
		return domain.Income{}, err
	}
	if !ok {
		return domain.Income{}, domain.ErrNotFound
	}
	return s.repo.Move(ctx, id, target)
}

// Move changes the income's space.
func (r *Repository) Move(ctx context.Context, id, target uuid.UUID) (domain.Income, error) {
	row, err := r.q.MoveIncome(ctx, sqlcgen.MoveIncomeParams{ID: id, TargetHouseholdID: target})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Income{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Income{}, fmt.Errorf("incomes.Move: %w", err)
	}
	return toIncome(row), nil
}
