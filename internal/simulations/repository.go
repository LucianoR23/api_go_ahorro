package simulations

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
)

// Repository: the one lookup the simulator does itself.
type Repository struct {
	q *sqlcgen.Queries
}

func NewRepository(db sqlcgen.DBTX) *Repository { return &Repository{q: sqlcgen.New(db)} }

// CategoryParent returns a category's parent (nil for a top-level one): a
// limit on the parent counts its subcategories too (plan 1.4).
func (r *Repository) CategoryParent(ctx context.Context, id uuid.UUID) (*uuid.UUID, error) {
	c, err := r.q.GetCategoryByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("simulations.CategoryParent: %w", err)
	}
	return c.ParentID, nil
}
