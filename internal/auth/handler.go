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
	svc        *Service
	resetSvc   *PasswordResetService
	accountSvc *AccountService
	verifySvc  *EmailVerificationService
	logger   *slog.Logger
	mw       *Middleware
	// secureCookies controla flags de la cookie. En prod: true (Secure + Host prefix).
	secureCookies bool
	// rateLimit es opcional: si está seteado, se aplica a /auth/login y
	// /auth/forgot-password. Nil en tests o si main no lo cablea.
	rateLimit RateLimiter
}

func NewHandler(svc *Service, mw *Middleware, logger *slog.Logger, secureCookies bool) *Handler {
	return &Handler{svc: svc, mw: mw, logger: logger, secureCookies: secureCookies}
}

// SetPasswordResetService cablea post-construcción para mantener el
// constructor simple y opcional (tests pueden no necesitarlo).
func (h *Handler) SetPasswordResetService(svc *PasswordResetService) {
	h.resetSvc = svc
}

// SetAccountService cablea el service encargado de DELETE /me (soft delete).
func (h *Handler) SetAccountService(svc *AccountService) {
	h.accountSvc = svc
}

// SetEmailVerificationService cablea el service de verificación de email.
func (h *Handler) SetEmailVerificationService(svc *EmailVerificationService) {
	h.verifySvc = svc
}

// SetRateLimiter cablea el middleware de rate limit post-construcción.
// RateLimiter es una interface pequeña definida en ratelimit.go.
func (h *Handler) SetRateLimiter(rl RateLimiter) {
	h.rateLimit = rl
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
		// Registro: rate-limit moderado para frenar creación masiva.
		r.Group(func(r chi.Router) {
			if h.rateLimit != nil {
				r.Use(h.rateLimit.Register())
			}
			r.Post("/register", h.Register)
		})

		// Login: el endpoint más sensible a brute-force.
		r.Group(func(r chi.Router) {
			if h.rateLimit != nil {
				r.Use(h.rateLimit.Login())
			}
			r.Post("/login", h.Login)
		})

		// Refresh: baja superficie pero aplicamos límite amplio.
		r.Group(func(r chi.Router) {
			if h.rateLimit != nil {
				r.Use(h.rateLimit.Refresh())
			}
			r.Post("/refresh", h.Refresh)
		})

		r.Post("/logout", h.Logout)

		// Forgot + reset: rate-limit agresivo para evitar spam de correos
		// y probing de tokens.
		r.Group(func(r chi.Router) {
			if h.rateLimit != nil {
				r.Use(h.rateLimit.ForgotPassword())
			}
			r.Post("/forgot-password", h.ForgotPassword)
		})
		r.Group(func(r chi.Router) {
			if h.rateLimit != nil {
				r.Use(h.rateLimit.ResetPassword())
			}
			r.Post("/reset-password", h.ResetPassword)
		})

		// Email verification: verify-email es público (confirma token desde
		// link en mail). resend-verification-email requiere auth (solo el
		// propio user puede pedir reenvío).
		r.Group(func(r chi.Router) {
			if h.rateLimit != nil {
				r.Use(h.rateLimit.VerifyEmail())
			}
			r.Post("/verify-email", h.VerifyEmail)
		})
		r.Group(func(r chi.Router) {
			r.Use(h.mw.RequireAuth)
			if h.rateLimit != nil {
				r.Use(h.rateLimit.ResendVerification())
			}
			r.Post("/resend-verification-email", h.ResendVerificationEmail)
		})

		// Change password: autenticado. Limitamos por IP para frenar scripts.
		r.Group(func(r chi.Router) {
			r.Use(h.mw.RequireAuth)
			if h.rateLimit != nil {
				r.Use(h.rateLimit.ChangePassword())
			}
			r.Post("/change-password", h.ChangePassword)
		})
	})

	r.Group(func(r chi.Router) {
		r.Use(h.mw.RequireAuth)
		r.Get("/me", h.Me)
		r.Patch("/me", h.UpdateMe)
		r.Delete("/me", h.DeleteMe)
	})
}

