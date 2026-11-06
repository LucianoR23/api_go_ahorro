// Package ownership checks that ids sent by a client belong where they claim
// (plan 0.6): a category to the household, a payment method to the user.
// Without it, a member could file an expense under another household's
// category or record money on someone else's account.
package ownership

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// errNotWired: services fail closed if the checker was never wired.
var errNotWired = errors.New("ownership: checker no configurado")

type Checker struct {
	q *sqlcgen.Queries
}

func NewChecker(pool *pgxpool.Pool) *Checker {
	return &Checker{q: sqlcgen.New(pool)}
}

// Category: nil is fine (no category); otherwise it must belong to the
// household, or it is a 422 on categoryId.
func (c *Checker) Category(ctx context.Context, categoryID *uuid.UUID, householdID uuid.UUID) error {
	if categoryID == nil {
		return nil
	}
	if c == nil {
		return errNotWired
	}
	ok, err := c.q.CategoryInHousehold(ctx, sqlcgen.CategoryInHouseholdParams{ID: *categoryID, HouseholdID: householdID})
	if err != nil {
		return fmt.Errorf("ownership.Category: %w", err)
	}
	if !ok {
		return domain.NewValidationError("categoryId", "la categoría no es de este hogar")
	}
	return nil
}

// PaymentMethod: nil is fine (none); otherwise it must belong to userID, or it
// is a 404 (same as expenses: whether someone else's method exists is not
// revealed).
func (c *Checker) PaymentMethod(ctx context.Context, paymentMethodID *uuid.UUID, userID uuid.UUID) error {
	if paymentMethodID == nil {
		return nil
	}
	if c == nil {
		return errNotWired
	}
	ok, err := c.q.PaymentMethodOwnedBy(ctx, sqlcgen.PaymentMethodOwnedByParams{ID: *paymentMethodID, OwnerUserID: userID})
	if err != nil {
		return fmt.Errorf("ownership.PaymentMethod: %w", err)
	}
	if !ok {
		return domain.ErrNotFound
	}
	return nil
}
