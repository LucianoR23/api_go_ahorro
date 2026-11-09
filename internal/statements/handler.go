package statements

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/httpx"
	"github.com/LucianoR23/valuo_api/internal/money"
)

type Handler struct {
	svc    *Service
	logger *slog.Logger
	authMW *auth.Middleware
}

func NewHandler(svc *Service, authMW *auth.Middleware, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, logger: logger, authMW: authMW}
}

// Mount registers /payment-methods/{id}/credit-card/statements/*. {id} is the
// card's payment method, like the periods endpoints. Owner only. Plus
// "Comprometido" across the user's cards and a card's installment plans (4.2).
func (h *Handler) Mount(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(h.authMW.RequireAuth)
		r.Get("/credit-cards/committed", h.Committed)
		r.Get("/payment-methods/{id}/credit-card/installment-plans", h.InstallmentPlans)
		r.Route("/payment-methods/{id}/credit-card/statements", func(r chi.Router) {
			r.Get("/", h.List)
			r.Route("/{ym}", func(r chi.Router) {
				r.Get("/", h.Detail)
				r.Patch("/", h.Patch)
				r.Post("/pay", h.Pay)
				r.Post("/unpay", h.Unpay)
				r.Post("/adjustments", h.CreateAdjustment)
				r.Patch("/adjustments/{adjustmentId}", h.UpdateAdjustment)
				r.Delete("/adjustments/{adjustmentId}", h.DeleteAdjustment)
			})
		})
	})
}

// ===================== DTOs =====================

type amountDTO struct {
	Currency string          `json:"currency"`
	Amount   decimal.Decimal `json:"amount"`
}

type statementDTO struct {
	PeriodYM         string           `json:"periodYm"`
	ClosingDate      string           `json:"closingDate"`
	DueDate          string           `json:"dueDate"`
	Status           string           `json:"status"`
	Totals           []amountDTO      `json:"totals"`
	InstallmentCount int              `json:"installmentCount"`
	MinimumPayment   *decimal.Decimal `json:"minimumPayment"`
	PaidAt           *time.Time       `json:"paidAt"`
	PaidAmount       *decimal.Decimal `json:"paidAmount"`
	PaidCurrency     *string          `json:"paidCurrency"`
}

type listDTO struct {
	OpenPeriodYM string         `json:"openPeriodYm"`
	Statements   []statementDTO `json:"statements"`
}

type installmentDTO struct {
	InstallmentID     string          `json:"installmentId"`
	ExpenseID         string          `json:"expenseId"`
	HouseholdID       string          `json:"householdId"`
	CategoryID        *string         `json:"categoryId"`
	Description       string          `json:"description"`
	SpentAt           string          `json:"spentAt"`
	InstallmentNumber int             `json:"installmentNumber"`
	TotalInstallments int             `json:"totalInstallments"`
	Amount            decimal.Decimal `json:"amount"`
	Currency          string          `json:"currency"`
	IsPaid            bool            `json:"isPaid"`
}

type adjustmentDTO struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Description string          `json:"description"`
	Amount      decimal.Decimal `json:"amount"`
	Currency    string          `json:"currency"`
	CreatedAt   time.Time       `json:"createdAt"`
}

type detailDTO struct {
	statementDTO
	Installments []installmentDTO `json:"installments"`
	Adjustments  []adjustmentDTO  `json:"adjustments"`
}

type periodTotalsDTO struct {
	PeriodYM string      `json:"periodYm"`
	Totals   []amountDTO `json:"totals"`
}

type cardCommittedDTO struct {
	PaymentMethodID string            `json:"paymentMethodId"`
	Name            string            `json:"name"`
	Alias           string            `json:"alias"`
	Network         *string           `json:"network"`
	LastFour        *string           `json:"lastFour"`
	OpenPeriodYM    string            `json:"openPeriodYm"`
	ToPay           *statementDTO     `json:"toPay"`
	Months          []periodTotalsDTO `json:"months"`
}

type committedDTO struct {
	Months []periodTotalsDTO  `json:"months"`
	Cards  []cardCommittedDTO `json:"cards"`
}

type installmentPlanDTO struct {
	ExpenseID             string          `json:"expenseId"`
	HouseholdID           string          `json:"householdId"`
	CategoryID            *string         `json:"categoryId"`
	Description           string          `json:"description"`
	Currency              string          `json:"currency"`
	TotalInstallments     int             `json:"totalInstallments"`
	NextInstallmentNumber int             `json:"nextInstallmentNumber"`
	NextInstallmentAmount decimal.Decimal `json:"nextInstallmentAmount"`
	LastPeriodYM          string          `json:"lastPeriodYm"`
}

