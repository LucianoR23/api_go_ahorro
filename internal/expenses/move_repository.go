package expenses

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

// MoveBundle is what Service.Move writes: the target space, the category it
// matched there, and the new shares per installment id (empty off a hogar).
type MoveBundle struct {
	ExpenseID         uuid.UUID
	TargetHouseholdID uuid.UUID
	CategoryID        *uuid.UUID
	IsShared          bool
	Shares            map[uuid.UUID][]domain.InstallmentShare
}

// MoveTx moves an expense and its installments to another space in one
// transaction: the old shares go, the expense row changes household (the
// installments follow by ON UPDATE CASCADE) and the new shares go in.
func (r *Repository) MoveTx(ctx context.Context, b MoveBundle) (domain.ExpenseDetail, error) {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		qTx := r.q.WithTx(tx)
		if err := qTx.DeleteSharesByExpense(ctx, b.ExpenseID); err != nil {
			return fmt.Errorf("delete shares: %w", err)
		}
		if _, err := qTx.MoveExpense(ctx, sqlcgen.MoveExpenseParams{
			ID:                b.ExpenseID,
			TargetHouseholdID: b.TargetHouseholdID,
			CategoryID:        b.CategoryID,
			IsShared:          b.IsShared,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return domain.ErrNotFound
			}
			return fmt.Errorf("move expense: %w", err)
		}
		for installmentID, shares := range b.Shares {
			for _, sh := range shares {
				owed, err := numericFromDecimal(sh.AmountBaseOwed, 2)
				if err != nil {
					return fmt.Errorf("share amount: %w", err)
				}
				if err := qTx.CreateInstallmentShare(ctx, sqlcgen.CreateInstallmentShareParams{
					InstallmentID:  installmentID,
					UserID:         sh.UserID,
					AmountBaseOwed: owed,
				}); err != nil {
					return fmt.Errorf("insert share: %w", err)
				}
			}
		}
		return nil
	})
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	return r.GetDetail(ctx, b.ExpenseID)
}

// Category is a category of a space, with its parent id.
type Category struct {
	ID       uuid.UUID
	Name     string
	ParentID *uuid.UUID
}

// ListCategories lists a space's categories.
func (r *Repository) ListCategories(ctx context.Context, householdID uuid.UUID) ([]Category, error) {
	rows, err := r.q.ListCategoriesByHousehold(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("expenses.ListCategories: %w", err)
	}
	out := make([]Category, 0, len(rows))
	for _, c := range rows {
		out = append(out, Category{ID: c.ID, Name: c.Name, ParentID: c.ParentID})
	}
	return out, nil
}
