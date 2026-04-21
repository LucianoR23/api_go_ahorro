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

// EmailVerification es el struct local del flujo. No lo exponemos a domain/
// porque es un detalle interno (igual que PasswordReset).
type EmailVerification struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

type EmailVerificationRepository struct {
	q *sqlcgen.Queries
}

func NewEmailVerificationRepository(pool *pgxpool.Pool) *EmailVerificationRepository {
	return &EmailVerificationRepository{q: sqlcgen.New(pool)}
}

func (r *EmailVerificationRepository) Create(ctx context.Context, userID uuid.UUID, tokenHash string, expiresAt time.Time) (EmailVerification, error) {
	row, err := r.q.CreateEmailVerification(ctx, sqlcgen.CreateEmailVerificationParams{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		return EmailVerification{}, fmt.Errorf("email_verification.Create: %w", err)
	}
	return toEmailVerificationDomain(row), nil
}

func (r *EmailVerificationRepository) GetByTokenHash(ctx context.Context, tokenHash string) (EmailVerification, error) {
	row, err := r.q.GetEmailVerificationByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmailVerification{}, domain.ErrNotFound
	}
	if err != nil {
		return EmailVerification{}, fmt.Errorf("email_verification.GetByTokenHash: %w", err)
	}
	return toEmailVerificationDomain(row), nil
}

// MarkUsed: single-use condicional. Race → ErrConflict.
func (r *EmailVerificationRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	_, err := r.q.MarkEmailVerificationUsed(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return fmt.Errorf("email_verification.MarkUsed: %w", err)
	}
	return nil
}

func (r *EmailVerificationRepository) InvalidateActiveForUser(ctx context.Context, userID uuid.UUID) error {
	if err := r.q.InvalidateActiveEmailVerificationsForUser(ctx, userID); err != nil {
		return fmt.Errorf("email_verification.InvalidateActiveForUser: %w", err)
	}
	return nil
}

func toEmailVerificationDomain(r sqlcgen.EmailVerification) EmailVerification {
	ev := EmailVerification{
		ID:        r.ID,
		UserID:    r.UserID,
		TokenHash: r.TokenHash,
		ExpiresAt: r.ExpiresAt.Time,
	}
	if r.UsedAt.Valid {
		t := r.UsedAt.Time
		ev.UsedAt = &t
	}
	return ev
}
