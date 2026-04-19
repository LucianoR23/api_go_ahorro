package paymethods

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

type Handler struct {
	svc    *Service
	logger *slog.Logger
	authMW *auth.Middleware
}

func NewHandler(svc *Service, authMW *auth.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, authMW: authMW}
}

// Mount registra rutas de banks, payment_methods y credit_cards.
// Todas requieren auth (JWT → userID en ctx).
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)

		r.Route("/banks", func(r chi.Router) {
			r.Get("/", h.ListBanks)
			r.Post("/", h.CreateBank)
			r.Route("/{id}", func(r chi.Router) {
				r.Patch("/", h.UpdateBank)
				r.Post("/deactivate", h.DeactivateBank)
				r.Post("/activate", h.ActivateBank)
			})
		})

		r.Route("/payment-methods", func(r chi.Router) {
			r.Get("/", h.ListPaymentMethods)
			r.Post("/", h.CreatePaymentMethod)
			r.Route("/{id}", func(r chi.Router) {
				r.Patch("/", h.UpdatePaymentMethod)
				r.Post("/deactivate", h.DeactivatePaymentMethod)
				r.Post("/activate", h.ActivatePaymentMethod)

				r.Get("/credit-card", h.GetCreditCard)
				r.Patch("/credit-card", h.UpdateCreditCard)
			})
		})
	})
}

// ===================== DTOs =====================

type bankDTO struct {
	ID          string    `json:"id"`
	OwnerUserID string    `json:"ownerUserId"`
	Name        string    `json:"name"`
	IsActive    bool      `json:"isActive"`
	CreatedAt   time.Time `json:"createdAt"`
}

type paymentMethodDTO struct {
	ID                 string    `json:"id"`
	OwnerUserID        string    `json:"ownerUserId"`
	BankID             *string   `json:"bankId,omitempty"`
	Name               string    `json:"name"`
	Kind               string    `json:"kind"`
	AllowsInstallments bool      `json:"allowsInstallments"`
	IsActive           bool      `json:"isActive"`
	CreatedAt          time.Time `json:"createdAt"`
}

type creditCardDTO struct {
	ID                   string    `json:"id"`
	PaymentMethodID      string    `json:"paymentMethodId"`
	Alias                string    `json:"alias"`
	LastFour             *string   `json:"lastFour,omitempty"`
	DefaultClosingDay    int       `json:"defaultClosingDay"`
	DefaultDueDay        int       `json:"defaultDueDay"`
	DebitPaymentMethodID *string   `json:"debitPaymentMethodId,omitempty"`
	CreatedAt            time.Time `json:"createdAt"`
}

type paymentMethodWithCardDTO struct {
	paymentMethodDTO
	CreditCard *creditCardDTO          `json:"creditCard,omitempty"`
	Periods    []creditCardPeriodDTO   `json:"periods,omitempty"`
}

