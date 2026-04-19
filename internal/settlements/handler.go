package settlements

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

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

func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)

		r.Route("/settlements", func(r chi.Router) {
			r.Get("/", h.List)
			r.Post("/", h.Create)
			r.Get("/{id}", h.Get)
			r.Delete("/{id}", h.Delete)
		})
	})
}

// ===================== DTOs =====================

type createReq struct {
	FromUser uuid.UUID `json:"fromUser"`
	ToUser   uuid.UUID `json:"toUser"`
	Amount   float64   `json:"amount"`
	Note     *string   `json:"note,omitempty"`
	PaidAt   *string   `json:"paidAt,omitempty"` // YYYY-MM-DD, optional (default: today)
}

type settlementDTO struct {
	ID           string    `json:"id"`
	HouseholdID  string    `json:"householdId"`
	FromUser     string    `json:"fromUser"`
	ToUser       string    `json:"toUser"`
	Amount       float64   `json:"amount"`
	BaseCurrency string    `json:"baseCurrency"`
	Note         *string   `json:"note,omitempty"`
	PaidAt       string    `json:"paidAt"`
	CreatedAt    time.Time `json:"createdAt"`
}

type listResponseDTO struct {
	Items []settlementDTO `json:"items"`
}

// ===================== handlers =====================

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req createReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	in := CreateInput{
		HouseholdID: householdID,
		FromUser:    req.FromUser,
		ToUser:      req.ToUser,
		Amount:      req.Amount,
		Note:        req.Note,
	}
	if req.PaidAt != nil {
		t, err := time.Parse("2006-01-02", *req.PaidAt)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("paidAt", "formato debe ser YYYY-MM-DD"))
			return
		}
		in.PaidAt = t
	}
	sp, err := h.svc.Create(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toDTO(sp))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	sp, err := h.svc.Get(r.Context(), householdID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(sp))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	f, err := parseListFilter(r, householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	items, err := h.svc.List(r.Context(), f)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := listResponseDTO{Items: make([]settlementDTO, len(items))}
	for i, sp := range items {
		out.Items[i] = toDTO(sp)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.svc.Delete(r.Context(), householdID, id); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, domain.NewValidationError(name, "no es un UUID válido")
	}
	return id, nil
}

func parseListFilter(r *http.Request, householdID uuid.UUID) (ListFilter, error) {
	q := r.URL.Query()
	f := ListFilter{HouseholdID: householdID, Limit: 50}

	if v := q.Get("fromUser"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, domain.NewValidationError("fromUser", "no es un UUID válido")
		}
		f.FromUser = &id
	}
	if v := q.Get("toUser"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, domain.NewValidationError("toUser", "no es un UUID válido")
		}
		f.ToUser = &id
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return f, domain.NewValidationError("from", "formato debe ser YYYY-MM-DD")
		}
		f.FromDate = &t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			return f, domain.NewValidationError("to", "formato debe ser YYYY-MM-DD")
		}
		f.ToDate = &t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			return f, domain.NewValidationError("limit", "debe ser entero entre 1 y 200")
		}
		f.Limit = int32(n)
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return f, domain.NewValidationError("offset", "debe ser entero >= 0")
		}
		f.Offset = int32(n)
	}
	return f, nil
}

func toDTO(sp domain.SettlementPayment) settlementDTO {
	return settlementDTO{
		ID:           sp.ID.String(),
		HouseholdID:  sp.HouseholdID.String(),
		FromUser:     sp.FromUser.String(),
		ToUser:       sp.ToUser.String(),
		Amount:       sp.AmountBase,
		BaseCurrency: sp.BaseCurrency,
		Note:         sp.Note,
		PaidAt:       sp.PaidAt.Format("2006-01-02"),
		CreatedAt:    sp.CreatedAt,
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
