package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// superadminChecker es la interface mínima que RequireSuperadmin necesita
// del users.Repository. Definida acá (interface en el consumidor) para no
// acoplar el paquete auth a users.
type superadminChecker interface {
	IsSuperadmin(ctx context.Context, userID uuid.UUID) (bool, error)
}

// Middleware guarda las dependencias necesarias para validar tokens.
// Se instancia una vez en main y se reutiliza como middleware chi.
type Middleware struct {
	tokens *TokenIssuer
	logger *slog.Logger
	admins superadminChecker
	users  userLookup
}

func NewMiddleware(tokens *TokenIssuer, logger *slog.Logger) *Middleware {
	return &Middleware{tokens: tokens, logger: logger}
}

// SetSuperadminChecker cablea el lookup de is_superadmin post-construcción.
// Se llama en main después de instanciar userRepo. Si no se cablea,
// RequireSuperadmin responde 403 siempre (fail-closed).
func (m *Middleware) SetSuperadminChecker(c superadminChecker) {
	m.admins = c
}

// RequireAuth extrae el Bearer token del header Authorization, lo valida,
// y pone el userID en el context. Si falla cualquier paso, responde 401
// sin llamar al handler siguiente.
//
// Uso en chi:
//
//	r.Group(func(r chi.Router) {
//	    r.Use(mw.RequireAuth)
//	    r.Get("/me", handler.Me)
//	})
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := extractBearerToken(r)
		if err != nil {
			httpx.WriteError(w, r, m.logger, err)
			return
		}

		userID, sessionID, err := m.tokens.ParseAccessToken(token)
		if err != nil {
			// ParseAccessToken ya envuelve con ErrUnauthorized.
			// WriteError lo mapea a 401 con mensaje "autenticación requerida".
			// El frontend intercepta 401 y dispara refresh automático.
			httpx.WriteError(w, r, m.logger, err)
			return
		}

		ctx := ContextWithSessionID(ContextWithUserID(r.Context(), userID), sessionID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireSuperadmin compone con RequireAuth: primero valida el JWT, después
// chequea el flag is_superadmin en DB. Fail-closed: si el checker no está
// seteado, rechaza. Devuelve 403 (no 404) a propósito: el recurso existe
// pero el caller no puede operarlo.
//
// Uso en chi:
//
//	r.Group(func(r chi.Router) {
//	    r.Use(authMW.RequireAuth)
//	    r.Use(authMW.RequireSuperadmin)
//	    r.Route("/admin", ...)
//	})
func (m *Middleware) RequireSuperadmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := UserIDFrom(r.Context())
		if !ok {
			// Bug de wiring: RequireSuperadmin sin RequireAuth antes.
			m.logger.ErrorContext(r.Context(), "RequireSuperadmin sin userID en ctx")
			httpx.WriteError(w, r, m.logger, domain.ErrUnauthorized)
			return
		}
		if m.admins == nil {
			m.logger.ErrorContext(r.Context(), "RequireSuperadmin sin checker cableado")
			httpx.WriteError(w, r, m.logger, domain.ErrForbidden)
			return
		}
		isAdmin, err := m.admins.IsSuperadmin(r.Context(), userID)
		if err != nil {
			httpx.WriteError(w, r, m.logger, err)
			return
		}
		if !isAdmin {
			httpx.WriteError(w, r, m.logger, domain.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// extractBearerToken busca el header "Authorization: Bearer <token>".
// Es case-insensitive en "Bearer" (RFC 6750 lo exige).
func extractBearerToken(r *http.Request) (string, error) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", domain.NewAuthError("falta header Authorization")
	}
	// Split en 2 para preservar espacios en el token (base64 puede contener
	// caracteres que SplitN con espacio no afecta, pero por prolijidad).
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", domain.NewAuthError("formato de Authorization inválido")
	}
	return parts[1], nil
}

// userLookup: what RequireVerifiedEmail needs from the users repository.
type userLookup interface {
	GetByID(ctx context.Context, id uuid.UUID) (domain.User, error)
}

// SetUserLookup wires the lookup RequireVerifiedEmail uses.
func (m *Middleware) SetUserLookup(u userLookup) { m.users = u }

// RequireVerifiedEmail composes after RequireAuth and lets through only
// users with a verified email (plan 0.5: AI features, bots and paid checkout
// will sit behind it). Others get 403 with code "email_unverified", so the
// app can offer to resend the verification mail. Fail-closed if not wired.
func (m *Middleware) RequireVerifiedEmail(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, ok := UserIDFrom(r.Context())
		if !ok {
			m.logger.ErrorContext(r.Context(), "RequireVerifiedEmail sin userID en ctx")
			httpx.WriteError(w, r, m.logger, domain.ErrUnauthorized)
			return
		}
		if m.users == nil {
			m.logger.ErrorContext(r.Context(), "RequireVerifiedEmail sin lookup cableado")
			httpx.WriteError(w, r, m.logger, domain.ErrForbidden)
			return
		}
		u, err := m.users.GetByID(r.Context(), userID)
		if err != nil {
			httpx.WriteError(w, r, m.logger, err)
			return
		}
		if u.EmailVerifiedAt == nil {
			httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{
				Code:    "email_unverified",
				Message: "Verificá tu email para usar esta función.",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}