type creditCardPeriodDTO struct {
	CreditCardID string    `json:"creditCardId"`
	PeriodYM     string    `json:"periodYm"`
	ClosingDate  string    `json:"closingDate"`
	DueDate      string    `json:"dueDate"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ===================== request DTOs =====================

type createBankReq struct {
	Name string `json:"name"`
}

type updateBankReq struct {
	Name string `json:"name"`
}

type createCreditCardReq struct {
	Alias                string     `json:"alias"`
	LastFour             *string    `json:"lastFour,omitempty"`
	DefaultClosingDay    int        `json:"defaultClosingDay"`
	DefaultDueDay        int        `json:"defaultDueDay"`
	DebitPaymentMethodID *uuid.UUID `json:"debitPaymentMethodId,omitempty"`
	CurrentPeriod        *periodReq `json:"currentPeriod,omitempty"`
	NextPeriod           *periodReq `json:"nextPeriod,omitempty"`
}

// periodReq: fechas "YYYY-MM-DD" para cierre/vencimiento. El period_ym se
// deriva del closingDate en el service.
type periodReq struct {
	ClosingDate string `json:"closingDate"`
	DueDate     string `json:"dueDate"`
}

type createPaymentMethodReq struct {
	BankID             *uuid.UUID           `json:"bankId,omitempty"`
	Name               string               `json:"name"`
	Kind               string               `json:"kind"`
	AllowsInstallments *bool                `json:"allowsInstallments,omitempty"`
	CreditCard         *createCreditCardReq `json:"creditCard,omitempty"`
}

type updatePaymentMethodReq struct {
	Name               string     `json:"name"`
	BankID             *uuid.UUID `json:"bankId,omitempty"`
	AllowsInstallments *bool      `json:"allowsInstallments,omitempty"`
}

type updateCreditCardReq struct {
	Alias                string     `json:"alias"`
	LastFour             *string    `json:"lastFour,omitempty"`
	DefaultClosingDay    int        `json:"defaultClosingDay"`
	DefaultDueDay        int        `json:"defaultDueDay"`
	DebitPaymentMethodID *uuid.UUID `json:"debitPaymentMethodId,omitempty"`
}

// ===================== handlers: banks =====================

func (h *Handler) ListBanks(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	banks, err := h.svc.ListBanks(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]bankDTO, len(banks))
	for i, b := range banks {
		out[i] = toBankDTO(b)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) CreateBank(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	var req createBankReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	b, err := h.svc.CreateBank(r.Context(), userID, req.Name)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toBankDTO(b))
}

func (h *Handler) UpdateBank(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	bankID, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req updateBankReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	b, err := h.svc.UpdateBankName(r.Context(), userID, bankID, req.Name)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toBankDTO(b))
}

func (h *Handler) DeactivateBank(w http.ResponseWriter, r *http.Request) {
	h.toggleBank(w, r, false)
}

func (h *Handler) ActivateBank(w http.ResponseWriter, r *http.Request) {
	h.toggleBank(w, r, true)
}

func (h *Handler) toggleBank(w http.ResponseWriter, r *http.Request, active bool) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	bankID, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	b, err := h.svc.SetBankActive(r.Context(), userID, bankID, active)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toBankDTO(b))
}

// ===================== handlers: payment_methods =====================

func (h *Handler) ListPaymentMethods(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	list, err := h.svc.ListPaymentMethods(r.Context(), userID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]paymentMethodDTO, len(list))
	for i, p := range list {
		out[i] = toPaymentMethodDTO(p)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) CreatePaymentMethod(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	var req createPaymentMethodReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}

	in := CreatePaymentMethodInput{
		OwnerID:            userID,
		BankID:             req.BankID,
		Name:               req.Name,
		Kind:               domain.PaymentMethodKind(req.Kind),
		AllowsInstallments: req.AllowsInstallments,
	}
	if req.CreditCard != nil {
		in.CreditCard = &CreateCreditCardInput{
			Alias:                req.CreditCard.Alias,
			LastFour:             req.CreditCard.LastFour,
			DefaultClosingDay:    req.CreditCard.DefaultClosingDay,
			DefaultDueDay:        req.CreditCard.DefaultDueDay,
			DebitPaymentMethodID: req.CreditCard.DebitPaymentMethodID,
		}
		if req.CreditCard.CurrentPeriod != nil {
			p, err := parsePeriodReq("currentPeriod", *req.CreditCard.CurrentPeriod)
			if err != nil {
				httpx.WriteError(w, r, h.logger, err)
				return
			}
			in.CreditCard.CurrentPeriod = &p
		}
		if req.CreditCard.NextPeriod != nil {
			p, err := parsePeriodReq("nextPeriod", *req.CreditCard.NextPeriod)
			if err != nil {
				httpx.WriteError(w, r, h.logger, err)
				return
			}
			in.CreditCard.NextPeriod = &p
		}
	}

	out, err := h.svc.CreatePaymentMethod(r.Context(), in)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, toPaymentMethodWithCardDTO(out))
}

func (h *Handler) UpdatePaymentMethod(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req updatePaymentMethodReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	p, err := h.svc.UpdatePaymentMethod(r.Context(), userID, id, req.Name, req.BankID, req.AllowsInstallments)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toPaymentMethodDTO(p))
}

func (h *Handler) DeactivatePaymentMethod(w http.ResponseWriter, r *http.Request) {
	h.togglePaymentMethod(w, r, false)
}

func (h *Handler) ActivatePaymentMethod(w http.ResponseWriter, r *http.Request) {
	h.togglePaymentMethod(w, r, true)
}

func (h *Handler) togglePaymentMethod(w http.ResponseWriter, r *http.Request, active bool) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	id, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	p, err := h.svc.SetPaymentMethodActive(r.Context(), userID, id, active)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toPaymentMethodDTO(p))
}

// ===================== handlers: credit_cards =====================

func (h *Handler) GetCreditCard(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	pmID, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	cc, err := h.svc.GetCreditCard(r.Context(), userID, pmID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCreditCardDTO(cc))
}

func (h *Handler) UpdateCreditCard(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	pmID, err := parseUUIDParam(r, "id")
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var req updateCreditCardReq
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	cc, err := h.svc.UpdateCreditCard(r.Context(), userID, pmID, UpdateCreditCardInput{
		Alias:                req.Alias,
		LastFour:             req.LastFour,
		DefaultClosingDay:    req.DefaultClosingDay,
		DefaultDueDay:        req.DefaultDueDay,
		DebitPaymentMethodID: req.DebitPaymentMethodID,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCreditCardDTO(cc))
}

// ===================== DTO mappers =====================

func toBankDTO(b domain.Bank) bankDTO {
	return bankDTO{
		ID:          b.ID.String(),
		OwnerUserID: b.OwnerUserID.String(),
		Name:        b.Name,
		IsActive:    b.IsActive,
		CreatedAt:   b.CreatedAt,
	}
}

func toPaymentMethodDTO(p domain.PaymentMethod) paymentMethodDTO {
	var bankID *string
	if p.BankID != nil {
		s := p.BankID.String()
		bankID = &s
	}
	return paymentMethodDTO{
		ID:                 p.ID.String(),
		OwnerUserID:        p.OwnerUserID.String(),
		BankID:             bankID,
		Name:               p.Name,
		Kind:               string(p.Kind),
		AllowsInstallments: p.AllowsInstallments,
		IsActive:           p.IsActive,
		CreatedAt:          p.CreatedAt,
	}
}

func toCreditCardDTO(cc domain.CreditCard) creditCardDTO {
	var debitID *string
	if cc.DebitPaymentMethodID != nil {
		s := cc.DebitPaymentMethodID.String()
		debitID = &s
	}
	return creditCardDTO{
		ID:                   cc.ID.String(),
		PaymentMethodID:      cc.PaymentMethodID.String(),
		Alias:                cc.Alias,
		LastFour:             cc.LastFour,
		DefaultClosingDay:    cc.DefaultClosingDay,
		DefaultDueDay:        cc.DefaultDueDay,
		DebitPaymentMethodID: debitID,
		CreatedAt:            cc.CreatedAt,
	}
}

func toPaymentMethodWithCardDTO(p domain.PaymentMethodWithCard) paymentMethodWithCardDTO {
	out := paymentMethodWithCardDTO{paymentMethodDTO: toPaymentMethodDTO(p.PaymentMethod)}
	if p.CreditCard != nil {
		cc := toCreditCardDTO(*p.CreditCard)
		out.CreditCard = &cc
	}
	if len(p.Periods) > 0 {
		out.Periods = make([]creditCardPeriodDTO, len(p.Periods))
		for i, period := range p.Periods {
			out.Periods[i] = toCreditCardPeriodDTO(period)
		}
	}
	return out
}

func toCreditCardPeriodDTO(p domain.CreditCardPeriod) creditCardPeriodDTO {
	return creditCardPeriodDTO{
		CreditCardID: p.CreditCardID.String(),
		PeriodYM:     p.PeriodYM,
		ClosingDate:  p.ClosingDate.Format("2006-01-02"),
		DueDate:      p.DueDate.Format("2006-01-02"),
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
	}
}

// parsePeriodReq convierte strings "YYYY-MM-DD" al PeriodInput del service.
func parsePeriodReq(field string, req periodReq) (PeriodInput, error) {
	closing, err := time.Parse("2006-01-02", req.ClosingDate)
	if err != nil {
		return PeriodInput{}, domain.NewValidationError(field+".closingDate", "formato debe ser YYYY-MM-DD")
	}
	due, err := time.Parse("2006-01-02", req.DueDate)
	if err != nil {
		return PeriodInput{}, domain.NewValidationError(field+".dueDate", "formato debe ser YYYY-MM-DD")
	}
	return PeriodInput{ClosingDate: closing, DueDate: due}, nil
}

// ===================== helpers =====================

func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, domain.NewValidationError(name, "no es un UUID válido")
	}
	return id, nil
}

// decodeJSON: mismo patrón estricto que auth/households (DisallowUnknownFields).
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
