package auth

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// AccountService agrupa operaciones de ciclo de vida de la cuenta que no
// caben en auth.Service (orquestación cross-dominio). Hoy: soft delete.
//
// Soft delete en vez de hard delete porque muchas FKs apuntan a users con
// ON DELETE RESTRICT (expenses.created_by, settlements.from_user/to_user,
// incomes.received_by, recurring_expenses.created_by). Borrar la fila rompe
// el historial de esos hogares.
//
// En su lugar:
//   - bloqueamos si aún es owner de algún hogar (debe transferir antes);
//   - removemos todas sus memberships (deja de ser miembro en todos lados);
//   - borramos sus push subscriptions (nada que notificar);
//   - marcamos deleted_at y anonimizamos el email (libera el UNIQUE para
//     que el ex-user pueda re-registrarse con el mismo email si quiere).
type AccountService struct {
	users      accountUserRepo
	households accountHouseholdRepo
	push       accountPushRepo
	logger     *slog.Logger
}

// accountUserRepo: solo soft delete + count owners.
type accountUserRepo interface {
	SoftDelete(ctx context.Context, id uuid.UUID) error
	CountHouseholdsOwned(ctx context.Context, userID uuid.UUID) (int64, error)
}

// accountHouseholdRepo: solo remover todas las memberships del user.
type accountHouseholdRepo interface {
	RemoveAllMembershipsForUser(ctx context.Context, userID uuid.UUID) error
}

// accountPushRepo: borra todas las subs del user. Opcional (nil-safe).
type accountPushRepo interface {
	DeleteAllForUser(ctx context.Context, userID uuid.UUID) error
}

func NewAccountService(users accountUserRepo, households accountHouseholdRepo, push accountPushRepo, logger *slog.Logger) *AccountService {
	return &AccountService{users: users, households: households, push: push, logger: logger}
}

// SoftDelete ejecuta la baja del user. No es transaccional a nivel DB
// (los tres repos operan con su propio pool) pero sí reversible en caso
// de que algo falle en el medio:
//   - si falla remove memberships: el user sigue intacto (no se tocó).
//   - si falla delete push subs: memberships ya se fueron; logueamos y
//     continuamos — las subs no son críticas y el endpoint idempotente
//     puede limpiarlas después.
//   - si falla soft delete: memberships y subs ya se fueron; al reintentar
//     el user sigue logueado y vuelve a ejecutar (idempotente).
//
// La ventana de inconsistencia es chica en la práctica; no justifica
// abrir una tx sobre múltiples repos que hoy usan el mismo pool.
func (s *AccountService) SoftDelete(ctx context.Context, userID uuid.UUID) error {
	owned, err := s.users.CountHouseholdsOwned(ctx, userID)
	if err != nil {
		return err
	}
	if owned > 0 {
		return fmt.Errorf(
			"aún sos owner de %d hogar(es): transferí la propiedad o borralos antes de dar de baja tu cuenta: %w",
			owned, domain.ErrConflict,
		)
	}

	if err := s.households.RemoveAllMembershipsForUser(ctx, userID); err != nil {
		return err
	}

	if s.push != nil {
		if err := s.push.DeleteAllForUser(ctx, userID); err != nil {
			// No abortamos: el user puede darse de baja igual. Las subs
			// quedan huérfanas hasta que el browser se re-suscriba (no pasa).
			s.logger.WarnContext(ctx, "no se pudieron borrar push subs del user",
				"user_id", userID, "error", err)
		}
	}

	if err := s.users.SoftDelete(ctx, userID); err != nil {
		return err
	}
	s.logger.InfoContext(ctx, "cuenta dada de baja (soft delete)", "user_id", userID)
	return nil
}

