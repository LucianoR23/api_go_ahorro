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

	"github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

// CategoryFields are the writable fields of a category, already validated.
type CategoryFields struct {
	Name     string
	Icon     string
	Color    string
	ParentID *uuid.UUID
}

type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// Create inserta una categoría en el hogar. UNIQUE(household_id, name)
// se traduce a ErrConflict si ya existe otra con ese nombre.
func (r *Repository) Create(ctx context.Context, householdID uuid.UUID, in CategoryFields) (domain.Category, error) {
	row, err := r.q.CreateCategory(ctx, sqlcgen.CreateCategoryParams{
		HouseholdID: householdID,
		Name:        in.Name,
		Icon:        in.Icon,
		Color:       in.Color,
		ParentID:    in.ParentID,
	})
	if err != nil {
		return domain.Category{}, writeErr("categories.Create", err)
	}
	return toDomain(row), nil
}

// SeedDefaultsTx inserta las DefaultCategories dentro de la tx recibida.
// Se llama desde households.Repository.CreateWithOwner para que el
// bootstrap del hogar sea atómico.
func (r *Repository) SeedDefaultsTx(ctx context.Context, tx pgx.Tx, householdID uuid.UUID) error {
	qTx := r.q.WithTx(tx)
	for _, seed := range domain.DefaultCategories {
		parent, err := qTx.CreateCategory(ctx, sqlcgen.CreateCategoryParams{
			HouseholdID: householdID,
			Name:        seed.Name,
			Icon:        seed.Icon,
			Color:       seed.Color,
		})
		if err != nil {
			return fmt.Errorf("seed default category %q: %w", seed.Name, err)
		}
		for _, child := range seed.Children {
			if _, err := qTx.CreateCategory(ctx, sqlcgen.CreateCategoryParams{
				HouseholdID: householdID,
				Name:        child.Name,
				Icon:        child.Icon,
				Color:       seed.Color,
				ParentID:    &parent.ID,
			}); err != nil {
				return fmt.Errorf("seed default category %q: %w", child.Name, err)
			}
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

func (r *Repository) Update(ctx context.Context, id uuid.UUID, in CategoryFields) (domain.Category, error) {
	row, err := r.q.UpdateCategory(ctx, sqlcgen.UpdateCategoryParams{
		ID:       id,
		Name:     in.Name,
		Icon:     in.Icon,
		Color:    in.Color,
		ParentID: in.ParentID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Category{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Category{}, writeErr("categories.Update", err)
	}
	return toDomain(row), nil
}

// HasChildren reports whether the category has subcategories.
func (r *Repository) HasChildren(ctx context.Context, id uuid.UUID) (bool, error) {
	has, err := r.q.CategoryHasChildren(ctx, &id)
	if err != nil {
		return false, fmt.Errorf("categories.HasChildren: %w", err)
	}
	return has, nil
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
		ParentID:    c.ParentID,
		Name:        c.Name,
		Icon:        c.Icon,
		Color:       c.Color,
		CreatedAt:   c.CreatedAt.Time,
		UpdatedAt:   c.UpdatedAt.Time,
	}
}

// writeErr maps constraint violations of a create or update: a repeated
// name, and the one-level rule when a concurrent write beat the service's
// own check.
func writeErr(op string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505":
			return fmt.Errorf("nombre de categoría repetido: %w", domain.ErrConflict)
		case pgErr.ConstraintName == "categories_one_level", pgErr.ConstraintName == "categories_parent_fk":
			return domain.NewValidationError("parentId", "no puede ser la categoría madre")
		}
	}
	return fmt.Errorf("%s: %w", op, err)
}
