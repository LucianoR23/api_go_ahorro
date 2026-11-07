package auth

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// AccountService runs account lifecycle operations that cross domains.
// Today: deletion.
//
// Soft delete instead of a hard delete because many FKs point at users with
// ON DELETE RESTRICT (expenses.created_by, settlements.from_user/to_user,
// incomes.received_by, recurring_expenses.created_by); deleting the row
// would break those households' history.
type AccountService struct {
	store  accountStore
	logger *slog.Logger
}

// accountStore deletes the account in one transaction and returns the
// active households the user still owns (nothing changes when above zero).
type accountStore interface {
	DeleteAccount(ctx context.Context, userID uuid.UUID) (owned int64, err error)
}

func NewAccountService(store accountStore, logger *slog.Logger) *AccountService {
	return &AccountService{store: store, logger: logger}
}

// SoftDelete deletes the user's account: memberships, push subscriptions and
// sessions go, the user is marked deleted and the email anonymized, all or
// nothing (plan 0.8). It is refused while the user owns an active household
// (they must transfer or delete it first).
func (s *AccountService) SoftDelete(ctx context.Context, userID uuid.UUID) error {
	owned, err := s.store.DeleteAccount(ctx, userID)
	if err != nil {
		return err
	}
	if owned > 0 {
		return fmt.Errorf(
			"aún sos owner de %d hogar(es): transferí la propiedad o borralos antes de dar de baja tu cuenta: %w",
			owned, domain.ErrConflict,
		)
	}
	s.logger.InfoContext(ctx, "cuenta dada de baja (soft delete)", "user_id", userID)
	return nil
}
