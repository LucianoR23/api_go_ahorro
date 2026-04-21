package households

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// Handler expone las rutas del dominio households.
type Handler struct {
	svc    *Service
	logger *slog.Logger
	authMW *auth.Middleware
}

func NewHandler(svc *Service, authMW *auth.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, authMW: authMW}
}

// Mount registra todas las rutas de households bajo /households.
// Todas requieren auth (RequireAuth en el grupo padre).
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)

		r.Route("/households", func(r chi.Router) {
			r.Get("/", h.List)
			r.Post("/", h.Create)

			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.Get)
				r.Patch("/", h.Update)
				r.Delete("/", h.Delete)

				r.Get("/members", h.ListMembers)
				r.Post("/members", h.InviteMember)
				r.Delete("/members/{userId}", h.RemoveMember)
				r.Patch("/members/{userId}/role", h.UpdateMemberRole)
			})
		})
	})
}

// ===================== DTOs =====================

type householdDTO struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	BaseCurrency string    `json:"baseCurrency"`
	CreatedBy    string    `json:"createdBy"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type memberDTO struct {
	UserID    string    `json:"userId"`
	Email     string    `json:"email"`
	FirstName string    `json:"firstName"`
	LastName  string    `json:"lastName"`
	Role      string    `json:"role"`
	JoinedAt  time.Time `json:"joinedAt"`
}

type createRequest struct {
	Name         string `json:"name"`
	BaseCurrency string `json:"baseCurrency"`
}

type updateRequest struct {
	Name         string `json:"name"`
	BaseCurrency string `json:"baseCurrency"`
}

type inviteRequest struct {
	Email string `json:"email"`
}

// ===================== handlers =====================

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	list, err := h.svc.List(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]householdDTO, len(list))
	for i, hh := range list {
		out[i] = toHouseholdDTO(hh)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	var req createRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	hh, err := h.svc.Create(r.Context(), userID, req.Name, req.BaseCurrency)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toHouseholdDTO(hh))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := h.callerAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	// Chequeo de membresía explícito (este endpoint no usa el middleware
	// RequireHouseholdMember, porque se identifica por path param y no
	// por header — decisión: /households/{id} es la excepción).
	isMember, err := h.svc.repo.IsMember(r.Context(), householdID, userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if !isMember {
		httpx.WriteError(w, r, h.logger, domain.ErrNotFound)
		return
	}

	hh, err := h.svc.Get(r.Context(), householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toHouseholdDTO(hh))
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := h.callerAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req updateRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	hh, err := h.svc.Update(r.Context(), userID, householdID, req.Name, req.BaseCurrency)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toHouseholdDTO(hh))
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := h.callerAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.svc.Delete(r.Context(), userID, householdID); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := h.callerAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	isMember, err := h.svc.repo.IsMember(r.Context(), householdID, userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if !isMember {
		httpx.WriteError(w, r, h.logger, domain.ErrNotFound)
		return
	}
	members, err := h.svc.ListMembers(r.Context(), householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]memberDTO, len(members))
	for i, m := range members {
		out[i] = memberDTO{
			UserID:    m.User.ID.String(),
			Email:     m.User.Email,
			FirstName: m.User.FirstName,
			LastName:  m.User.LastName,
			Role:      string(m.Role),
			JoinedAt:  m.JoinedAt,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) InviteMember(w http.ResponseWriter, r *http.Request) {
	inviterID, householdID, err := h.callerAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req inviteRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	m, err := h.svc.InviteByEmail(r.Context(), inviterID, householdID, req.Email)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"userId":      m.UserID.String(),
		"householdId": m.HouseholdID.String(),
		"role":        string(m.Role),
		"joinedAt":    m.JoinedAt,
	})
}

func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	requesterID, householdID, err := h.callerAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("userId", "no es un UUID válido"))
		return
	}
	if err := h.svc.RemoveMember(r.Context(), requesterID, householdID, targetID); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateRoleRequest struct {
	Role string `json:"role"`
}

// UpdateMemberRole transfiere la propiedad del hogar. Acepta solo
// role="owner" — modela una transferencia completa (el owner actual queda
// como member). Cualquier otro valor responde 422.
func (h *Handler) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	callerID, householdID, err := h.callerAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	targetID, err := uuid.Parse(chi.URLParam(r, "userId"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("userId", "no es un UUID válido"))
		return
	}
	var req updateRoleRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	// Single-owner model: la única transición soportada es "owner" (transferencia).
	// Si en el futuro admitimos multi-owner, acá se agrega el "member" path.
	if req.Role != string(domain.RoleOwner) {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("role", "solo se acepta 'owner' (transferencia)"))
		return
	}
	if err := h.svc.TransferOwnership(r.Context(), callerID, householdID, targetID); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ===================== helpers =====================

// callerAndHousehold extrae el userID del ctx (seteado por RequireAuth)
// y parsea el {id} del path. Devuelve error ya apto para WriteError.
func (h *Handler) callerAndHousehold(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		return uuid.Nil, uuid.Nil, domain.ErrUnauthorized
	}
	householdID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, domain.NewValidationError("id", "no es un UUID válido")
	}
	return userID, householdID, nil
}

func toHouseholdDTO(h domain.Household) householdDTO {
	return householdDTO{
		ID:           h.ID.String(),
		Name:         h.Name,
		BaseCurrency: h.BaseCurrency,
		CreatedBy:    h.CreatedBy.String(),
		CreatedAt:    h.CreatedAt,
		UpdatedAt:    h.UpdatedAt,
	}
}

// decodeJSON: mismo patrón estricto que auth — rechaza campos desconocidos.
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
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
	return nil
}
