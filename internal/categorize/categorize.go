// Package categorize suggests a category for a new expense from its
// description (plan 8.4). It tries the cheap sources first: the space's own
// history, and only then the AI, which counts against the person's AI
// limits. The rules of plan 3.8 will go first in the same chain.
//
// The AI receives only the description and the names of the space's
// categories: no amount, date, people or emails (decided with the user).
package categorize

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/ai"
	"github.com/LucianoR23/valuo_api/internal/textnorm"
)

// MaxDescriptionRunes caps what is matched and sent to the AI.
const MaxDescriptionRunes = 200

const maxOutputTokens = 100

// Source says where a suggestion came from.
type Source string

const (
	SourceHistory Source = "history"
	SourceAI      Source = "ai"
)

// Suggestion is a category for the description.
type Suggestion struct {
	CategoryID uuid.UUID
	Source     Source
}

// Category is one category of the space.
type Category struct {
	ID   uuid.UUID
	Name string
}

// Past is one earlier expense of the space with its category.
type Past struct {
	Description string
	CategoryID  uuid.UUID
}

// Store reads the space's categories and history (newest first).
type Store interface {
	Categories(ctx context.Context, householdID uuid.UUID) ([]Category, error)
	History(ctx context.Context, householdID uuid.UUID) ([]Past, error)
}

// Runner is the part of ai.Service the suggester uses.
type Runner interface {
	Enabled() bool
	Run(ctx context.Context, req ai.Request, out any) error
}

// Suggester runs the chain.
type Suggester struct {
	store  Store
	ai     Runner
	prompt ai.Prompt
	logger *slog.Logger
}

// New loads the prompt. A nil runner gives a suggester that only uses
// history.
func New(store Store, r Runner, logger *slog.Logger) *Suggester {
	if logger == nil {
		logger = slog.Default()
	}
	return &Suggester{store: store, ai: r, prompt: ai.MustPrompt("categorize", nil), logger: logger}
}

// Suggest returns a category for the description, or false when no source
// is confident. userID is who asks: the AI cost counts against them.
func (s *Suggester) Suggest(ctx context.Context, userID, householdID uuid.UUID, description string) (Suggestion, bool, error) {
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > MaxDescriptionRunes {
		description = string([]rune(description)[:MaxDescriptionRunes])
	}
	key := textnorm.Key(description)
	if letters(key) < 2 {
		return Suggestion{}, false, nil
	}
	cats, err := s.store.Categories(ctx, householdID)
	if err != nil {
		return Suggestion{}, false, err
	}
	if len(cats) == 0 {
		return Suggestion{}, false, nil
	}
	known := make(map[uuid.UUID]bool, len(cats))
	for _, c := range cats {
		known[c.ID] = true
	}

	past, err := s.store.History(ctx, householdID)
	if err != nil {
		return Suggestion{}, false, err
	}
	if id, ok := fromHistory(key, past, known); ok {
		return Suggestion{CategoryID: id, Source: SourceHistory}, true, nil
	}

	if id, ok := s.fromAI(ctx, userID, householdID, description, key, cats); ok {
		return Suggestion{CategoryID: id, Source: SourceAI}, true, nil
	}
	return Suggestion{}, false, nil
}

// fromHistory looks for earlier expenses with the same description, and
// then for ones that start with the same word ("Uber a casa", "Uber
// centro"). A match by first word needs two thirds of those expenses in one
// category, so a generic word such as "pago" does not decide. Ties go to the
// most recent.
func fromHistory(key string, past []Past, known map[uuid.UUID]bool) (uuid.UUID, bool) {
	first := firstWord(key)
	var exact, byWord votes
	for _, p := range past {
		if !known[p.CategoryID] {
			continue
		}
		k := textnorm.Key(p.Description)
		if k == key {
			exact.add(p.CategoryID)
		}
		if first != "" && firstWord(k) == first {
			byWord.add(p.CategoryID)
		}
	}
	if id, n := exact.top(); n > 0 {
		return id, true
	}
	if id, n := byWord.top(); n > 0 && 3*n >= 2*byWord.total {
		return id, true
	}
	return uuid.Nil, false
}

// votes counts categories in the order they first appear.
type votes struct {
	order []uuid.UUID
	count map[uuid.UUID]int
	total int
}

func (v *votes) add(id uuid.UUID) {
	if v.count == nil {
		v.count = map[uuid.UUID]int{}
	}
	if v.count[id] == 0 {
		v.order = append(v.order, id)
	}
	v.count[id]++
	v.total++
}

// top returns the most voted category; on a tie, the one seen first.
func (v *votes) top() (uuid.UUID, int) {
	var best uuid.UUID
	n := 0
	for _, id := range v.order {
		if v.count[id] > n {
			best, n = id, v.count[id]
		}
	}
	return best, n
}

// firstWord is the first word with at least 3 letters, or "".
func firstWord(key string) string {
	w, _, _ := strings.Cut(key, " ")
	if letters(w) < 3 {
		return ""
	}
	return w
}

func letters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// answer is what the model returns. The schema limits category to the
// space's names; the caller checks it again after decoding.
type answer struct {
	Match    bool   `json:"match"`
	Category string `json:"category"`
}

// Validate rejects a match without a category.
func (a *answer) Validate() error {
	if a.Match && a.Category == "" {
		return errors.New("match without a category")
	}
	return nil
}

func (s *Suggester) fromAI(ctx context.Context, userID, householdID uuid.UUID, description, key string, cats []Category) (uuid.UUID, bool) {
	if s.ai == nil || !s.ai.Enabled() || letters(key) < 3 {
		return uuid.Nil, false
	}
	names := make([]string, len(cats))
	byName := make(map[string]uuid.UUID, len(cats))
	for i, c := range cats {
		names[i] = c.Name
		byName[c.Name] = c.ID
	}
	// json.Marshal escapes < and >, so the text cannot close a tag.
	desc, _ := json.Marshal(description)
	list, _ := json.Marshal(names)
	schema, _ := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"match":    map[string]any{"type": "boolean"},
			"category": map[string]any{"type": "string", "enum": names},
		},
		"required":             []string{"match", "category"},
		"additionalProperties": false,
	})
	hh := householdID
	var out answer
	err := s.ai.Run(ctx, ai.Request{
		UserID: userID, HouseholdID: &hh,
		Task: ai.TaskCategorize, Channel: ai.ChannelApp,
		PromptVersion: s.prompt.Version, System: s.prompt.System,
		Parts:           []ai.Part{ai.TextPart("<description>" + string(desc) + "</description>\n<categories>" + string(list) + "</categories>")},
		Schema:          schema,
		MaxOutputTokens: maxOutputTokens,
	}, &out)
	if err != nil {
		s.logger.InfoContext(ctx, "categorize: no AI suggestion", "householdId", hh.String(), "prompt", s.prompt.Version, "reason", err.Error())
		return uuid.Nil, false
	}
	id, known := byName[out.Category]
	if !out.Match || !known {
		return uuid.Nil, false
	}
	return id, true
}
