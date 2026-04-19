package balances

import (
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

// Mount: /balances requiere auth + X-Household-ID (middleware de households).
// Doble endpoint:
//   - GET /balances    → matriz neta del hogar
//   - GET /balances/me → vista del caller (owe / owed-to-me / net)
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)

		r.Route("/balances", func(r chi.Router) {
			r.Get("/", h.HouseholdNet)
			r.Get("/me", h.MyView)
		})
	})
}

// ===================== DTOs =====================

type balanceRowDTO struct {
	From   string  `json:"from"`
	To     string  `json:"to"`
	Amount float64 `json:"amount"`
}

type householdNetDTO struct {
	HouseholdID string          `json:"householdId"`
	Balances    []balanceRowDTO `json:"balances"`
}

type myViewDTO struct {
	UserID   string          `json:"userId"`
	Owe      []balanceRowDTO `json:"owe"`
	OwedToMe []balanceRowDTO `json:"owedToMe"`
	Net      float64         `json:"net"`
}

// ===================== handlers =====================

func (h *Handler) HouseholdNet(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	rows, err := h.svc.HouseholdNet(r.Context(), householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := householdNetDTO{
		HouseholdID: householdID.String(),
		Balances:    make([]balanceRowDTO, len(rows)),
	}
	for i, b := range rows {
		out.Balances[i] = toRowDTO(b)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) MyView(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	v, err := h.svc.MyView(r.Context(), householdID, userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := myViewDTO{
		UserID:   v.UserID.String(),
		Owe:      mapRows(v.Owe),
		OwedToMe: mapRows(v.OwedToMe),
		Net:      v.Net,
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

func toRowDTO(b domain.BalanceRow) balanceRowDTO {
	return balanceRowDTO{From: b.From.String(), To: b.To.String(), Amount: b.Amount}
}

func mapRows(rows []domain.BalanceRow) []balanceRowDTO {
	out := make([]balanceRowDTO, len(rows))
	for i, b := range rows {
		out[i] = toRowDTO(b)
	}
	return out
}
