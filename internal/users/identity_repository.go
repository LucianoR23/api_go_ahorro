package users

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

// IdentityRepository persiste vinculaciones OAuth en la tabla user_identities.
// Cada fila representa "este (provider, subject) pertenece a este user".
//
// Separado del Repository principal por cohesión: identity vive en su propia
// tabla y solo el service de auth la consulta. Tener un repo aparte mantiene
// las dependencias del Repository de users acotadas a esa tabla.
type IdentityRepository struct {
	q *sqlcgen.Queries
}

func NewIdentityRepository(pool *pgxpool.Pool) *IdentityRepository {
	return &IdentityRepository{q: sqlcgen.New(pool)}
}

// Get devuelve el user_id vinculado a (provider, subject), o ErrNotFound
// si no existe. El service usa NotFound como señal para arrancar el flujo
// de auto-vinculación por email o crear un user nuevo.
func (r *IdentityRepository) Get(ctx context.Context, provider, subject string) (uuid.UUID, error) {
	row, err := r.q.GetUserIdentity(ctx, sqlcgen.GetUserIdentityParams{
		Provider: provider,
		Subject:  subject,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, domain.ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("users.IdentityRepository.Get: %w", err)
	}
	return row.UserID, nil
}

// Create inserta la vinculación. Conflicto en PK (provider, subject) ya
// existente → ErrConflict (race en concurrent login, casi imposible en
// la práctica pero correcto manejarlo).
func (r *IdentityRepository) Create(ctx context.Context, provider, subject string, userID uuid.UUID, email string) error {
	err := r.q.CreateUserIdentity(ctx, sqlcgen.CreateUserIdentityParams{
		Provider: provider,
		Subject:  subject,
		UserID:   userID,
		Email:    email,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("identity ya vinculada: %w", domain.ErrConflict)
		}
		return fmt.Errorf("users.IdentityRepository.Create: %w", err)
	}
	return nil
}
