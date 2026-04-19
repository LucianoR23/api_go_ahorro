package categories

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
	"github.com/LucianoR23/api_go_ahorra/internal/households"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

type Handler struct {
	svc       *Service
	logger    *slog.Logger
	authMW    *auth.Middleware
	hhMW      *households.Middleware
}

func NewHandler(svc *Service, authMW *auth.Middleware, hhMW *households.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, authMW: authMW, hhMW: hhMW}
}

// Mount registra /categories/* bajo RequireAuth + RequireHouseholdMember.
// Todas las categorías son "del hogar actual" (X-Household-ID header).
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)

		r.Route("/categories", func(r chi.Router) {
			r.Get("/", h.List)
			r.Post("/", h.Create)
			r.Patch("/{id}", h.Update)
			r.Delete("/{id}", h.Delete)
		})
	})
}

// ===================== DTOs =====================

type categoryDTO struct {
	ID          string    `json:"id"`
	HouseholdID string    `json:"householdId"`
	Name        string    `json:"name"`
	Icon        string    `json:"icon"`
	Color       string    `json:"color"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type createRequest struct {
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

type updateRequest struct {
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Color string `json:"color"`
}

// ===================== handlers =====================

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	householdID, ok := households.HouseholdIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}
	list, err := h.svc.List(r.Context(), householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]categoryDTO, len(list))
	for i, c := range list {
		out[i] = toDTO(c)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	householdID, ok := households.HouseholdIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}
	var req createRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	cat, err := h.svc.Create(r.Context(), householdID, req.Name, req.Icon, req.Color)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toDTO(cat))
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	householdID, categoryID, err := h.householdAndCategory(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req updateRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	cat, err := h.svc.Update(r.Context(), householdID, categoryID, req.Name, req.Icon, req.Color)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(cat))
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	householdID, categoryID, err := h.householdAndCategory(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.svc.Delete(r.Context(), householdID, categoryID); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ===================== helpers =====================

func (h *Handler) householdAndCategory(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	householdID, ok := households.HouseholdIDFrom(r.Context())
	if !ok {
		return uuid.Nil, uuid.Nil, errors.New("middleware faltante")
	}
	categoryID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		return uuid.Nil, uuid.Nil, domain.NewValidationError("id", "no es un UUID válido")
	}
	return householdID, categoryID, nil
}

func toDTO(c domain.Category) categoryDTO {
	return categoryDTO{
		ID:          c.ID.String(),
		HouseholdID: c.HouseholdID.String(),
		Name:        c.Name,
		Icon:        c.Icon,
		Color:       c.Color,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
	}
}

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