func toAmountDTOs(in []domain.CurrencyAmount) []amountDTO {
	out := make([]amountDTO, len(in))
	for i, t := range in {
		out[i] = amountDTO{Currency: t.Currency, Amount: t.Amount}
	}
	return out
}

func toPeriodTotalsDTOs(in []domain.PeriodTotals) []periodTotalsDTO {
	out := make([]periodTotalsDTO, len(in))
	for i, p := range in {
		out[i] = periodTotalsDTO{PeriodYM: p.PeriodYM, Totals: toAmountDTOs(p.Totals)}
	}
	return out
}

func toCommittedDTO(c domain.Committed) committedDTO {
	out := committedDTO{Months: toPeriodTotalsDTOs(c.Months), Cards: make([]cardCommittedDTO, len(c.Cards))}
	for i, card := range c.Cards {
		cc := card.Card.CreditCard
		d := cardCommittedDTO{
			PaymentMethodID: card.Card.ID.String(), Name: card.Card.Name, Alias: cc.Alias,
			Network: cc.Network, LastFour: cc.LastFour, OpenPeriodYM: card.OpenPeriodYM,
			Months: toPeriodTotalsDTOs(card.Months),
		}
		if card.ToPay != nil {
			st := toStatementDTO(*card.ToPay)
			d.ToPay = &st
		}
		out.Cards[i] = d
	}
	return out
}

func toStatementDTO(s domain.CardStatement) statementDTO {
	return statementDTO{
		PeriodYM: s.PeriodYM, ClosingDate: s.ClosingDate.Format(time.DateOnly), DueDate: s.DueDate.Format(time.DateOnly),
		Status: string(s.Status), Totals: toAmountDTOs(s.Totals), InstallmentCount: s.InstallmentCount,
		MinimumPayment: s.MinimumPayment, PaidAt: s.PaidAt, PaidAmount: s.PaidAmount, PaidCurrency: s.PaidCurrency,
	}
}

func toDetailDTO(d domain.CardStatementDetail) detailDTO {
	out := detailDTO{
		statementDTO: toStatementDTO(d.CardStatement),
		Installments: make([]installmentDTO, len(d.Installments)),
		Adjustments:  make([]adjustmentDTO, len(d.Adjustments)),
	}
	for i, in := range d.Installments {
		var cat *string
		if in.CategoryID != nil {
			c := in.CategoryID.String()
			cat = &c
		}
		out.Installments[i] = installmentDTO{
			InstallmentID: in.InstallmentID.String(), ExpenseID: in.ExpenseID.String(), HouseholdID: in.HouseholdID.String(),
			CategoryID: cat, Description: in.Description, SpentAt: in.SpentAt.Format(time.DateOnly),
			InstallmentNumber: in.InstallmentNumber, TotalInstallments: in.TotalInstallments,
			Amount: in.Amount, Currency: in.Currency, IsPaid: in.IsPaid,
		}
	}
	for i, a := range d.Adjustments {
		out.Adjustments[i] = adjustmentDTO{
			ID: a.ID.String(), Kind: string(a.Kind), Description: a.Description,
			Amount: a.Amount, Currency: a.Currency, CreatedAt: a.CreatedAt,
		}
	}
	return out
}

// Amounts arrive as float64 so a wrong type still names the field (see
// money.FromFloat).
type patchReq struct {
	MinimumPayment httpx.Optional[float64] `json:"minimumPayment"`
}

type payReq struct {
	Amount   *float64 `json:"amount"`
	Currency string   `json:"currency"`
}

