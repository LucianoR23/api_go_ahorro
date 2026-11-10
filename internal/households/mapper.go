package households

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

func toDomain(h sqlcgen.Household) domain.Household {
	d := domain.Household{
		ID:           h.ID,
		Name:         h.Name,
		BaseCurrency: h.BaseCurrency,
		FxSource:     domain.RateSource(h.FxSource),
		Kind:         domain.HouseholdKind(h.Kind),
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

// errOneSharedPerUser is what the user sees when the database refuses a
// second shared space (the service usually catches it first, with names).
var errOneSharedPerUser = domain.NewConflictError("Ya sos parte de otro hogar. Para unirte a este, primero salí del otro.")

// spaceRuleError maps the space triggers of migration 000032 to domain
// errors, or returns nil when err is something else.
func spaceRuleError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return nil
	}
	switch pgErr.ConstraintName {
	case "household_members_one_shared_per_user":
		return errOneSharedPerUser
	case "household_members_personal_single_member":
		return domain.NewValidationError("household", "Mi plata es solo tuya: no se puede compartir")
	}
	return nil
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
