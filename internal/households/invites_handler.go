package households

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// InvitesHandler expone las rutas de invitaciones a hogar.
//
// Rutas:
//
//	Privadas (owner):
//	  POST   /households/{id}/invites         → crear invitación
//	  GET    /households/{id}/invites         → listar pendientes
//	  DELETE /households/invites/{inviteId}   → revocar
//
//	Privada (cualquier user autenticado):
//	  POST   /invites/accept                  → body: {token}
//
//	Pública:
//	  GET    /invites/{token}                 → preview pre-login
type InvitesHandler struct {
	svc    *InvitesService
	authMW *auth.Middleware
	logger *slog.Logger
}

func NewInvitesHandler(svc *InvitesService, authMW *auth.Middleware, logger *slog.Logger) *InvitesHandler {
	return &InvitesHandler{svc: svc, authMW: authMW, logger: logger}
}

func (h *InvitesHandler) Mount(r chi.Router) {
	// Ruta pública: inspección por token (para que el frontend muestre
	// "Te invitaron al hogar X" antes de pedir login/registro).
	r.Get("/invites/{token}", h.Inspect)

	// Accept y management requieren auth.
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)

		r.Post("/invites/accept", h.Accept)

		r.Route("/households/{id}/invites", func(r chi.Router) {
			r.Post("/", h.Create)
			r.Get("/", h.ListPending)
		})
		r.Delete("/households/invites/{inviteId}", h.Revoke)
		r.Post("/households/invites/{inviteId}/resend", h.Resend)
	})
}

// ===================== DTOs =====================

type createInviteRequest struct {
	Email string `json:"email"`
}

type inviteDTO struct {
	ID            string     `json:"id"`
	HouseholdID   string     `json:"householdId"`
	Email         string     `json:"email"`
	InvitedBy     string     `json:"invitedBy"`
	ExpiresAt     time.Time  `json:"expiresAt"`
	AcceptedAt    *time.Time `json:"acceptedAt,omitempty"`
	RevokedAt     *time.Time `json:"revokedAt,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	Status        string     `json:"status"`
}

type createInviteResponse struct {
	Invite    inviteDTO `json:"invite"`
	Token     string    `json:"token"`     // one-shot plano, fallback si falla el mail
	AcceptURL string    `json:"acceptUrl"` // link completo para copiar
	EmailSent bool      `json:"emailSent"`
}

type acceptRequest struct {
	Token string `json:"token"`
}

type previewResponse struct {
	HouseholdID   string    `json:"householdId"`
	HouseholdName string    `json:"householdName"`
	Email         string    `json:"email"`
	ExpiresAt     time.Time `json:"expiresAt"`
	Status        string    `json:"status"`
}

// ===================== handlers =====================

func (h *InvitesHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	householdID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("id", "no es un UUID válido"))
		return
	}
	var req createInviteRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	res, err := h.svc.Create(r.Context(), userID, householdID, req.Email)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, createInviteResponse{
		Invite:    toInviteDTO(res.Invite),
		Token:     res.Token,
		AcceptURL: res.AcceptURL,
		EmailSent: res.EmailSent,
	})
}

func (h *InvitesHandler) ListPending(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	householdID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("id", "no es un UUID válido"))
		return
	}
	list, err := h.svc.ListPending(r.Context(), userID, householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]inviteDTO, len(list))
	for i, inv := range list {
		out[i] = toInviteDTO(inv)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *InvitesHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	inviteID, err := uuid.Parse(chi.URLParam(r, "inviteId"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("inviteId", "no es un UUID válido"))
		return
	}
	if err := h.svc.Revoke(r.Context(), userID, inviteID); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *InvitesHandler) Resend(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	inviteID, err := uuid.Parse(chi.URLParam(r, "inviteId"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("inviteId", "no es un UUID válido"))
		return
	}
	res, err := h.svc.Resend(r.Context(), userID, inviteID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, createInviteResponse{
		Invite:    toInviteDTO(res.Invite),
		Token:     res.Token,
		AcceptURL: res.AcceptURL,
		EmailSent: res.EmailSent,
	})
}

func (h *InvitesHandler) Inspect(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	preview, err := h.svc.Inspect(r.Context(), token)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, previewResponse{
		HouseholdID:   preview.HouseholdID.String(),
		HouseholdName: preview.HouseholdName,
		Email:         preview.Email,
		ExpiresAt:     preview.ExpiresAt,
		Status:        preview.Status,
	})
}

func (h *InvitesHandler) Accept(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	var req acceptRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	m, err := h.svc.Accept(r.Context(), userID, req.Token)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"householdId": m.HouseholdID.String(),
		"userId":      m.UserID.String(),
		"role":        string(m.Role),
		"joinedAt":    m.JoinedAt,
	})
}

// ===================== mappers =====================

func toInviteDTO(inv domain.HouseholdInvite) inviteDTO {
	dto := inviteDTO{
		ID:          inv.ID.String(),
		HouseholdID: inv.HouseholdID.String(),
		Email:       inv.Email,
		InvitedBy:   inv.InvitedBy.String(),
		ExpiresAt:   inv.ExpiresAt,
		AcceptedAt:  inv.AcceptedAt,
		RevokedAt:   inv.RevokedAt,
		CreatedAt:   inv.CreatedAt,
		Status:      statusOf(inv),
	}
	return dto
}
