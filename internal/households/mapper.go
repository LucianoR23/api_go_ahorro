package households

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

func toDomain(h sqlcgen.Household) domain.Household {
	d := domain.Household{
		ID:           h.ID,
		Name:         h.Name,
		BaseCurrency: h.BaseCurrency,
		CreatedBy:    h.CreatedBy,
		CreatedAt:    h.CreatedAt.Time,
		UpdatedAt:    h.UpdatedAt.Time,
	}
	if h.DeletedAt.Valid {
		t := h.DeletedAt.Time
		d.DeletedAt = &t
	}
	return d
}

func toDomainMember(m sqlcgen.HouseholdMember) domain.HouseholdMember {
	return domain.HouseholdMember{
		HouseholdID: m.HouseholdID,
		UserID:      m.UserID,
		Role:        domain.Role(m.Role),
		JoinedAt:    m.JoinedAt.Time,
	}
}

// isUniqueViolation: duplica el helper de internal/users.
// TODO: si un tercer paquete lo necesita, mover a internal/db/pgerr.go.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
