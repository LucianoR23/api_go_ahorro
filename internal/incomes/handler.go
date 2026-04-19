package incomes

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

// Mount: /incomes/*, /recurring-incomes/*, /totals/income (todos bajo auth+hhMW).
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)

		r.Route("/incomes", func(r chi.Router) {
			r.Get("/", h.List)
			r.Post("/", h.Create)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.Get)
				r.Patch("/", h.Update)
				r.Delete("/", h.Delete)
			})
		})

		r.Route("/recurring-incomes", func(r chi.Router) {
			r.Get("/", h.ListRecurring)
			r.Post("/", h.CreateRecurring)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.GetRecurring)
				r.Patch("/", h.UpdateRecurring)
				r.Delete("/", h.DeleteRecurring)
				r.Patch("/active", h.SetRecurringActive)
			})
		})

		r.Get("/totals/income", h.TotalsIncome)
	})
}

// ===================== DTOs =====================

type createIncomeReq struct {
	ReceivedBy      *uuid.UUID `json:"receivedBy,omitempty"` // opcional: default = userID del token
	PaymentMethodID *uuid.UUID `json:"paymentMethodId,omitempty"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	Source          string     `json:"source"`
	Description     string     `json:"description"`
	ReceivedAt      string     `json:"receivedAt"` // YYYY-MM-DD
}

type updateIncomeReq struct {
	Source      string `json:"source"`
	Description string `json:"description"`
	ReceivedAt  string `json:"receivedAt"`
}

type incomeDTO struct {
	ID              string     `json:"id"`
	HouseholdID     string     `json:"householdId"`
	ReceivedBy      string     `json:"receivedBy"`
	PaymentMethodID *string    `json:"paymentMethodId,omitempty"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	AmountBase      float64    `json:"amountBase"`
	BaseCurrency    string     `json:"baseCurrency"`
	RateUsed        *float64   `json:"rateUsed,omitempty"`
	RateAt          *time.Time `json:"rateAt,omitempty"`
	Source          string     `json:"source"`
	Description     string     `json:"description"`
	ReceivedAt      string     `json:"receivedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type listIncomesResponseDTO struct {
	Items      []incomeDTO `json:"items"`
	TotalCount int64       `json:"totalCount"`
	Limit      int32       `json:"limit"`
	Offset     int32       `json:"offset"`
}

type createRecurringReq struct {
	ReceivedBy      *uuid.UUID `json:"receivedBy,omitempty"`
	PaymentMethodID *uuid.UUID `json:"paymentMethodId,omitempty"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	Description     string     `json:"description"`
	Source          string     `json:"source"`
	Frequency       string     `json:"frequency"`
	DayOfMonth      *int       `json:"dayOfMonth,omitempty"`
	DayOfWeek       *int       `json:"dayOfWeek,omitempty"`
	MonthOfYear     *int       `json:"monthOfYear,omitempty"`
	StartsAt        string     `json:"startsAt"`
	EndsAt          *string    `json:"endsAt,omitempty"`
}

type updateRecurringReq struct {
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	Description     string     `json:"description"`
	Source          string     `json:"source"`
	Frequency       string     `json:"frequency"`
	DayOfMonth      *int       `json:"dayOfMonth,omitempty"`
	DayOfWeek       *int       `json:"dayOfWeek,omitempty"`
	MonthOfYear     *int       `json:"monthOfYear,omitempty"`
	EndsAt          *string    `json:"endsAt,omitempty"`
	PaymentMethodID *uuid.UUID `json:"paymentMethodId,omitempty"`
}

type setActiveReq struct {
	IsActive bool `json:"isActive"`
}

