package simulations

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/households"
	"github.com/LucianoR23/valuo_api/internal/httpx"
	"github.com/LucianoR23/valuo_api/internal/money"
)

type Handler struct {
	svc    *Service
	authMW *auth.Middleware
	hhMW   *households.Middleware
	logger *slog.Logger
}

func NewHandler(svc *Service, authMW *auth.Middleware, hhMW *households.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, authMW: authMW, hhMW: hhMW, logger: logger}
}

// Mount: POST /simulations/purchase (auth + household member). Saves nothing.
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Use(h.hhMW.RequireHouseholdMember)
		r.Post("/simulations/purchase", h.Purchase)
	})
}

type purchaseReq struct {
	PaymentMethodID uuid.UUID  `json:"paymentMethodId"`
	CategoryID      *uuid.UUID `json:"categoryId,omitempty"`
	Amount          float64    `json:"amount"`
	Currency        string     `json:"currency"`
	Installments    int        `json:"installments"`
	SpentAt         string     `json:"spentAt,omitempty"` // YYYY-MM-DD, default today
	Months          int        `json:"months,omitempty"`  // 1 to 12, default 6
}

type installmentDTO struct {
	Number     int             `json:"number"`
	AmountBase decimal.Decimal `json:"amountBase"`
	PeriodYM   string          `json:"periodYm"`
	DueDate    *string         `json:"dueDate"`
}

type purchaseDTO struct {
	PaymentMethodID uuid.UUID        `json:"paymentMethodId"`
	IsCredit        bool             `json:"isCredit"`
	Amount          decimal.Decimal  `json:"amount"`
	Currency        string           `json:"currency"`
	AmountBase      decimal.Decimal  `json:"amountBase"`
	Installments    []installmentDTO `json:"installments"`
}

type monthDTO struct {
	PeriodYM    string          `json:"periodYm"`
	CardsBefore decimal.Decimal `json:"cardsBefore"`
	Purchase    decimal.Decimal `json:"purchase"`
	CardsAfter  decimal.Decimal `json:"cardsAfter"`
	Fixed       decimal.Decimal `json:"fixed"`
	Income      decimal.Decimal `json:"income"`
	Left        decimal.Decimal `json:"left"`
	PctOfIncome *float64        `json:"pctOfIncome"`
}

type cardStatementDTO struct {
	PeriodYM string          `json:"periodYm"`
	Before   decimal.Decimal `json:"before"`
	After    decimal.Decimal `json:"after"`
}

type goalDTO struct {
	GoalID            uuid.UUID       `json:"goalId"`
	GoalType          string          `json:"goalType"`
	Scope             string          `json:"scope"`
	CategoryID        *uuid.UUID      `json:"categoryId"`
	Target            decimal.Decimal `json:"target"`
	Current           decimal.Decimal `json:"current"`
	ThisPeriod        decimal.Decimal `json:"thisPeriod"`
	After             decimal.Decimal `json:"after"`
	PctAfter          float64         `json:"pctAfter"`
	Exceeds           bool            `json:"exceeds"`
	PerInstallment    decimal.Decimal `json:"perInstallment"`
	PctPerInstallment float64         `json:"pctPerInstallment"`
}

type resultDTO struct {
	BaseCurrency string             `json:"baseCurrency"`
	Purchase     purchaseDTO        `json:"purchase"`
	Months       []monthDTO         `json:"months"`
	Card         []cardStatementDTO `json:"card"`
	Goals        []goalDTO          `json:"goals"`
	HasIncome    bool               `json:"hasIncome"`
	TodayRate    bool               `json:"todayRate"`
}

func (h *Handler) Purchase(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	householdID, ok := households.HouseholdIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}
	var req purchaseReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	var spentAt time.Time
	if req.SpentAt != "" {
		t, err := time.Parse(time.DateOnly, req.SpentAt)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("spentAt", "formato YYYY-MM-DD"))
			return
		}
		spentAt = t
	}
	res, err := h.svc.Purchase(r.Context(), Input{
		HouseholdID: householdID, UserID: userID,
		PaymentMethodID: req.PaymentMethodID, CategoryID: req.CategoryID,
		Amount: money.FromFloat(req.Amount), Currency: req.Currency,
		Installments: req.Installments, SpentAt: spentAt, Months: req.Months,
	})
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toDTO(res))
}

func toDTO(res Result) resultDTO {
	out := resultDTO{
		BaseCurrency: res.BaseCurrency,
		Purchase: purchaseDTO{
			PaymentMethodID: res.Purchase.PaymentMethodID, IsCredit: res.Purchase.IsCredit,
			Amount: res.Purchase.Amount, Currency: res.Purchase.Currency, AmountBase: res.Purchase.AmountBase,
			Installments: make([]installmentDTO, len(res.Purchase.Installments)),
		},
		Months:    make([]monthDTO, len(res.Months)),
		Card:      make([]cardStatementDTO, len(res.Card)),
		Goals:     make([]goalDTO, len(res.Goals)),
		HasIncome: res.HasIncome,
		TodayRate: res.TodayRate,
	}
	for i, x := range res.Purchase.Installments {
		var due *string
		if x.DueDate != nil {
			d := x.DueDate.Format(time.DateOnly)
			due = &d
		}
		out.Purchase.Installments[i] = installmentDTO{Number: x.Number, AmountBase: x.AmountBase, PeriodYM: x.PeriodYM, DueDate: due}
	}
	for i, m := range res.Months {
		out.Months[i] = monthDTO(m)
	}
	for i, c := range res.Card {
		out.Card[i] = cardStatementDTO(c)
	}
	for i, g := range res.Goals {
		out.Goals[i] = goalDTO(g)
	}
	return out
}
