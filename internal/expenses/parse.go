package expenses

import (
	"context"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/expenseparse"
	"github.com/LucianoR23/valuo_api/internal/households"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

// Parser reads a sentence into expense fields (plan 3.1).
type Parser interface {
	Parse(ctx context.Context, userID, householdID uuid.UUID, text string) (expenseparse.Result, error)
}

// WithParser enables POST /expenses/parse.
func (h *Handler) WithParser(p Parser) *Handler {
	h.parser = p
	return h
}

type parseRequest struct {
	Text string `json:"text"`
}

// parseDTO has nulls for what the sentence does not say. paymentCandidates
// lists the methods that fit when the words name more than one; then
// paymentMethodId is null and the person picks.
type parseDTO struct {
	Understood        bool     `json:"understood"`
	NotFinance        bool     `json:"notFinance"`
	Source            string   `json:"source"`
	Amount            *float64 `json:"amount"`
	Currency          *string  `json:"currency"`
	SpentAt           *string  `json:"spentAt"`
	Installments      *int     `json:"installments"`
	Description       *string  `json:"description"`
	PaymentMethodID   *string  `json:"paymentMethodId"`
	PaymentCandidates []string `json:"paymentCandidates"`
	Shared            bool     `json:"shared"`
	CategoryID        *string  `json:"categoryId"`
	CategorySource    *string  `json:"categorySource"`
}

func ptr[T any](v T) *T { return &v }

func toParseDTO(r expenseparse.Result) parseDTO {
	out := parseDTO{
		Understood: r.Understood, NotFinance: r.NotFinance, Source: string(r.Source),
		Shared: r.Shared, PaymentCandidates: []string{},
	}
	if out.Source == "" {
		out.Source = string(expenseparse.SourceRules)
	}
	if r.Amount > 0 {
		out.Amount = ptr(r.Amount)
	}
	if r.Currency != "" {
		out.Currency = ptr(r.Currency)
	}
	if !r.SpentAt.IsZero() {
		out.SpentAt = ptr(r.SpentAt.Format("2006-01-02"))
	}
	if r.Installments > 0 {
		out.Installments = ptr(r.Installments)
	}
	if r.Description != "" {
		out.Description = ptr(r.Description)
	}
	if r.PaymentMethodID != uuid.Nil {
		out.PaymentMethodID = ptr(r.PaymentMethodID.String())
	}
	for _, id := range r.Candidates {
		out.PaymentCandidates = append(out.PaymentCandidates, id.String())
	}
	if r.CategoryID != uuid.Nil {
		out.CategoryID = ptr(r.CategoryID.String())
		out.CategorySource = ptr(string(r.CategorySource))
	}
	return out
}

// Parse answers 200 with whatever was understood; nothing is saved. An AI
// limit or a provider failure leaves only what the rules read.
func (h *Handler) Parse(w http.ResponseWriter, r *http.Request) {
	householdID, ok := households.HouseholdIDFrom(r.Context())
	userID, okUser := auth.UserIDFrom(r.Context())
	if !ok || !okUser {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}
	var req parseRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if utf8.RuneCountInString(req.Text) > expenseparse.MaxInputRunes {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("text", "es demasiado largo"))
		return
	}
	var res expenseparse.Result
	if h.parser != nil {
		var err error
		if res, err = h.parser.Parse(r.Context(), userID, householdID, req.Text); err != nil {
			httpx.WriteError(w, r, h.logger, err)
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, toParseDTO(res))
}
