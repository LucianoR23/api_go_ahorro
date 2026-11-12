// Package summaries writes the text of the weekly and monthly spending
// summaries with AI (plan 8.2). The server computes every figure; the
// model only turns those facts into a short paragraph. Nothing else is
// sent: no descriptions, names or emails (plan §8.4, §8.5). When the AI is
// off, fails, or writes a number that is not in the facts, the caller keeps
// its rule-based text.
package summaries

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/ai"
)

// MaxBodyRunes caps the paragraph: it fits an in-app card and a push.
const MaxBodyRunes = 400

const maxOutputTokens = 400

// Runner is the part of ai.Service the writer uses.
type Runner interface {
	Enabled() bool
	Run(ctx context.Context, req ai.Request, out any) error
}

// Category is one of the categories that weighed the most.
type Category struct {
	Name   string `json:"name"`
	Amount string `json:"amount"`
	Share  string `json:"share,omitempty"` // "30%"
}

// WeeklyFacts are the week's figures, already written for people.
type WeeklyFacts struct {
	SpentThisWeek string     `json:"spentThisWeek"`
	Movements     int64      `json:"movements"`
	Categories    int64      `json:"categories"`
	SpentLastWeek string     `json:"spentLastWeek,omitempty"`
	Change        string     `json:"changeVsLastWeek,omitempty"` // "20% más"
	TopCategories []Category `json:"topCategories,omitempty"`
	StillDue      string     `json:"stillDueThisMonth,omitempty"`
}

// MonthlyFacts are the month's figures, already written for people.
type MonthlyFacts struct {
	Month          string     `json:"month"` // "octubre"
	Spent          string     `json:"spent"`
	SpentLastMonth string     `json:"spentMonthBefore,omitempty"`
	Change         string     `json:"changeVsMonthBefore,omitempty"`
	Income         string     `json:"income,omitempty"`
	FixedShare     string     `json:"fixedExpensesShare,omitempty"`
	TopCategories  []Category `json:"topCategories,omitempty"`
}

// Space is who a summary is for: the space, and the person its AI cost
// counts against (the owner).
type Space struct {
	HouseholdID uuid.UUID
	OwnerID     uuid.UUID
}

// Writer turns facts into a paragraph.
type Writer struct {
	ai      Runner
	weekly  ai.Prompt
	monthly ai.Prompt
	logger  *slog.Logger
}

// New loads the prompts. A nil runner gives a writer that always declines.
func New(r Runner, logger *slog.Logger) *Writer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Writer{
		ai:      r,
		weekly:  ai.MustPrompt("weekly_summary", nil),
		monthly: ai.MustPrompt("monthly_summary", nil),
		logger:  logger,
	}
}

// Weekly writes the week's paragraph, or returns false.
func (w *Writer) Weekly(ctx context.Context, sp Space, f WeeklyFacts) (string, bool) {
	return w.write(ctx, sp, false, f)
}

// Monthly writes the month's paragraph, or returns false.
func (w *Writer) Monthly(ctx context.Context, sp Space, f MonthlyFacts) (string, bool) {
	return w.write(ctx, sp, true, f)
}

var schema = json.RawMessage(`{"type":"object","properties":{"body":{"type":"string"}},"required":["body"],"additionalProperties":false}`)

// answer is what the model returns.
type answer struct {
	Body string `json:"body"`
}

// Validate keeps the answer to one plain paragraph.
func (a *answer) Validate() error {
	a.Body = strings.TrimSpace(a.Body)
	n := utf8.RuneCountInString(a.Body)
	if n == 0 || n > MaxBodyRunes {
		return fmt.Errorf("body has %d characters", n)
	}
	if strings.ContainsAny(a.Body, "\n\r<>*#`|") {
		return errors.New("body is not plain text")
	}
	low := strings.ToLower(a.Body)
	if strings.Contains(low, "http") || strings.Contains(low, "www.") {
		return errors.New("body has a link")
	}
	return nil
}

func (w *Writer) write(ctx context.Context, sp Space, monthly bool, facts any) (string, bool) {
	if w == nil || w.ai == nil || !w.ai.Enabled() {
		return "", false
	}
	p := w.weekly
	if monthly {
		p = w.monthly
	}
	// json.Marshal escapes < and >, so a category name cannot close the tag.
	data, err := json.Marshal(facts)
	if err != nil {
		return "", false
	}
	hh := sp.HouseholdID
	var out answer
	err = w.ai.Run(ctx, ai.Request{
		UserID: sp.OwnerID, HouseholdID: &hh,
		Task: ai.TaskSummarize, Channel: ai.ChannelSystem,
		PromptVersion: p.Version, System: p.System,
		Parts:           []ai.Part{ai.TextPart("<facts>" + string(data) + "</facts>")},
		Schema:          schema,
		MaxOutputTokens: maxOutputTokens,
	}, &out)
	if err != nil {
		w.logger.InfoContext(ctx, "summaries: kept the rule-based text", "householdId", hh.String(), "prompt", p.Version, "reason", err.Error())
		return "", false
	}
	if bad := strangeNumbers(out.Body, string(data)); len(bad) > 0 {
		w.logger.WarnContext(ctx, "summaries: answer has numbers not in the facts", "householdId", hh.String(), "prompt", p.Version, "numbers", bad)
		return "", false
	}
	return out.Body, true
}

// numberRe matches a written number: "85.000", "1.234,50", "20", "3".
var numberRe = regexp.MustCompile(`\d+(?:[.,]\d+)*`)

// strangeNumbers lists the numbers in body that the facts never mention:
// the model must only copy figures (plan §8.4: answers cite our numbers).
func strangeNumbers(body, facts string) []string {
	known := map[string]bool{}
	for _, n := range numberRe.FindAllString(facts, -1) {
		known[n] = true
	}
	var bad []string
	for _, n := range numberRe.FindAllString(body, -1) {
		if !known[n] {
			bad = append(bad, n)
		}
	}
	return bad
}
