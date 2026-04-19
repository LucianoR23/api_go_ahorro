// Package categories: categorías de gasto por hogar.
package categories

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// Create inserta una categoría en el hogar. UNIQUE(household_id, name)
// se traduce a ErrConflict si ya existe otra con ese nombre.
func (r *Repository) Create(ctx context.Context, householdID uuid.UUID, name, icon, color string) (domain.Category, error) {
	row, err := r.q.CreateCategory(ctx, sqlcgen.CreateCategoryParams{
		HouseholdID: householdID,
		Name:        name,
		Icon:        icon,
		Color:       color,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Category{}, fmt.Errorf("nombre de categoría repetido: %w", domain.ErrConflict)
		}
		return domain.Category{}, fmt.Errorf("categories.Create: %w", err)
	}
	return toDomain(row), nil
}

// SeedDefaultsTx inserta las DefaultCategories dentro de la tx recibida.
// Se llama desde households.Repository.CreateWithOwner para que el
// bootstrap del hogar sea atómico.
func (r *Repository) SeedDefaultsTx(ctx context.Context, tx pgx.Tx, householdID uuid.UUID) error {
	qTx := r.q.WithTx(tx)
	for _, seed := range domain.DefaultCategories {
		if _, err := qTx.CreateCategory(ctx, sqlcgen.CreateCategoryParams{
			HouseholdID: householdID,
			Name:        seed.Name,
			Icon:        seed.Icon,
			Color:       seed.Color,
		}); err != nil {
			return fmt.Errorf("seed default category %q: %w", seed.Name, err)
		}
	}
	return nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.Category, error) {
	row, err := r.q.GetCategoryByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Category{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Category{}, fmt.Errorf("categories.GetByID: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) ListByHousehold(ctx context.Context, householdID uuid.UUID) ([]domain.Category, error) {
	rows, err := r.q.ListCategoriesByHousehold(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("categories.ListByHousehold: %w", err)
	}
	out := make([]domain.Category, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, name, icon, color string) (domain.Category, error) {
	row, err := r.q.UpdateCategory(ctx, sqlcgen.UpdateCategoryParams{
		ID:    id,
		Name:  name,
		Icon:  icon,
		Color: color,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Category{}, domain.ErrNotFound
	}
	if err != nil {
		if isUniqueViolation(err) {
			return domain.Category{}, fmt.Errorf("nombre de categoría repetido: %w", domain.ErrConflict)
		}
		return domain.Category{}, fmt.Errorf("categories.Update: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.q.DeleteCategory(ctx, id); err != nil {
		return fmt.Errorf("categories.Delete: %w", err)
	}
	return nil
}

func toDomain(c sqlcgen.Category) domain.Category {
	return domain.Category{
		ID:          c.ID,
		HouseholdID: c.HouseholdID,
		Name:        c.Name,
		Icon:        c.Icon,
		Color:       c.Color,
		CreatedAt:   c.CreatedAt.Time,
		UpdatedAt:   c.UpdatedAt.Time,
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
