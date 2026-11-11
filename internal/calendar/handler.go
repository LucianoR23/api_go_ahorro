package calendar

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/households"
	"github.com/LucianoR23/valuo_api/internal/httpx"
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

// Mount: GET /calendar?month=YYYY-MM, auth + household member.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)
		r.Get("/calendar", h.Month)
	})
}

type amountDTO struct {
	Currency string          `json:"currency"`
	Amount   decimal.Decimal `json:"amount"`
}

type eventDTO struct {
	Date             string           `json:"date"` // YYYY-MM-DD
	Kind             Kind             `json:"kind"`
	Title            string           `json:"title"`
	Amount           *decimal.Decimal `json:"amount,omitempty"`
	Currency         string           `json:"currency,omitempty"`
	Totals           []amountDTO      `json:"totals,omitempty"`
	Status           string           `json:"status,omitempty"`
	InstallmentCount int              `json:"installmentCount,omitempty"`
	PaymentMethodID  *uuid.UUID       `json:"paymentMethodId,omitempty"`
	PeriodYM         string           `json:"periodYm,omitempty"`
	RecurringID      *uuid.UUID       `json:"recurringId,omitempty"`
	CategoryID       *uuid.UUID       `json:"categoryId,omitempty"`
	Variable         bool             `json:"variable,omitempty"`
}

type monthDTO struct {
	Month  string     `json:"month"`
	Events []eventDTO `json:"events"`
}

func toDTO(m Month) monthDTO {
	out := monthDTO{Month: m.Month, Events: make([]eventDTO, len(m.Events))}
	for i, e := range m.Events {
		d := eventDTO{
			Date: e.Date.Format("2006-01-02"), Kind: e.Kind, Title: e.Title, Currency: e.Currency,
			Status: string(e.Status), InstallmentCount: e.InstallmentCount, PaymentMethodID: e.PaymentMethodID,
			PeriodYM: e.PeriodYM, RecurringID: e.RecurringID, CategoryID: e.CategoryID, Variable: e.Variable,
		}
		d.Amount = e.Amount
		for _, t := range e.Totals {
			d.Totals = append(d.Totals, amountDTO{Currency: t.Currency, Amount: t.Amount})
		}
		out.Events[i] = d
	}
	return out
}

func (h *Handler) Month(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	householdID, ok := households.HouseholdIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errors.New("calendar: household middleware missing"))
		return
	}
	month, err := ParseMonth(r.URL.Query().Get("month"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	res, err := h.svc.Month(r.Context(), userID, householdID, month)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(res))
}
