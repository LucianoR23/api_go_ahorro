package expenses

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

// Mount registra /expenses/* bajo auth + household member.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)

		r.Route("/expenses", func(r chi.Router) {
			r.Get("/", h.List)
			r.Post("/", h.Create)
			r.Route("/{id}", func(r chi.Router) {
				r.Get("/", h.Get)
				r.Patch("/", h.Update)
				r.Delete("/", h.Delete)
				r.Patch("/installments/{n}", h.UpdateInstallment)
			})
		})
	})
}

// ===================== request DTOs =====================

type createReq struct {
	CategoryID      *uuid.UUID          `json:"categoryId,omitempty"`
	PaymentMethodID uuid.UUID           `json:"paymentMethodId"`
	Amount          float64             `json:"amount"`
	Currency        string              `json:"currency"`
	Description     string              `json:"description"`
	SpentAt         string              `json:"spentAt"` // YYYY-MM-DD
	Installments    int                 `json:"installments"`
	IsShared        bool                `json:"isShared"`
	SharesOverride  []shareOverrideReq  `json:"sharesOverride,omitempty"`
}

type shareOverrideReq struct {
	UserID uuid.UUID `json:"userId"`
	Amount float64   `json:"amount"`
}

type updateReq struct {
	Description string     `json:"description"`
	SpentAt     string     `json:"spentAt"`
	CategoryID  *uuid.UUID `json:"categoryId,omitempty"`
}

// ===================== response DTOs =====================

type expenseDTO struct {
	ID              string     `json:"id"`
	HouseholdID     string     `json:"householdId"`
	CreatedBy       string     `json:"createdBy"`
	CategoryID      *string    `json:"categoryId,omitempty"`
	PaymentMethodID string     `json:"paymentMethodId"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	AmountBase      float64    `json:"amountBase"`
	BaseCurrency    string     `json:"baseCurrency"`
	RateUsed        *float64   `json:"rateUsed,omitempty"`
	RateAt          *time.Time `json:"rateAt,omitempty"`
	Description     string     `json:"description"`
	SpentAt         string     `json:"spentAt"`
	Installments    int        `json:"installments"`
	IsShared        bool       `json:"isShared"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type installmentDTO struct {
	ID                    string     `json:"id"`
	ExpenseID             string     `json:"expenseId"`
	InstallmentNumber     int        `json:"installmentNumber"`
	InstallmentAmount     float64    `json:"installmentAmount"`
	InstallmentAmountBase float64    `json:"installmentAmountBase"`
	BillingDate           string     `json:"billingDate"`
	DueDate               *string    `json:"dueDate,omitempty"`
	IsPaid                bool       `json:"isPaid"`
	PaidAt                *time.Time `json:"paidAt,omitempty"`
	Shares                []shareDTO `json:"shares,omitempty"`
}

type shareDTO struct {
	UserID         string  `json:"userId"`
	AmountBaseOwed float64 `json:"amountBaseOwed"`
}

type expenseDetailDTO struct {
	Expense      expenseDTO       `json:"expense"`
	Installments []installmentDTO `json:"installments"`
}

type listResponseDTO struct {
	Items      []expenseDTO `json:"items"`
	TotalCount int64        `json:"totalCount"`
	Limit      int32        `json:"limit"`
	Offset     int32        `json:"offset"`
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
	spentAt, err := time.Parse("2006-01-02", req.SpentAt)
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("spentAt", "formato debe ser YYYY-MM-DD"))
		return
	}

	var overrides []ShareOverride
	if len(req.SharesOverride) > 0 {
		overrides = make([]ShareOverride, len(req.SharesOverride))
		for i, s := range req.SharesOverride {
			overrides[i] = ShareOverride{UserID: s.UserID, Amount: s.Amount}
		}
	}

	detail, err := h.svc.Create(r.Context(), CreateInput{
		HouseholdID:     householdID,
		CreatedBy:       userID,
		CategoryID:      req.CategoryID,
		PaymentMethodID: req.PaymentMethodID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Description:     req.Description,
		SpentAt:         spentAt,
		Installments:    req.Installments,
		IsShared:        req.IsShared,
		SharesOverride:  overrides,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toDetailDTO(detail))
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
	detail, err := h.svc.Get(r.Context(), householdID, id)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDetailDTO(detail))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	f, err := parseListFilter(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	items, total, err := h.svc.List(r.Context(), householdID, f)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := listResponseDTO{
		Items:      make([]expenseDTO, len(items)),
		TotalCount: total,
		Limit:      f.Limit,
		Offset:     f.Offset,
	}
	for i, e := range items {
		out.Items[i] = toExpenseDTO(e)
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
	var req updateReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	spentAt, err := time.Parse("2006-01-02", req.SpentAt)
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("spentAt", "formato debe ser YYYY-MM-DD"))
		return
	}
	e, err := h.svc.Update(r.Context(), householdID, id, UpdateInput{
		Description: req.Description,
		SpentAt:     spentAt,
		CategoryID:  req.CategoryID,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toExpenseDTO(e))
}

