package categories

import (
	"context"
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/categorize"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/households"
	"github.com/LucianoR23/valuo_api/internal/httpx"
)

// Suggester picks a category for a description (plan 8.4).
type Suggester interface {
	Suggest(ctx context.Context, userID, householdID uuid.UUID, description string) (categorize.Suggestion, bool, error)
}

// WithSuggester enables POST /categories/suggest.
func (h *Handler) WithSuggester(s Suggester) *Handler {
	h.suggester = s
	return h
}

// maxSuggestInput rejects pasted essays; the suggester itself only reads
// the first categorize.MaxDescriptionRunes.
const maxSuggestInput = 500

type suggestRequest struct {
	Description string `json:"description"`
}

// suggestDTO has a null categoryId when no source is confident.
type suggestDTO struct {
	CategoryID *string `json:"categoryId"`
	Source     *string `json:"source"`
}

// Suggest answers 200 with or without a suggestion: a missing suggestion,
// an AI limit or a provider failure all leave the form as it is.
func (h *Handler) Suggest(w http.ResponseWriter, r *http.Request) {
	householdID, ok := households.HouseholdIDFrom(r.Context())
	userID, okUser := auth.UserIDFrom(r.Context())
	if !ok || !okUser {
		httpx.WriteError(w, r, h.logger, errors.New("middleware faltante"))
		return
	}
	var req suggestRequest
	if err := decodeJSON(r, &req); err != nil {
		httpx.WriteError(w, r, h.logger, err)
		return
	}
	if utf8.RuneCountInString(req.Description) > maxSuggestInput {
		httpx.WriteError(w, r, h.logger, domain.NewValidationError("description", "es demasiado larga"))
		return
	}
	var out suggestDTO
	if h.suggester != nil {
		s, found, err := h.suggester.Suggest(r.Context(), userID, householdID, req.Description)
		if err != nil {
			httpx.WriteError(w, r, h.logger, err)
			return
		}
		if found {
			id, src := s.CategoryID.String(), string(s.Source)
			out = suggestDTO{CategoryID: &id, Source: &src}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
