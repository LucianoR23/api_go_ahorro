// Package expenseparse turns a sentence such as "café 2500 visa 3 cuotas
// ayer" into the fields of a new expense (plan 3.1). Rules read it first;
// the AI is asked only when they cannot tell the amount, and its answers
// are kept in memory for 24 hours so the same sentence is not paid twice.
// Nothing is saved: the app shows the fields for the person to confirm.
//
// The AI receives only today's date and the sentence. Payment methods are
// matched here, from the words it returns, so their names never leave.
package expenseparse

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/ai"
	"github.com/LucianoR23/valuo_api/internal/categorize"
	"github.com/LucianoR23/valuo_api/internal/textnorm"
)

// MaxInputRunes is the longest sentence accepted (plan §8.3).
const MaxInputRunes = 300

const maxOutputTokens = 300

// Source says who understood the amount.
type Source string

const (
	SourceRules Source = "rules"
	SourceAI    Source = "ai"
)

// Result is a parsed sentence.
type Result struct {
	Fields
	CategoryID     uuid.UUID
	CategorySource categorize.Source
	// Understood is true when an amount was found.
	Understood bool
	// NotFinance is true when the AI says the sentence is not an expense.
	NotFinance bool
	Source     Source
	// Transcript is the sentence for the box: what the person said in a
	// voice note (plan 3.2), or what a receipt or screenshot shows written
	// as a sentence (plan 3.3). Empty for typed sentences and when nothing
	// could be read.
	Transcript string
}

// Store reads the person's active payment methods.
type Store interface {
	Methods(ctx context.Context, userID uuid.UUID) ([]Method, error)
}

// Runner is the part of ai.Service the parser uses.
type Runner interface {
	Enabled() bool
	Run(ctx context.Context, req ai.Request, out any) error
}

// Categorizer suggests a category for the description (plan 8.4).
type Categorizer interface {
	Suggest(ctx context.Context, userID, householdID uuid.UUID, description string) (categorize.Suggestion, bool, error)
}

// Parser reads sentences.
type Parser struct {
	store   Store
	ai      Runner
	cats    Categorizer
	prompt  ai.Prompt
	audio   ai.Prompt
	receipt ai.Prompt
	cache   *cache
	now     func() time.Time
	loc     *time.Location
	logger  *slog.Logger
}

// New builds a parser. A nil runner or categorizer turns that part off.
func New(store Store, r Runner, cats Categorizer, loc *time.Location, now func() time.Time, logger *slog.Logger) *Parser {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	if loc == nil {
		loc = time.UTC
	}
	return &Parser{
		store: store, ai: r, cats: cats, prompt: ai.MustPrompt("parse_expense", nil),
		audio: ai.MustPrompt("parse_audio", nil), receipt: ai.MustPrompt("parse_receipt", nil),
		cache: newCache(cacheTTL, cacheSize, now), now: now, loc: loc, logger: logger,
	}
}

// Parse reads text for userID. householdID is the space whose categories
// are suggested; the AI cost counts against userID.
func (p *Parser) Parse(ctx context.Context, userID, householdID uuid.UUID, text string) (Result, error) {
	text = truncate(strings.TrimSpace(text), MaxInputRunes)
	if text == "" {
		return Result{}, nil
	}
	today := p.today()

	methods, err := p.store.Methods(ctx, userID)
	if err != nil {
		return Result{}, err
	}
	r := parseRules(text, today, methods)
	res := Result{Fields: r.Fields, Understood: r.amountClear, Source: SourceRules}

	if !r.amountClear {
		if a, ok := p.askAI(ctx, userID, householdID, text, today); ok {
			if a.Intent == intentNotFinance {
				return Result{NotFinance: true, Source: SourceAI}, nil
			}
			res = merge(res, a, text, today, methods)
		}
	}

	p.categorize(ctx, userID, householdID, &res)
	return res, nil
}

// categorize fills the category from the description (plan 8.4).
func (p *Parser) categorize(ctx context.Context, userID, householdID uuid.UUID, res *Result) {
	if res.Description == "" || p.cats == nil {
		return
	}
	s, ok, err := p.cats.Suggest(ctx, userID, householdID, res.Description)
	if err != nil {
		p.logger.WarnContext(ctx, "expenseparse: category suggestion failed", "householdId", householdID.String(), "error", err)
	} else if ok {
		res.CategoryID, res.CategorySource = s.CategoryID, s.Source
	}
}

// today is the start of the current day in the app's time zone.
func (p *Parser) today() time.Time {
	y, m, d := p.now().In(p.loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, p.loc)
}

const (
	intentExpense    = "expense"
	intentNotFinance = "not_finance"
)

