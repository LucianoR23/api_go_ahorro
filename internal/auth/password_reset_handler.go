package auth

import (
	"net/http"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// Los endpoints de reset/change-password se montan en el Handler principal
// a través de MountPasswordReset — reutiliza el Handler ya construido para
// compartir logger y middleware, pero toma el PasswordResetService por parte.

// forgotRequest: solo email. No devolvemos diferencias por email existente
// o no (anti-enumeration) — siempre 204.
type forgotRequest struct {
	Email string `json:"email"`
}

// resetRequest: token + password nueva. No pedimos email: el token ya
// identifica al user.
type resetRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"newPassword"`
}

// changeRequest: currentPassword para revalidar el hecho de ser el user
// (defensa si alguien robó el access token) + newPassword.
type changeRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// ForgotPassword inicia el flujo. Siempre 204, incluso si el email no
// existe. El service se encarga de no filtrar.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if h.resetSvc == nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("server", "password reset no configurado"))
		return
	}
	var req forgotRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.resetSvc.RequestReset(r.Context(), req.Email); err != nil {
		// Solo sale por validation (email vacío) — el resto se silencia en el svc.
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ResetPassword confirma el flujo: valida token, pisa password y limpia
// cookie de refresh (si estaba autenticado con otro session, el reset lo
// revoca del lado cliente; el refresh token viejo sigue funcionando hasta
// su expiración — aceptamos ese tradeoff a cambio de no tener tabla de
// tokens revocados).
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if h.resetSvc == nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("server", "password reset no configurado"))
		return
	}
	var req resetRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.resetSvc.ConfirmReset(r.Context(), req.Token, req.NewPassword); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	// Limpiamos cookie de refresh por las dudas: si el browser tenía sesión
	// vieja abierta, que vuelva a loguearse con la nueva clave.
	h.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// ChangePassword: user logueado. Requiere JWT válido (el middleware ya lo
// valida antes de llegar acá).
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	if h.resetSvc == nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("server", "password reset no configurado"))
		return
	}
	userID, ok := UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	var req changeRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.resetSvc.ChangePassword(r.Context(), userID, req.CurrentPassword, req.NewPassword); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
