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

// AfterCreateHook corre dentro de la misma transacción que creó el household.
// Se usa para sembrar datos por defecto (categorías, etc.) de forma atómica:
// si el hook falla, rollback de todo y el hogar no queda a medio armar.
type AfterCreateHook func(ctx context.Context, tx pgx.Tx, householdID uuid.UUID) error

// AfterMemberHook corre dentro de la tx justo después de insertar un member.
// Alcance por-user (a diferencia de AfterCreateHook que es por-household).
// Se usa para seedear la split_rule del miembro con weight=1.0 en bootstrap
// y en AddMember.
type AfterMemberHook func(ctx context.Context, tx pgx.Tx, householdID, userID uuid.UUID) error

// CreateWithOwner inserta household + household_members(owner) en una sola
// transacción. Si falla cualquiera de los dos inserts, rollback automático.
// afterCreate (opcional) corre dentro de la misma tx para bootstrap de
// datos default (categorías); si devuelve error, rollback completo.
//
// pgx.BeginFunc maneja el commit/rollback por nosotros: si la función
// devuelve error, rollback; si devuelve nil, commit. Es el patrón oficial
// para evitar el clásico bug de olvidar rollback en early-return.
func (r *Repository) CreateWithOwner(ctx context.Context, name, baseCurrency string, ownerID uuid.UUID, afterCreate AfterCreateHook, afterMember AfterMemberHook) (domain.Household, error) {
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

		// Hook por-member: seed split_rule del owner (weight=1.0).
		if afterMember != nil {
			if err := afterMember(ctx, tx, h.ID, ownerID); err != nil {
				return fmt.Errorf("after-member hook (owner): %w", err)
			}
		}

		if afterCreate != nil {
			if err := afterCreate(ctx, tx, h.ID); err != nil {
				return fmt.Errorf("after-create hook: %w", err)
			}
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

// ListAllIDs devuelve todos los IDs de hogares — usado por workers (insights,
// reports) que iteran el universo entero.
func (r *Repository) ListAllIDs(ctx context.Context) ([]uuid.UUID, error) {
	ids, err := r.q.ListAllHouseholdIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("households.ListAllIDs: %w", err)
	}
	return ids, nil
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
// afterMember (opcional) corre dentro de la misma tx para bootstrap por-user
// (split_rule weight=1.0). Si falla, rollback y el miembro no queda insertado.
func (r *Repository) AddMember(ctx context.Context, householdID, userID uuid.UUID, role domain.Role, afterMember AfterMemberHook) (domain.HouseholdMember, error) {
	var result domain.HouseholdMember
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		qTx := r.q.WithTx(tx)
		row, err := qTx.AddHouseholdMember(ctx, sqlcgen.AddHouseholdMemberParams{
			HouseholdID: householdID,
			UserID:      userID,
			Role:        string(role),
		})
		if err != nil {
			// UNIQUE violation en (household_id, user_id) → ya es miembro.
			if isUniqueViolation(err) {
				return fmt.Errorf("ya es miembro del hogar: %w", domain.ErrConflict)
			}
			return fmt.Errorf("households.AddMember: %w", err)
		}
		if afterMember != nil {
			if err := afterMember(ctx, tx, householdID, userID); err != nil {
				return fmt.Errorf("after-member hook: %w", err)
			}
		}
		result = toDomainMember(row)
		return nil
	})
	if err != nil {
		return domain.HouseholdMember{}, err
	}
	return result, nil
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
				FirstName: row.User.FirstName,
				LastName:  row.User.LastName,
				CreatedAt: row.User.CreatedAt.Time,
				UpdatedAt: row.User.UpdatedAt.Time,
			},
			Role:     domain.Role(row.Role),
			JoinedAt: row.JoinedAt.Time,
		}
	}
	return out, nil
}
