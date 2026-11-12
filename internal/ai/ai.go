// Package ai is the only door to AI providers (plan 8.1). It exposes no
// generic chat: each feature calls Run with its own task, prompt and JSON
// schema, and gets back a value decoded and validated against a Go struct.
// Before a call it checks the guardrails of plan §8 (kill switch, verified
// email, per-user rate and cost limits, global daily budget), and after it
// records the call's metrics in ai_usage. Text sent or received is never
// stored or logged.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Task names one AI feature. The value is stored in ai_usage and is the key
// of AI_ROUTES and AI_DISABLED_TASKS.
type Task string

const (
	TaskParseExpense      Task = "parse_expense"
	TaskParseReceipt      Task = "parse_receipt"
	TaskParseAudio        Task = "parse_audio"
	TaskParseEmail        Task = "parse_email"
	TaskParseStatementPDF Task = "parse_statement_pdf"
	TaskCategorize        Task = "categorize"
	TaskSummarize         Task = "summarize"
	TaskAnswerQuestion    Task = "answer_finance_question"
)

// Channel is where the request came from.
type Channel string

const (
	ChannelApp      Channel = "app"
	ChannelTelegram Channel = "telegram"
	ChannelWhatsApp Channel = "whatsapp"
	ChannelEmail    Channel = "email"
	ChannelSystem   Channel = "system" // scheduled work, such as summaries
)

// Part is one piece of the user message: text, or one media file (image,
// PDF or audio) with its MIME type.
type Part struct {
	Text string
	MIME string
	Data []byte
}

// TextPart and MediaPart build parts.
func TextPart(s string) Part                  { return Part{Text: s} }
func MediaPart(mime string, data []byte) Part { return Part{MIME: mime, Data: data} }

func (p Part) isMedia() bool { return len(p.Data) > 0 }

// Request is one task run for one person.
type Request struct {
	UserID      uuid.UUID
	HouseholdID *uuid.UUID
	Task        Task
	Channel     Channel
	// PromptVersion identifies the prompt template, stored with each call.
	PromptVersion string
	// System holds the instructions. User content goes only in Parts,
	// delimited by the task as data, never as instructions (plan §8.5).
	System string
	Parts  []Part
	// Schema is the JSON schema the answer must follow (structured output).
	Schema json.RawMessage
	// MaxOutputTokens caps the answer (plan §8.3: parse 300, summary 600).
	MaxOutputTokens int
}

// Validator is implemented by answers that check their own values after
// decoding (amount > 0, a known currency, and so on).
type Validator interface {
	Validate() error
}

// Errors returned by Run. Features turn them into a friendly message and
// fall back to manual entry.
var (
	ErrDisabled      = errors.New("ai: disabled")
	ErrUnverified    = errors.New("ai: email not verified")
	ErrRateLimited   = errors.New("ai: too many requests")
	ErrUserBudget    = errors.New("ai: personal daily limit reached")
	ErrGlobalBudget  = errors.New("ai: daily budget reached")
	ErrInvalidOutput = errors.New("ai: invalid answer")
	ErrProvider      = errors.New("ai: provider failed")
	ErrBadRequest    = errors.New("ai: bad request")
)

// Call is what a provider receives: the request plus its route.
type Call struct {
	Model           string
	Effort          string
	Thinking        string
	System          string
	Parts           []Part
	Schema          json.RawMessage
	MaxOutputTokens int
}

// Output is a provider's answer and what it consumed.
type Output struct {
	Text         string
	InputTokens  int // every input token, audio included
	AudioTokens  int // the audio part of InputTokens
	OutputTokens int // thinking included
	// Truncated is true when the answer hit MaxOutputTokens.
	Truncated bool
}

// Provider talks to one AI vendor.
type Provider interface {
	Name() string
	Generate(ctx context.Context, c Call) (Output, error)
}

// ProviderError is a failed provider call. Retryable marks the ones worth a
// second attempt (rate limits, overload, 5xx, timeouts).
type ProviderError struct {
	Status    int
	Retryable bool
	Timeout   bool
	Msg       string
}

func (e *ProviderError) Error() string { return "ai provider: " + e.Msg }

// Limits are the guardrails of plan §8.2, configurable by env.
type Limits struct {
	PerMinute         int
	PerDay            int
	UserDailyMicroUSD int64
	DailyMicroUSD     int64
}

// MicroUSD converts dollars to millionths of a dollar.
func MicroUSD(usd float64) int64 { return int64(usd*1e6 + 0.5) }

// callTimeout bounds one provider call.
const callTimeout = 45 * time.Second