// ===================== Register =====================

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	FirstName   string `json:"firstName"`
	LastName    string `json:"lastName"`
	InviteToken string `json:"inviteToken,omitempty"`
}

type authResponse struct {
	User            userDTO   `json:"user"`
	AccessToken     string    `json:"accessToken"`
	AccessExpiresAt time.Time `json:"accessExpiresAt"`
}

type userDTO struct {
	ID              string     `json:"id"`
	Email           string     `json:"email"`
	FirstName       string     `json:"firstName"`
	LastName        string     `json:"lastName"`
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt,omitempty"`
	// IsSuperadmin: el front lo usa para mostrar la sección /admin de
	// households borrados. Se devuelve en login/register/me/refresh.
	IsSuperadmin bool `json:"isSuperadmin"`
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	result, err := h.svc.Register(r.Context(), req.Email, req.Password, req.FirstName, req.LastName, req.InviteToken)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	h.setRefreshCookie(w, result.Tokens.RefreshToken, result.Tokens.RefreshExpiresAt)
	httpx.WriteJSON(w, http.StatusCreated, authResponse{
		User:            toUserDTO(result.User),
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
		User:            toUserDTO(result.User),
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
	httpx.WriteJSON(w, http.StatusOK, toUserDTO(user))
}

// updateMeRequest: todos opcionales. Los que se omitan quedan igual.
// Usamos punteros para distinguir "no vino" de "vino vacío".
type updateMeRequest struct {
	FirstName *string `json:"firstName,omitempty"`
	LastName  *string `json:"lastName,omitempty"`
	Email     *string `json:"email,omitempty"`
}

// UpdateMe edita el perfil del user logueado. Cambio de email NO dispara
// verification todavía (Fase 3) pero sí se valida uniqueness → 409.
func (h *Handler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	var req updateMeRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	user, err := h.svc.UpdateMe(r.Context(), userID, UpdateMeInput{
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Email:     req.Email,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toUserDTO(user))
}

// DeleteMe borra (soft) la cuenta del user autenticado. Ver
// accountService.SoftDelete para la lógica de cascada e invariantes.
func (h *Handler) DeleteMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	if h.accountSvc == nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("server", "delete account no configurado"))
		return
	}
	if err := h.accountSvc.SoftDelete(r.Context(), userID); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	// Cerramos la sesión del cliente limpiando la cookie de refresh.
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// ===================== Email verification =====================

type verifyEmailRequest struct {
	Token string `json:"token"`
}

// VerifyEmail confirma el token recibido por mail. Público — el user puede
// no estar logueado aún (o estarlo con access token).
func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	if h.verifySvc == nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("server", "email verification no configurado"))
		return
	}
	var req verifyEmailRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.verifySvc.Confirm(r.Context(), req.Token); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResendVerificationEmail: autenticado. Reemite un token si el user todavía
// no verificó su email. 409 si ya está verificado.
func (h *Handler) ResendVerificationEmail(w http.ResponseWriter, r *http.Request) {
	if h.verifySvc == nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("server", "email verification no configurado"))
		return
	}
	userID, ok := UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	if err := h.verifySvc.Resend(r.Context(), userID); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		HttpOnly: true,            // inaccesible desde JS → mitiga XSS
		Secure:   h.secureCookies, // solo HTTPS en prod
		// Lax permite que la cookie viaje en navegaciones top-level
		// (ej. links de verificación de email) y en XHR same-site
		// (front y API comparten eTLD+1 lemydev.com). Strict rompe
		// esos flujos sin ganancia real de seguridad para este caso.
		SameSite: http.SameSiteLaxMode,
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
		SameSite: http.SameSiteLaxMode,
	})
}

func toUserDTO(u domain.User) userDTO {
	return userDTO{
		ID:              u.ID.String(),
		Email:           u.Email,
		FirstName:       u.FirstName,
		LastName:        u.LastName,
		EmailVerifiedAt: u.EmailVerifiedAt,
		IsSuperadmin:    u.IsSuperadmin,
	}
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