type recurringDTO struct {
	ID              string     `json:"id"`
	HouseholdID     string     `json:"householdId"`
	ReceivedBy      string     `json:"receivedBy"`
	PaymentMethodID *string    `json:"paymentMethodId,omitempty"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	Description     string     `json:"description"`
	Source          string     `json:"source"`
	Frequency       string     `json:"frequency"`
	DayOfMonth      *int       `json:"dayOfMonth,omitempty"`
	DayOfWeek       *int       `json:"dayOfWeek,omitempty"`
	MonthOfYear     *int       `json:"monthOfYear,omitempty"`
	IsActive        bool       `json:"isActive"`
	StartsAt        string     `json:"startsAt"`
	EndsAt          *string    `json:"endsAt,omitempty"`
	LastGenerated   *string    `json:"lastGenerated,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
}

type totalsIncomeDTO struct {
	Total        float64 `json:"total"`
	BaseCurrency string  `json:"baseCurrency"`
	From         string  `json:"from"`
	To           string  `json:"to"`
}

// ===================== incomes handlers =====================

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req createIncomeReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	receivedAt, err := time.Parse("2006-01-02", req.ReceivedAt)
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("receivedAt", "formato debe ser YYYY-MM-DD"))
		return
	}
	receivedBy := userID
	if req.ReceivedBy != nil {
		receivedBy = *req.ReceivedBy
	}
	inc, err := h.svc.Create(r.Context(), CreateInput{
		HouseholdID:     householdID,
		ReceivedBy:      receivedBy,
		PaymentMethodID: req.PaymentMethodID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Source:          req.Source,
		Description:     req.Description,
		ReceivedAt:      receivedAt,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toIncomeDTO(inc))
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
	inc, err := h.svc.Get(r.Context(), householdID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toIncomeDTO(inc))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	f, err := parseIncomeListFilter(r, householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	items, total, err := h.svc.List(r.Context(), f)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := listIncomesResponseDTO{
		Items:      make([]incomeDTO, len(items)),
		TotalCount: total,
		Limit:      f.Limit,
		Offset:     f.Offset,
	}
	for i, e := range items {
		out.Items[i] = toIncomeDTO(e)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
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
	var req updateIncomeReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	receivedAt, err := time.Parse("2006-01-02", req.ReceivedAt)
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("receivedAt", "formato debe ser YYYY-MM-DD"))
		return
	}
	inc, err := h.svc.Update(r.Context(), householdID, id, UpdateInput{
		Source:      req.Source,
		Description: req.Description,
		ReceivedAt:  receivedAt,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toIncomeDTO(inc))
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

// ===================== recurring handlers =====================

func (h *Handler) CreateRecurring(w http.ResponseWriter, r *http.Request) {
	userID, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req createRecurringReq
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
	receivedBy := userID
	if req.ReceivedBy != nil {
		receivedBy = *req.ReceivedBy
	}
	ri, err := h.svc.CreateRecurring(r.Context(), CreateRecurringInput{
		HouseholdID:     householdID,
		ReceivedBy:      receivedBy,
		PaymentMethodID: req.PaymentMethodID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Description:     req.Description,
		Source:          req.Source,
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
	httpx.WriteJSON(w, http.StatusCreated, toRecurringDTO(ri))
}

func (h *Handler) ListRecurring(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	items, err := h.svc.ListRecurring(r.Context(), householdID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]recurringDTO, len(items))
	for i, ri := range items {
		out[i] = toRecurringDTO(ri)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) GetRecurring(w http.ResponseWriter, r *http.Request) {
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
	ri, err := h.svc.GetRecurring(r.Context(), householdID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toRecurringDTO(ri))
}

func (h *Handler) UpdateRecurring(w http.ResponseWriter, r *http.Request) {
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
	var req updateRecurringReq
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
	ri, err := h.svc.UpdateRecurring(r.Context(), householdID, id, UpdateRecurringInput{
		Amount:          req.Amount,
		Currency:        req.Currency,
		Description:     req.Description,
		Source:          req.Source,
		Frequency:       req.Frequency,
		DayOfMonth:      req.DayOfMonth,
		DayOfWeek:       req.DayOfWeek,
		MonthOfYear:     req.MonthOfYear,
		EndsAt:          endsAt,
		PaymentMethodID: req.PaymentMethodID,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toRecurringDTO(ri))
}

func (h *Handler) SetRecurringActive(w http.ResponseWriter, r *http.Request) {
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
	if err := h.svc.SetRecurringActive(r.Context(), householdID, id, req.IsActive); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) DeleteRecurring(w http.ResponseWriter, r *http.Request) {
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
	if err := h.svc.DeleteRecurring(r.Context(), householdID, id); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ===================== totals =====================

// TotalsIncome: GET /totals/income?from=YYYY-MM-DD&to=YYYY-MM-DD.
// Defaults: from=primer día del mes actual, to=hoy.
func (h *Handler) TotalsIncome(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	q := r.URL.Query()
	now := time.Now().UTC()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if v := q.Get("from"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("from", "formato debe ser YYYY-MM-DD"))
			return
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse("2006-01-02", v)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("to", "formato debe ser YYYY-MM-DD"))
			return
		}
		to = t
	}
	total, baseCur, err := h.svc.TotalsInRange(r.Context(), householdID, from, to)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, totalsIncomeDTO{
		Total:        total,
		BaseCurrency: baseCur,
		From:         from.Format("2006-01-02"),
		To:           to.Format("2006-01-02"),
	})
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

func parseIncomeListFilter(r *http.Request, householdID uuid.UUID) (ListFilter, error) {
	q := r.URL.Query()
	f := ListFilter{HouseholdID: householdID}

	if v := q.Get("receivedBy"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, domain.NewValidationError("receivedBy", "no es un UUID válido")
		}
		f.ReceivedBy = &id
	}
	if v := q.Get("paymentMethodId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, domain.NewValidationError("paymentMethodId", "no es un UUID válido")
		}
		f.PaymentMethodID = &id
	}
	if v := q.Get("source"); v != "" {
		s := v
		f.Source = &s
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
	f.Limit = 50
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

// ===================== DTO mappers =====================

func toIncomeDTO(i domain.Income) incomeDTO {
	var pmID *string
	if i.PaymentMethodID != nil {
		s := i.PaymentMethodID.String()
		pmID = &s
	}
	return incomeDTO{
		ID:              i.ID.String(),
		HouseholdID:     i.HouseholdID.String(),
		ReceivedBy:      i.ReceivedBy.String(),
		PaymentMethodID: pmID,
		Amount:          i.Amount,
		Currency:        i.Currency,
		AmountBase:      i.AmountBase,
		BaseCurrency:    i.BaseCurrency,
		RateUsed:        i.RateUsed,
		RateAt:          i.RateAt,
		Source:          i.Source,
		Description:     i.Description,
		ReceivedAt:      i.ReceivedAt.Format("2006-01-02"),
		CreatedAt:       i.CreatedAt,
	}
}

func toRecurringDTO(ri domain.RecurringIncome) recurringDTO {
	var pmID *string
	if ri.PaymentMethodID != nil {
		s := ri.PaymentMethodID.String()
		pmID = &s
	}
	var endsAt, lastGen *string
	if ri.EndsAt != nil {
		s := ri.EndsAt.Format("2006-01-02")
		endsAt = &s
	}
	if ri.LastGenerated != nil {
		s := ri.LastGenerated.Format("2006-01-02")
		lastGen = &s
	}
	return recurringDTO{
		ID:              ri.ID.String(),
		HouseholdID:     ri.HouseholdID.String(),
		ReceivedBy:      ri.ReceivedBy.String(),
		PaymentMethodID: pmID,
		Amount:          ri.Amount,
		Currency:        ri.Currency,
		Description:     ri.Description,
		Source:          ri.Source,
		Frequency:       ri.Frequency,
		DayOfMonth:      ri.DayOfMonth,
		DayOfWeek:       ri.DayOfWeek,
		MonthOfYear:     ri.MonthOfYear,
		IsActive:        ri.IsActive,
		StartsAt:        ri.StartsAt.Format("2006-01-02"),
		EndsAt:          endsAt,
		LastGenerated:   lastGen,
		CreatedAt:       ri.CreatedAt,
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
