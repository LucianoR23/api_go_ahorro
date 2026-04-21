package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// PasswordResetRepository persiste los tokens de reset. Guardamos solo el
// SHA-256 del token (mismo patrón que household_invites).
type PasswordResetRepository struct {
	q *sqlcgen.Queries
}

func NewPasswordResetRepository(pool *pgxpool.Pool) *PasswordResetRepository {
	return &PasswordResetRepository{q: sqlcgen.New(pool)}
}

// PasswordReset es el struct de dominio local al paquete auth (no lo
// exponemos a domain/ porque es un detalle interno del flujo).
type PasswordReset struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

func (r *PasswordResetRepository) Create(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) (PasswordReset, error) {
	row, err := r.q.CreatePasswordReset(ctx, sqlcgen.CreatePasswordResetParams{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return PasswordReset{}, fmt.Errorf("password_reset.Create: %w", err)
	}
	return toResetDomain(row), nil
}

func (r *PasswordResetRepository) GetByTokenHash(ctx context.Context, tokenHash string) (PasswordReset, error) {
	row, err := r.q.GetPasswordResetByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return PasswordReset{}, domain.ErrNotFound
	}
	if err != nil {
		return PasswordReset{}, fmt.Errorf("password_reset.GetByTokenHash: %w", err)
	}
	return toResetDomain(row), nil
}

// MarkUsed: single-use condicional. Si otro request ya lo usó, devuelve
// ErrConflict (pgx.ErrNoRows porque el WHERE no matchea).
func (r *PasswordResetRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.q.MarkPasswordResetUsed(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return fmt.Errorf("password_reset.MarkUsed: %w", err)
	}
	return nil
}

// InvalidateActiveForUser marca como usados todos los resets activos del
// user. Se llama al emitir uno nuevo (así el último mail invalida al
// anterior) y opcionalmente al completar change-password.
func (r *PasswordResetRepository) InvalidateActiveForUser(ctx context.Context, userID uuid.UUID) error {
	if err := r.q.InvalidateActivePasswordResetsForUser(ctx, userID); err != nil {
		return fmt.Errorf("password_reset.InvalidateActiveForUser: %w", err)
	}
	return nil
}

func toResetDomain(r sqlcgen.PasswordReset) PasswordReset {
	pr := PasswordReset{
		ID:        r.ID,
		UserID:    r.UserID,
		TokenHash: r.TokenHash,
		ExpiresAt: r.ExpiresAt.Time,
	}
	if r.UsedAt.Valid {
		t := r.UsedAt.Time
		pr.UsedAt = &t
	}
	return pr
}
