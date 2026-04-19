// Package households: repository y mapeos para el dominio Household.
//
// Particularidad: crear un hogar requiere insertar en 2 tablas
// (households + household_members con rol owner). Para que sea atómico,
// el repo expone CreateWithOwner que usa una tx interna.
package households

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// Repository maneja households y household_members en DB.
type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// CreateWithOwner inserta household + household_members(owner) en una sola
// transacción. Si falla cualquiera de los dos inserts, rollback automático.
//
// pgx.BeginFunc maneja el commit/rollback por nosotros: si la función
// devuelve error, rollback; si devuelve nil, commit. Es el patrón oficial
// para evitar el clásico bug de olvidar rollback en early-return.
func (r *Repository) CreateWithOwner(ctx context.Context, name, baseCurrency string, ownerID uuid.UUID) (domain.Household, error) {
	var created domain.Household

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		// qTx apunta a la misma Queries generada pero ejecuta dentro de la tx.
		qTx := r.q.WithTx(tx)

		h, err := qTx.CreateHousehold(ctx, sqlcgen.CreateHouseholdParams{
			Name:         name,
			BaseCurrency: baseCurrency,
			CreatedBy:    ownerID,
		})
		if err != nil {
			return fmt.Errorf("crear household: %w", err)
		}

		if _, err := qTx.AddHouseholdMember(ctx, sqlcgen.AddHouseholdMemberParams{
			HouseholdID: h.ID,
			UserID:      ownerID,
			Role:        string(domain.RoleOwner),
		}); err != nil {
			return fmt.Errorf("agregar owner como member: %w", err)
		}

		created = toDomain(h)
		return nil
	})
	if err != nil {
		return domain.Household{}, err
	}
	return created, nil
}

// GetByID devuelve el household o ErrNotFound. No valida membresía:
// eso lo hace el middleware antes de llegar al service.
func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.Household, error) {
	h, err := r.q.GetHouseholdByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Household{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Household{}, fmt.Errorf("households.GetByID: %w", err)
	}
	return toDomain(h), nil
}

// ListForUser devuelve todos los hogares donde el user es miembro.
func (r *Repository) ListForUser(ctx context.Context, userID uuid.UUID) ([]domain.Household, error) {
	rows, err := r.q.ListHouseholdsForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("households.ListForUser: %w", err)
	}
	out := make([]domain.Household, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

// Update cambia nombre y moneda. created_by y timestamps no se tocan.
func (r *Repository) Update(ctx context.Context, id uuid.UUID, name, baseCurrency string) (domain.Household, error) {
	h, err := r.q.UpdateHousehold(ctx, sqlcgen.UpdateHouseholdParams{
		ID:           id,
		Name:         name,
		BaseCurrency: baseCurrency,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Household{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Household{}, fmt.Errorf("households.Update: %w", err)
	}
	return toDomain(h), nil
}

// Delete borra el hogar. ON DELETE CASCADE en household_members
// limpia la membresía automáticamente. Futuros: también cascadeará
// expenses, goals, etc. cuando esas tablas apunten acá.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.q.DeleteHousehold(ctx, id); err != nil {
		return fmt.Errorf("households.Delete: %w", err)
	}
	return nil
}

// ===================== membresía =====================

// IsMember devuelve true si el user pertenece al hogar.
// Usado por el middleware antes de dejar pasar un request con X-Household-ID.
func (r *Repository) IsMember(ctx context.Context, householdID, userID uuid.UUID) (bool, error) {
	ok, err := r.q.IsHouseholdMember(ctx, sqlcgen.IsHouseholdMemberParams{
		HouseholdID: householdID,
		UserID:      userID,
	})
	if err != nil {
		return false, fmt.Errorf("households.IsMember: %w", err)
	}
	return ok, nil
}

// GetMemberRole devuelve el rol del user en el household, o ErrNotFound
// si no es miembro. Usado cuando una operación requiere ser owner
// (editar, borrar, invitar).
func (r *Repository) GetMemberRole(ctx context.Context, householdID, userID uuid.UUID) (domain.Role, error) {
	row, err := r.q.GetHouseholdMemberRole(ctx, sqlcgen.GetHouseholdMemberRoleParams{
		HouseholdID: householdID,
		UserID:      userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("households.GetMemberRole: %w", err)
	}
	return domain.Role(row), nil
}

// AddMember agrega un user existente al household con rol 'member'.
// El user lo resuelve el service por email antes de llamar acá.
func (r *Repository) AddMember(ctx context.Context, householdID, userID uuid.UUID, role domain.Role) (domain.HouseholdMember, error) {
	row, err := r.q.AddHouseholdMember(ctx, sqlcgen.AddHouseholdMemberParams{
		HouseholdID: householdID,
		UserID:      userID,
		Role:        string(role),
	})
	if err != nil {
		// UNIQUE violation en (household_id, user_id) → ya es miembro.
		if isUniqueViolation(err) {
			return domain.HouseholdMember{}, fmt.Errorf("ya es miembro del hogar: %w", domain.ErrConflict)
		}
		return domain.HouseholdMember{}, fmt.Errorf("households.AddMember: %w", err)
	}
	return toDomainMember(row), nil
}

// RemoveMember elimina la membresía. No toca datos del user.
func (r *Repository) RemoveMember(ctx context.Context, householdID, userID uuid.UUID) error {
	if err := r.q.RemoveHouseholdMember(ctx, sqlcgen.RemoveHouseholdMemberParams{
		HouseholdID: householdID,
		UserID:      userID,
	}); err != nil {
		return fmt.Errorf("households.RemoveMember: %w", err)
	}
	return nil
}

// ListMembers devuelve los miembros con info del user (nombre, email)
// para que el frontend no tenga que hacer N+1 queries.
func (r *Repository) ListMembers(ctx context.Context, householdID uuid.UUID) ([]domain.HouseholdMemberDetail, error) {
	rows, err := r.q.ListHouseholdMembers(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("households.ListMembers: %w", err)
	}
	out := make([]domain.HouseholdMemberDetail, len(rows))
	for i, row := range rows {
		out[i] = domain.HouseholdMemberDetail{
			User: domain.User{
				ID:        row.User.ID,
				Email:     string(row.User.Email),
				Name:      row.User.Name,
				CreatedAt: row.User.CreatedAt.Time,
				UpdatedAt: row.User.UpdatedAt.Time,
			},
			Role:     domain.Role(row.Role),
			JoinedAt: row.JoinedAt.Time,
		}
	}
	return out, nil
}