// answer is what the model returns. Empty strings and zeros mean "not
// said": the schema has no nulls.
type answer struct {
	Intent       string  `json:"intent"`
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Date         string  `json:"date"`
	Installments int     `json:"installments"`
	Payment      string  `json:"payment"`
	Description  string  `json:"description"`
	Shared       bool    `json:"shared"`
}

// Validate rejects values out of range; the caller drops the ones that are
// valid but do not fit the sentence.
func (a *answer) Validate() error {
	switch {
	case a.Intent != intentExpense && a.Intent != intentNotFinance:
		return errors.New("intent")
	case a.Amount < 0 || a.Amount > 1e12:
		return errors.New("amount")
	case a.Currency != "" && a.Currency != "ARS" && a.Currency != "USD" && a.Currency != "EUR":
		return errors.New("currency")
	case a.Installments < 0 || a.Installments > 60:
		return errors.New("installments")
	}
	if a.Date != "" {
		if _, err := time.Parse(time.DateOnly, a.Date); err != nil {
			return errors.New("date")
		}
	}
	return nil
}

var schema = mustJSON(map[string]any{
	"type": "object",
	"properties": map[string]any{
		"intent":       map[string]any{"type": "string", "enum": []string{intentExpense, intentNotFinance}},
		"amount":       map[string]any{"type": "number"},
		"currency":     map[string]any{"type": "string", "enum": []string{"", "ARS", "USD", "EUR"}},
		"date":         map[string]any{"type": "string"},
		"installments": map[string]any{"type": "integer"},
		"payment":      map[string]any{"type": "string"},
		"description":  map[string]any{"type": "string"},
		"shared":       map[string]any{"type": "boolean"},
	},
	"required":             []string{"intent", "amount", "currency", "date", "installments", "payment", "description", "shared"},
	"additionalProperties": false,
})

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (p *Parser) askAI(ctx context.Context, userID, householdID uuid.UUID, text string, today time.Time) (answer, bool) {
	if p.ai == nil || !p.ai.Enabled() || letters(text) < 3 {
		return answer{}, false
	}
	day := today.Format(time.DateOnly)
	key := cacheKey(userID, day, text)
	if a, ok := p.cache.get(key); ok {
		return a, true
	}
	// json.Marshal escapes < and >, so the text cannot close its tag.
	quoted, _ := json.Marshal(text)
	hh := householdID
	var a answer
	err := p.ai.Run(ctx, ai.Request{
		UserID: userID, HouseholdID: &hh,
		Task: ai.TaskParseExpense, Channel: ai.ChannelApp,
		PromptVersion: p.prompt.Version, System: p.prompt.System,
		Parts:           []ai.Part{ai.TextPart("<today>" + day + "</today>\n<text>" + string(quoted) + "</text>")},
		Schema:          schema,
		MaxOutputTokens: maxOutputTokens,
	}, &a)
	if err != nil {
		p.logger.InfoContext(ctx, "expenseparse: no AI answer", "prompt", p.prompt.Version, "reason", err.Error())
		return answer{}, false
	}
	p.cache.put(key, a)
	return a, true
}

// merge fills what the rules missed with the AI's answer. Words the AI
// returns must come from the sentence: it never writes a description of
// its own (plan §8.4). An amount the rules read clearly is kept.
func merge(res Result, a answer, text string, today time.Time, methods []Method) Result {
	res.Source = SourceAI
	if !res.Understood && a.Amount > 0 {
		res.Amount, res.Understood = a.Amount, true
	}
	if res.Currency == "" {
		res.Currency = a.Currency
	}
	if res.SpentAt.IsZero() && a.Date != "" {
		d, _ := time.ParseInLocation(time.DateOnly, a.Date, today.Location())
		if !d.After(today) && d.After(today.AddDate(-1, 0, 0)) {
			res.SpentAt = d
		}
	}
	if res.Installments == 0 {
		res.Installments = a.Installments
	}
	said := wordSet(text)
	if a.Description != "" && within(a.Description, said) {
		res.Description = capitalize(truncate(strings.TrimSpace(a.Description), maxDescriptionRunes))
	}
	if res.PaymentMethodID == uuid.Nil && len(res.Candidates) == 0 && a.Payment != "" && within(a.Payment, said) {
		var r ruleResult
		findPayment(tokenize(a.Payment), methods, &r)
		res.PaymentMethodID, res.Candidates = r.PaymentMethodID, r.Candidates
	}
	res.Shared = res.Shared || a.Shared
	return res
}

func wordSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(textnorm.Key(s)) {
		out[w] = true
	}
	return out
}

// within says whether every word of s is in the sentence.
func within(s string, said map[string]bool) bool {
	words := strings.Fields(textnorm.Key(s))
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !said[w] {
			return false
		}
	}
	return true
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
