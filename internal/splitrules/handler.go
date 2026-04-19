package splitrules

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/households"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

type Handler struct {
	svc    *Service
	logger *slog.Logger
	authMW *auth.Middleware
	hhMW   *households.Middleware
}

func NewHandler(svc *Service, authMW *auth.Middleware, hhMW *households.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, authMW: authMW, hhMW: hhMW}
}

// Mount: rutas bajo /split (con X-Household-ID vía middleware).
//   - GET  /split        → lista pesos del hogar (cualquier miembro)
//   - PATCH /split       → actualiza batch de pesos (solo owner)
//
// El service valida ownership en Update (requireOwner). Lista es libre
// para cualquier miembro para que el frontend muestre el panel.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)

		r.Route("/split", func(r chi.Router) {
			r.Get("/", h.List)
			r.Patch("/", h.Update)
		})
	})
}

// ===================== DTOs =====================

type ruleDTO struct {
	UserID string  `json:"userId"`
	Weight float64 `json:"weight"`
}

type listResponseDTO struct {
	HouseholdID string    `json:"householdId"`
	Rules       []ruleDTO `json:"rules"`
}

type updateItemReq struct {
	UserID uuid.UUID `json:"userId"`
	Weight float64   `json:"weight"`
}

type updateReq struct {
	Items []updateItemReq `json:"items"`
}

// ===================== handlers =====================

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	rules, err := h.svc.List(r.Context(), householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := listResponseDTO{
		HouseholdID: householdID.String(),
		Rules:       make([]ruleDTO, len(rules)),
	}
	for i, r := range rules {
		out.Rules[i] = toDTO(r)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req updateReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	items := make([]UpdateInput, len(req.Items))
	for i, it := range req.Items {
		items[i] = UpdateInput{UserID: it.UserID, Weight: it.Weight}
	}
	updated, err := h.svc.Update(r.Context(), userID, householdID, items)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := listResponseDTO{
		HouseholdID: householdID.String(),
		Rules:       make([]ruleDTO, len(updated)),
	}
	for i, r := range updated {
		out.Rules[i] = toDTO(r)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ===================== helpers =====================

func ctxUserAndHousehold(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		return uuid.Nil, uuid.Nil, domain.ErrUnauthorized
	}
	householdID, ok := households.HouseholdIDFrom(r.Context())
	if !ok {
		return uuid.Nil, uuid.Nil, errors.New("middleware faltante")
	}
	return userID, householdID, nil
}

func toDTO(r domain.SplitRule) ruleDTO {
	return ruleDTO{UserID: r.UserID.String(), Weight: r.Weight}
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
