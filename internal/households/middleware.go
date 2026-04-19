package households

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// Header que usamos para seleccionar el hogar activo en cada request.
// Alternativas consideradas:
//   - path param /households/{id}/expenses → verbose, duplicaría rutas.
//   - query param ?household=... → funciona pero se mezcla con filtros.
// Header es limpio y standard para "contexto de tenant".
const HouseholdHeader = "X-Household-ID"

// ctxKey privado para no colisionar con otras keys.
type ctxKey int

const (
	ctxKeyHouseholdID ctxKey = iota
)

// ContextWithHouseholdID devuelve un ctx derivado con el household activo.
func ContextWithHouseholdID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKeyHouseholdID, id)
}

// HouseholdIDFrom extrae el household del ctx. El bool es false si el
// middleware no se aplicó (bug de wiring → 500).
func HouseholdIDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKeyHouseholdID).(uuid.UUID)
	return id, ok
}

// Middleware valida que el user (ya autenticado por auth.RequireAuth)
// sea miembro del household indicado en X-Household-ID. Si sí, inyecta
// el ID en el context. Se usa en endpoints "dentro del hogar" como
// /expenses, /members, etc.
//
// No se aplica a las rutas /households y /households/{id} porque esas
// ya validan membresía por otro lado (el list muestra solo los míos,
// el detalle lo valida el handler con el userID del ctx).
type Middleware struct {
	repo   *Repository
	logger *slog.Logger
}

func NewMiddleware(repo *Repository, logger *slog.Logger) *Middleware {
	return &Middleware{repo: repo, logger: logger}
}

func (m *Middleware) RequireHouseholdMember(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.Header.Get(HouseholdHeader)
		if raw == "" {
			httpx.WriteError(w, r, m.logger, domain.NewValidationError(HouseholdHeader, "header requerido"))
			return
		}
		householdID, err := uuid.Parse(raw)
		if err != nil {
			httpx.WriteError(w, r, m.logger, domain.NewValidationError(HouseholdHeader, "no es un UUID válido"))
			return
		}

		userID, ok := auth.UserIDFrom(r.Context())
		if !ok {
			// Este middleware se usa SIEMPRE después de RequireAuth.
			// Si no hay userID es un bug de wiring.
			m.logger.ErrorContext(r.Context(), "household middleware sin userID en ctx")
			httpx.WriteError(w, r, m.logger, domain.ErrUnauthorized)
			return
		}

		isMember, err := m.repo.IsMember(r.Context(), householdID, userID)
		if err != nil {
			httpx.WriteError(w, r, m.logger, err)
			return
		}
		if !isMember {
			// 404 en vez de 403 a propósito: no filtra existencia de hogares
			// ajenos ni la membresía de otros users.
			httpx.WriteError(w, r, m.logger, domain.ErrNotFound)
			return
		}

		ctx := ContextWithHouseholdID(r.Context(), householdID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
