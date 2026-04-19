package recurringexpenses

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
	svc    *Service
	logger *slog.Logger
	authMW *auth.Middleware
	hhMW   *households.Middleware
}

func NewHandler(svc *Service, authMW *auth.Middleware, hhMW *households.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, authMW: authMW, hhMW: hhMW}
}

// Mount: /recurring-expenses/* bajo auth + household member.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)

		r.Route("/recurring-expenses", func(r chi.Router) {
			r.Get("/", h.List)
			r.Post("/", h.Create)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.Get)
				r.Patch("/", h.Update)
				r.Delete("/", h.Delete)
				r.Patch("/active", h.SetActive)
			})
		})
	})
}

// ===================== DTOs =====================

type createReq struct {
	CategoryID      *uuid.UUID `json:"categoryId,omitempty"`
	PaymentMethodID uuid.UUID  `json:"paymentMethodId"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	Description     string     `json:"description"`
	Installments    int        `json:"installments"`
	IsShared        bool       `json:"isShared"`
	Frequency       string     `json:"frequency"`
	DayOfMonth      *int       `json:"dayOfMonth,omitempty"`
	DayOfWeek       *int       `json:"dayOfWeek,omitempty"`
	MonthOfYear     *int       `json:"monthOfYear,omitempty"`
	StartsAt        string     `json:"startsAt"`
	EndsAt          *string    `json:"endsAt,omitempty"`
}

type updateReq struct {
	CategoryID      *uuid.UUID `json:"categoryId,omitempty"`
	PaymentMethodID uuid.UUID  `json:"paymentMethodId"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	Description     string     `json:"description"`
	Installments    int        `json:"installments"`
	IsShared        bool       `json:"isShared"`
	Frequency       string     `json:"frequency"`
	DayOfMonth      *int       `json:"dayOfMonth,omitempty"`
	DayOfWeek       *int       `json:"dayOfWeek,omitempty"`
	MonthOfYear     *int       `json:"monthOfYear,omitempty"`
	EndsAt          *string    `json:"endsAt,omitempty"`
}

type setActiveReq struct {
	IsActive bool `json:"isActive"`
}

type recurringDTO struct {
	ID              string    `json:"id"`
	HouseholdID     string    `json:"householdId"`
	CreatedBy       string    `json:"createdBy"`
	CategoryID      *string   `json:"categoryId,omitempty"`
	PaymentMethodID string    `json:"paymentMethodId"`
	Amount          float64   `json:"amount"`
	Currency        string    `json:"currency"`
	Description     string    `json:"description"`
	Installments    int       `json:"installments"`
	IsShared        bool      `json:"isShared"`
	Frequency       string    `json:"frequency"`
	DayOfMonth      *int      `json:"dayOfMonth,omitempty"`
	DayOfWeek       *int      `json:"dayOfWeek,omitempty"`
	MonthOfYear     *int      `json:"monthOfYear,omitempty"`
	IsActive        bool      `json:"isActive"`
	StartsAt        string    `json:"startsAt"`
	EndsAt          *string   `json:"endsAt,omitempty"`
	LastGenerated   *string   `json:"lastGenerated,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

// ===================== handlers =====================

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req createReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	startsAt, err := time.Parse("2006-01-02", req.StartsAt)
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("startsAt", "formato debe ser YYYY-MM-DD"))
		return
	}
	var endsAt *time.Time
	if req.EndsAt != nil && *req.EndsAt != "" {
		t, err := time.Parse("2006-01-02", *req.EndsAt)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("endsAt", "formato debe ser YYYY-MM-DD"))
			return
		}
		endsAt = &t
	}
	installments := req.Installments
	if installments == 0 {
		installments = 1
	}
	re, err := h.svc.Create(r.Context(), CreateInput{
		HouseholdID:     householdID,
		CreatedBy:       userID,
		CategoryID:      req.CategoryID,
		PaymentMethodID: req.PaymentMethodID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Description:     req.Description,
		Installments:    installments,
		IsShared:        req.IsShared,
		Frequency:       req.Frequency,
		DayOfMonth:      req.DayOfMonth,
		DayOfWeek:       req.DayOfWeek,
		MonthOfYear:     req.MonthOfYear,
		StartsAt:        startsAt,
		EndsAt:          endsAt,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toDTO(re))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	items, err := h.svc.List(r.Context(), householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]recurringDTO, len(items))
	for i, re := range items {
		out[i] = toDTO(re)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	re, err := h.svc.Get(r.Context(), householdID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(re))
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req updateReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var endsAt *time.Time
	if req.EndsAt != nil && *req.EndsAt != "" {
		t, err := time.Parse("2006-01-02", *req.EndsAt)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("endsAt", "formato debe ser YYYY-MM-DD"))
			return
		}
		endsAt = &t
	}
	installments := req.Installments
	if installments == 0 {
		installments = 1
	}
	re, err := h.svc.Update(r.Context(), householdID, id, UpdateInput{
		Amount:          req.Amount,
		Currency:        req.Currency,
		Description:     req.Description,
		Installments:    installments,
		IsShared:        req.IsShared,
		Frequency:       req.Frequency,
		DayOfMonth:      req.DayOfMonth,
		DayOfWeek:       req.DayOfWeek,
		MonthOfYear:     req.MonthOfYear,
		EndsAt:          endsAt,
		CategoryID:      req.CategoryID,
		PaymentMethodID: req.PaymentMethodID,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(re))
}

func (h *Handler) SetActive(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req setActiveReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if err := h.svc.SetActive(r.Context(), householdID, id, req.IsActive); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
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

func (h *Handler) ctxUserAndHousehold(r *http.Request) (uuid.UUID, uuid.UUID, error) {
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

func toDTO(re domain.RecurringExpense) recurringDTO {
	var catID *string
	if re.CategoryID != nil {
		s := re.CategoryID.String()
		catID = &s
	}
	var endsAt, lastGen *string
	if re.EndsAt != nil {
		s := re.EndsAt.Format("2006-01-02")
		endsAt = &s
	}
	if re.LastGenerated != nil {
		s := re.LastGenerated.Format("2006-01-02")
		lastGen = &s
	}
	return recurringDTO{
		ID:              re.ID.String(),
		HouseholdID:     re.HouseholdID.String(),
		CreatedBy:       re.CreatedBy.String(),
		CategoryID:      catID,
		PaymentMethodID: re.PaymentMethodID.String(),
		Amount:          re.Amount,
		Currency:        re.Currency,
		Description:     re.Description,
		Installments:    re.Installments,
		IsShared:        re.IsShared,
		Frequency:       re.Frequency,
		DayOfMonth:      re.DayOfMonth,
		DayOfWeek:       re.DayOfWeek,
		MonthOfYear:     re.MonthOfYear,
		IsActive:        re.IsActive,
		StartsAt:        re.StartsAt.Format("2006-01-02"),
		EndsAt:          endsAt,
		LastGenerated:   lastGen,
		CreatedAt:       re.CreatedAt,
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