// updateInstallmentReq: todos los campos opcionales. dueDate es json.RawMessage
// para distinguir 3 casos:
//   - ausente           → no tocar
//   - "dueDate": null   → setear a NULL (ClearDueDate=true)
//   - "dueDate": "YYYY-MM-DD" → setear ese valor
type updateInstallmentReq struct {
	BillingDate *string         `json:"billingDate,omitempty"`
	DueDate     json.RawMessage `json:"dueDate,omitempty"`
	IsPaid      *bool           `json:"isPaid,omitempty"`
}

func (h *Handler) UpdateInstallment(w http.ResponseWriter, r *http.Request) {
	_, householdID, err := h.ctxUserAndHousehold(r)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	expenseID, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	nStr := chi.URLParam(r, "n")
	n, err := strconv.Atoi(nStr)
	if err != nil || n < 1 {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("n", "debe ser entero >= 1"))
		return
	}

	var req updateInstallmentReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	in := UpdateInstallmentInput{IsPaid: req.IsPaid}
	if req.BillingDate != nil {
		t, err := time.Parse("2006-01-02", *req.BillingDate)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("billingDate", "formato debe ser YYYY-MM-DD"))
			return
		}
		in.BillingDate = &t
	}
	if len(req.DueDate) > 0 {
		// RawMessage vacío = campo ausente. Si llegó, puede ser "null" o "\"...\"".
		if string(req.DueDate) == "null" {
			in.ClearDueDate = true
		} else {
			var s string
			if err := json.Unmarshal(req.DueDate, &s); err != nil {
				httpx.WriteError(w, r, h.logger, domain.NewValidationError("dueDate", "debe ser string o null"))
				return
			}
			t, err := time.Parse("2006-01-02", s)
			if err != nil {
				httpx.WriteError(w, r, h.logger, domain.NewValidationError("dueDate", "formato debe ser YYYY-MM-DD"))
				return
			}
			in.DueDate = &t
		}
	}

	inst, err := h.svc.UpdateInstallment(r.Context(), householdID, expenseID, n, in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	// Devolvemos el installment sin shares (no cambian en este endpoint).
	httpx.WriteJSON(w, http.StatusOK, toInstallmentDTO(domain.InstallmentWithShares{Installment: inst}))
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

// parseListFilter lee ?categoryId=...&paymentMethodId=...&from=YYYY-MM-DD&to=...&limit=&offset=
func parseListFilter(r *http.Request) (ListFilter, error) {
	q := r.URL.Query()
	var f ListFilter

	if v := q.Get("categoryId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, domain.NewValidationError("categoryId", "no es un UUID válido")
		}
		f.CategoryID = &id
	}
	if v := q.Get("paymentMethodId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return f, domain.NewValidationError("paymentMethodId", "no es un UUID válido")
		}
		f.PaymentMethodID = &id
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

func toExpenseDTO(e domain.Expense) expenseDTO {
	var catID *string
	if e.CategoryID != nil {
		s := e.CategoryID.String()
		catID = &s
	}
	return expenseDTO{
		ID:              e.ID.String(),
		HouseholdID:     e.HouseholdID.String(),
		CreatedBy:       e.CreatedBy.String(),
		CategoryID:      catID,
		PaymentMethodID: e.PaymentMethodID.String(),
		Amount:          e.Amount,
		Currency:        e.Currency,
		AmountBase:      e.AmountBase,
		BaseCurrency:    e.BaseCurrency,
		RateUsed:        e.RateUsed,
		RateAt:          e.RateAt,
		Description:     e.Description,
		SpentAt:         e.SpentAt.Format("2006-01-02"),
		Installments:    e.Installments,
		IsShared:        e.IsShared,
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

func toInstallmentDTO(iw domain.InstallmentWithShares) installmentDTO {
	i := iw.Installment
	var due *string
	if i.DueDate != nil {
		s := i.DueDate.Format("2006-01-02")
		due = &s
	}
	out := installmentDTO{
		ID:                    i.ID.String(),
		ExpenseID:             i.ExpenseID.String(),
		InstallmentNumber:     i.InstallmentNumber,
		InstallmentAmount:     i.InstallmentAmount,
		InstallmentAmountBase: i.InstallmentAmountBase,
		BillingDate:           i.BillingDate.Format("2006-01-02"),
		DueDate:               due,
		IsPaid:                i.IsPaid,
		PaidAt:                i.PaidAt,
	}
	if len(iw.Shares) > 0 {
		out.Shares = make([]shareDTO, len(iw.Shares))
		for k, s := range iw.Shares {
			out.Shares[k] = shareDTO{UserID: s.UserID.String(), AmountBaseOwed: s.AmountBaseOwed}
		}
	}
	return out
}

func toDetailDTO(d domain.ExpenseDetail) expenseDetailDTO {
	out := expenseDetailDTO{
		Expense:      toExpenseDTO(d.Expense),
		Installments: make([]installmentDTO, len(d.Installments)),
	}
	for i, iw := range d.Installments {
		out.Installments[i] = toInstallmentDTO(iw)
	}
	return out
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
