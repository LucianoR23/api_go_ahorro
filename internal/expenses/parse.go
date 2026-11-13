package expenses

import (
	"context"
	"errors"
	"io"
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
	ParseAudio(ctx context.Context, userID, householdID uuid.UUID, mimeType string, data []byte) (expenseparse.Result, error)
	ParseReceipt(ctx context.Context, userID, householdID uuid.UUID, mimeType string, data []byte) (expenseparse.Result, error)
}

// WithParser enables POST /expenses/parse, /parse-audio and /parse-receipt.
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
	// Transcript is the sentence for the box: what a voice note said (plan
	// 3.2) or what an image shows (plan 3.3). Null for text, and when
	// nothing could be read.
	Transcript *string `json:"transcript"`
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
	if r.Transcript != "" {
		out.Transcript = ptr(r.Transcript)
	}
	return out
}

// ParseAudio takes a voice note as the raw body, with its audio
// Content-Type (plan 3.2). Like Parse, it answers 200 with whatever was
// understood and saves nothing; the audio is not kept.
func (h *Handler) ParseAudio(w http.ResponseWriter, r *http.Request) {
	householdID, ok := households.HouseholdIDFrom(r.Context())
	userID, okUser := auth.UserIDFrom(r.Context())
	if !ok || !okUser {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}
	contentType := r.Header.Get("Content-Type")
	if _, ok := expenseparse.AudioType(contentType); !ok {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("audio", "formato no soportado"))
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, expenseparse.MaxAudioBytes))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("audio", "es demasiado largo"))
		return
	case err != nil:
		httpx.WriteError(w, r, h.logger, err)
		return
	case len(data) == 0:
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("audio", "está vacío"))
		return
	}
	var res expenseparse.Result
	if h.parser != nil {
		if res, err = h.parser.ParseAudio(r.Context(), userID, householdID, contentType, data); err != nil {
			httpx.WriteError(w, r, h.logger, err)
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, toParseDTO(res))
}

// ParseReceipt takes one image of a ticket or a payment screenshot as the
// raw body, with its image Content-Type (plan 3.3). Like Parse, it answers
// 200 with whatever was read and saves nothing; the image is not kept.
func (h *Handler) ParseReceipt(w http.ResponseWriter, r *http.Request) {
	householdID, ok := households.HouseholdIDFrom(r.Context())
	userID, okUser := auth.UserIDFrom(r.Context())
	if !ok || !okUser {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}
	contentType := r.Header.Get("Content-Type")
	if _, ok := expenseparse.ImageType(contentType); !ok {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("image", "formato no soportado"))
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, expenseparse.MaxImageBytes))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("image", "es demasiado grande"))
		return
	case err != nil:
		httpx.WriteError(w, r, h.logger, err)
		return
	case len(data) == 0:
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("image", "está vacía"))
		return
	}
	var res expenseparse.Result
	if h.parser != nil {
		res, err = h.parser.ParseReceipt(r.Context(), userID, householdID, contentType, data)
		switch {
		case errors.Is(err, expenseparse.ErrImageType):
			httpx.WriteError(w, r, h.logger, domain.NewValidationError("image", "formato no soportado"))
			return
		case err != nil:
			httpx.WriteError(w, r, h.logger, err)
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, toParseDTO(res))
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
