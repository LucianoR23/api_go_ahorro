package auth

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// refreshCookieName: nombre de la cookie que guarda el refresh token.
// Prefijo "__Host-" es un hint de navegador para que la cookie solo sea
// aceptada si: Secure, Path=/, sin Domain. Lo activamos solo en prod
// (requiere HTTPS); en dev usamos nombre normal para que funcione en http.
const (
	refreshCookieNameDev  = "ahorra_refresh"
	refreshCookieNameProd = "__Host-ahorra_refresh"
)

// Handler agrupa los endpoints de auth y sus dependencias.
type Handler struct {
	svc    *Service
	logger *slog.Logger
	mw     *Middleware
	// secureCookies controla flags de la cookie. En prod: true (Secure + Host prefix).
	secureCookies bool
}

func NewHandler(svc *Service, mw *Middleware, logger *slog.Logger, secureCookies bool) *Handler {
	return &Handler{svc: svc, mw: mw, logger: logger, secureCookies: secureCookies}
}

// Mount registra las rutas de auth en el router.
// Mantenemos el montaje dentro del handler para que el main no tenga
// que conocer los paths concretos.
//
// Agrupamos en 2 secciones:
//   /auth/*  → públicas (register, login, refresh, logout)
//   /me      → protegida con RequireAuth
func (h *Handler) Mount(r chi.Router) {
	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", h.Register)
		r.Post("/login", h.Login)
		r.Post("/refresh", h.Refresh)
		r.Post("/logout", h.Logout)
	})

	r.Group(func(r chi.Router) {
		r.Use(h.mw.RequireAuth)
		r.Get("/me", h.Me)
	})
}

// ===================== Register =====================

type registerRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

type authResponse struct {
	User            userDTO   `json:"user"`
	AccessToken     string    `json:"accessToken"`
	AccessExpiresAt time.Time `json:"accessExpiresAt"`
}

type userDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	result, err := h.svc.Register(r.Context(), req.Email, req.Password, req.Name)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	h.setRefreshCookie(w, result.Tokens.RefreshToken, result.Tokens.RefreshExpiresAt)
	httpx.WriteJSON(w, http.StatusCreated, authResponse{
		User:            toUserDTO(result.User.ID.String(), result.User.Email, result.User.Name),
		AccessToken:     result.Tokens.AccessToken,
		AccessExpiresAt: result.Tokens.AccessExpiresAt,
	})
}

// ===================== Login =====================

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	result, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	h.setRefreshCookie(w, result.Tokens.RefreshToken, result.Tokens.RefreshExpiresAt)
	httpx.WriteJSON(w, http.StatusOK, authResponse{
		User:            toUserDTO(result.User.ID.String(), result.User.Email, result.User.Name),
		AccessToken:     result.Tokens.AccessToken,
		AccessExpiresAt: result.Tokens.AccessExpiresAt,
	})
}

// ===================== Me =====================

// Me devuelve el user del access token. El middleware ya validó el JWT
// y puso el userID en el context: acá solo lo leemos y pedimos a DB.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFrom(r.Context())
	if !ok {
		// Bug de wiring: el handler se registró sin RequireAuth. Es 500
		// porque no es culpa del cliente.
		h.logger.ErrorContext(r.Context(), "handler /me sin middleware RequireAuth")
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}

	user, err := h.svc.Me(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUserDTO(user.ID.String(), user.Email, user.Name))
}

// ===================== Refresh =====================

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(h.cookieName())
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}

	pair, err := h.svc.Refresh(r.Context(), cookie.Value)
	if err != nil {
		// Rota la cookie con un valor vacío y MaxAge=-1 para que el cliente
		// se olvide del refresh inválido.
		h.clearRefreshCookie(w)
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	h.setRefreshCookie(w, pair.RefreshToken, pair.RefreshExpiresAt)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"accessToken":     pair.AccessToken,
		"accessExpiresAt": pair.AccessExpiresAt,
	})
}

// ===================== Logout =====================

// Logout simplemente borra la cookie de refresh. No hay tabla de tokens
// revocados: el access expira en 15min y el refresh queda inaccesible
// para el cliente. Si se filtró antes del logout, espera su expiración.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// ===================== helpers =====================

func (h *Handler) cookieName() string {
	if h.secureCookies {
		return refreshCookieNameProd
	}
	return refreshCookieNameDev
}

func (h *Handler) setRefreshCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName(),
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,               // inaccesible desde JS → mitiga XSS
		Secure:   h.secureCookies,    // solo HTTPS en prod
		SameSite: http.SameSiteStrictMode,
	})
}

func (h *Handler) clearRefreshCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.secureCookies,
		SameSite: http.SameSiteStrictMode,
	})
}

func toUserDTO(id, email, name string) userDTO {
	return userDTO{ID: id, Email: email, Name: name}
}

// decodeJSON: decoder estricto que rechaza campos desconocidos. Así un
// payload con typo ("passwrod" en vez de "password") falla temprano en
// lugar de hashear un string vacío.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return wrapJSONError(err)
	}
	return nil
}

// wrapJSONError convierte errores de json.Decoder en ValidationError
// para que el handler de errores les dé 422 en vez de 500.
func wrapJSONError(err error) error {
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return domain.NewValidationError("body", "JSON inválido")
	}
	var unmarshal *json.UnmarshalTypeError
	if errors.As(err, &unmarshal) {
		return domain.NewValidationError(unmarshal.Field, "tipo incorrecto")
	}
	return domain.NewValidationError("body", err.Error())
}
