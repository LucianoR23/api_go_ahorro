package households

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// InvitesRepository maneja household_invites. Separado de Repository para
// mantener los archivos manejables — misma DB, mismo sqlc.Queries.
type InvitesRepository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewInvitesRepository(pool *pgxpool.Pool) *InvitesRepository {
	return &InvitesRepository{pool: pool, q: sqlcgen.New(pool)}
}

func (r *InvitesRepository) Create(ctx context.Context, householdID uuid.UUID, email, tokenHash string, invitedBy uuid.UUID, expiresAt time.Time) (domain.HouseholdInvite, error) {
	row, err := r.q.CreateHouseholdInvite(ctx, sqlcgen.CreateHouseholdInviteParams{
		HouseholdID: householdID,
		Email:       email,
		TokenHash:   tokenHash,
		InvitedBy:   invitedBy,
		ExpiresAt:   pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if err != nil {
		if isUniqueViolation(err) {
			return domain.HouseholdInvite{}, fmt.Errorf("ya hay una invitación pendiente para ese email: %w", domain.ErrConflict)
		}
		return domain.HouseholdInvite{}, fmt.Errorf("invites.Create: %w", err)
	}
	return toInviteDomain(row), nil
}

// GetByTokenHash busca por hash exacto (SHA-256 hex).
func (r *InvitesRepository) GetByTokenHash(ctx context.Context, tokenHash string) (domain.HouseholdInvite, error) {
	row, err := r.q.GetHouseholdInviteByTokenHash(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.HouseholdInvite{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.HouseholdInvite{}, fmt.Errorf("invites.GetByTokenHash: %w", err)
	}
	return toInviteDomain(row), nil
}

func (r *InvitesRepository) GetByID(ctx context.Context, id uuid.UUID) (domain.HouseholdInvite, error) {
	row, err := r.q.GetHouseholdInviteByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.HouseholdInvite{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.HouseholdInvite{}, fmt.Errorf("invites.GetByID: %w", err)
	}
	return toInviteDomain(row), nil
}

func (r *InvitesRepository) ListPending(ctx context.Context, householdID uuid.UUID) ([]domain.HouseholdInvite, error) {
	rows, err := r.q.ListPendingInvitesForHousehold(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("invites.ListPending: %w", err)
	}
	out := make([]domain.HouseholdInvite, len(rows))
	for i, row := range rows {
		out[i] = toInviteDomain(row)
	}
	return out, nil
}

// MarkAccepted + AddMember en una sola transacción. Si el add miembro
// falla (ej: ya era miembro por otra vía), rollback de la aceptación.
// memberHook siembra split_rule weight=1.0 para el recién agregado.
func (r *InvitesRepository) AcceptAndAddMember(ctx context.Context, inviteID, userID uuid.UUID, memberHook AfterMemberHook) (domain.HouseholdInvite, domain.HouseholdMember, error) {
	var invite domain.HouseholdInvite
	var member domain.HouseholdMember

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		qTx := r.q.WithTx(tx)

		accepted, err := qTx.MarkInviteAccepted(ctx, sqlcgen.MarkInviteAcceptedParams{
			ID:         inviteID,
			AcceptedBy: &userID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// No matcheó: ya estaba aceptada, revocada o expirada.
			return domain.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("invites.AcceptAndAddMember.MarkAccepted: %w", err)
		}

		m, err := qTx.AddHouseholdMember(ctx, sqlcgen.AddHouseholdMemberParams{
			HouseholdID: accepted.HouseholdID,
			UserID:      userID,
			Role:        string(domain.RoleMember),
		})
		if err != nil {
			if isUniqueViolation(err) {
				// Ya era miembro — devolvemos conflict para que el handler
				// no cuente como éxito.
				return fmt.Errorf("ya sos miembro del hogar: %w", domain.ErrConflict)
			}
			return fmt.Errorf("invites.AcceptAndAddMember.AddMember: %w", err)
		}

		if memberHook != nil {
			if err := memberHook(ctx, tx, accepted.HouseholdID, userID); err != nil {
				return fmt.Errorf("invites.AcceptAndAddMember.hook: %w", err)
			}
		}

		invite = toInviteDomain(accepted)
		member = toDomainMember(m)
		return nil
	})
	if err != nil {
		return domain.HouseholdInvite{}, domain.HouseholdMember{}, err
	}
	return invite, member, nil
}

// RefreshToken rota el token_hash y extiende expires_at de una invitación
// pendiente. Si la invite ya fue aceptada o revocada, pgx.ErrNoRows →
// ErrConflict (el caller sabrá que no puede reenviar).
func (r *InvitesRepository) RefreshToken(ctx context.Context, id uuid.UUID, tokenHash string, expiresAt time.Time) (domain.HouseholdInvite, error) {
	row, err := r.q.RefreshInviteToken(ctx, sqlcgen.RefreshInviteTokenParams{
		ID:        id,
		TokenHash: tokenHash,
		ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.HouseholdInvite{}, domain.ErrConflict
	}
	if err != nil {
		return domain.HouseholdInvite{}, fmt.Errorf("invites.RefreshToken: %w", err)
	}
	return toInviteDomain(row), nil
}

// Revoke marca la invitación como revocada. Solo sirve si está pendiente.
func (r *InvitesRepository) Revoke(ctx context.Context, id uuid.UUID) (domain.HouseholdInvite, error) {
	row, err := r.q.RevokeInvite(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.HouseholdInvite{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.HouseholdInvite{}, fmt.Errorf("invites.Revoke: %w", err)
	}
	return toInviteDomain(row), nil
}

func toInviteDomain(r sqlcgen.HouseholdInvite) domain.HouseholdInvite {
	inv := domain.HouseholdInvite{
		ID:          r.ID,
		HouseholdID: r.HouseholdID,
		Email:       r.Email,
		InvitedBy:   r.InvitedBy,
		ExpiresAt:   r.ExpiresAt.Time,
		CreatedAt:   r.CreatedAt.Time,
	}
	if r.AcceptedAt.Valid {
		t := r.AcceptedAt.Time
		inv.AcceptedAt = &t
	}
	if r.AcceptedBy != nil {
		inv.AcceptedBy = r.AcceptedBy
	}
	if r.RevokedAt.Valid {
		t := r.RevokedAt.Time
		inv.RevokedAt = &t
	}
	return inv
}