type adjustmentReq struct {
	Kind        string  `json:"kind"`
	Description string  `json:"description"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
}

func (a adjustmentReq) input() AdjustmentInput {
	return AdjustmentInput{Kind: domain.AdjustmentKind(a.Kind), Description: a.Description, Amount: money.FromFloat(a.Amount), Currency: a.Currency}
}

// ===================== handlers =====================

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	userID, pmID, ok := h.ids(w, r)
	if !ok {
		return
	}
	res, err := h.svc.List(r.Context(), userID, pmID, r.URL.Query().Get("from"), r.URL.Query().Get("to"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := listDTO{OpenPeriodYM: res.OpenPeriodYM, Statements: make([]statementDTO, len(res.Statements))}
	for i, s := range res.Statements {
		out.Statements[i] = toStatementDTO(s)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// Committed: GET /credit-cards/committed?months=6 (1 to 12, default 6).
func (h *Handler) Committed(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return
	}
	months := 6
	if v := r.URL.Query().Get("months"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("months", "debe ser un número"))
			return
		}
		months = n
	}
	res, err := h.svc.Committed(r.Context(), userID, months)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toCommittedDTO(res))
}

func (h *Handler) InstallmentPlans(w http.ResponseWriter, r *http.Request) {
	userID, pmID, ok := h.ids(w, r)
	if !ok {
		return
	}
	plans, err := h.svc.InstallmentPlans(r.Context(), userID, pmID)
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	out := make([]installmentPlanDTO, len(plans))
	for i, p := range plans {
		var cat *string
		if p.CategoryID != nil {
			c := p.CategoryID.String()
			cat = &c
		}
		out[i] = installmentPlanDTO{
			ExpenseID: p.ExpenseID.String(), HouseholdID: p.HouseholdID.String(), CategoryID: cat,
			Description: p.Description, Currency: p.Currency, TotalInstallments: p.TotalInstallments,
			NextInstallmentNumber: p.NextInstallmentNumber, NextInstallmentAmount: p.NextInstallmentAmount,
			LastPeriodYM: p.LastPeriodYM,
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"plans": out})
}

func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	h.respond(w, r, func(userID, pmID uuid.UUID, ym string) (domain.CardStatementDetail, error) {
		return h.svc.Detail(r.Context(), userID, pmID, ym)
	})
}

func (h *Handler) Patch(w http.ResponseWriter, r *http.Request) {
	var req patchReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	h.respond(w, r, func(userID, pmID uuid.UUID, ym string) (domain.CardStatementDetail, error) {
		if !req.MinimumPayment.Set {
			return h.svc.Detail(r.Context(), userID, pmID, ym)
		}
		var minimum *decimal.Decimal
		if !req.MinimumPayment.Null {
			m := money.FromFloat(req.MinimumPayment.Value)
			minimum = &m
		}
		return h.svc.SetMinimum(r.Context(), userID, pmID, ym, minimum)
	})
}

func (h *Handler) Pay(w http.ResponseWriter, r *http.Request) {
	var req payReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	in := PayInput{Currency: req.Currency}
	if req.Amount != nil {
		a := money.FromFloat(*req.Amount)
		in.Amount = &a
	}
	h.respond(w, r, func(userID, pmID uuid.UUID, ym string) (domain.CardStatementDetail, error) {
		return h.svc.Pay(r.Context(), userID, pmID, ym, in)
	})
}

func (h *Handler) Unpay(w http.ResponseWriter, r *http.Request) {
	h.respond(w, r, func(userID, pmID uuid.UUID, ym string) (domain.CardStatementDetail, error) {
		return h.svc.Unpay(r.Context(), userID, pmID, ym)
	})
}

func (h *Handler) CreateAdjustment(w http.ResponseWriter, r *http.Request) {
	var req adjustmentReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	h.respondStatus(w, r, http.StatusCreated, func(userID, pmID uuid.UUID, ym string) (domain.CardStatementDetail, error) {
		return h.svc.CreateAdjustment(r.Context(), userID, pmID, ym, req.input())
	})
}

func (h *Handler) UpdateAdjustment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "adjustmentId"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("adjustmentId", "no es un UUID válido"))
		return
	}
	var req adjustmentReq
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	h.respond(w, r, func(userID, pmID uuid.UUID, ym string) (domain.CardStatementDetail, error) {
		return h.svc.UpdateAdjustment(r.Context(), userID, pmID, ym, id, req.input())
	})
}

func (h *Handler) DeleteAdjustment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "adjustmentId"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("adjustmentId", "no es un UUID válido"))
		return
	}
	h.respond(w, r, func(userID, pmID uuid.UUID, ym string) (domain.CardStatementDetail, error) {
		return h.svc.DeleteAdjustment(r.Context(), userID, pmID, ym, id)
	})
}

// ===================== helpers =====================

func (h *Handler) respond(w http.ResponseWriter, r *http.Request, f func(userID, pmID uuid.UUID, ym string) (domain.CardStatementDetail, error)) {
	h.respondStatus(w, r, http.StatusOK, f)
}

func (h *Handler) respondStatus(w http.ResponseWriter, r *http.Request, status int, f func(userID, pmID uuid.UUID, ym string) (domain.CardStatementDetail, error)) {
	userID, pmID, ok := h.ids(w, r)
	if !ok {
		return
	}
	d, err := f(userID, pmID, chi.URLParam(r, "ym"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	httpx.WriteJSON(w, status, toDetailDTO(d))
}

func (h *Handler) ids(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := auth.UserIDFrom(r.Context())
	if !ok {
		httpx.WriteError(w, r, h.logger, domain.ErrUnauthorized)
		return uuid.Nil, uuid.Nil, false
	}
	pmID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("id", "no es un UUID válido"))
		return uuid.Nil, uuid.Nil, false
	}
	return userID, pmID, true
}
