package expenseparse

// Voice notes (plan 3.2). The app records up to 30 seconds and sends the
// audio here; one AI call returns what the person said plus the fields.
// The rules then read that transcript like a typed sentence and win where
// they are clear, so the app can show the transcript in the box and a
// correction typed there gives the same reading. The audio is only held in
// memory for the call and never stored (plan §8b).

import (
	"context"
	"errors"
	"mime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/ai"
)

// MaxAudioBytes caps a voice note. The app records about 30 seconds of
// Opus or AAC at 32 kbps (around 120 KB); browsers that ignore the bitrate
// stay well under this too.
const MaxAudioBytes = 1 << 20

// maxAudioOutputTokens leaves room for the transcript on top of the fields
// (plan §8.3 sets 300 for a typed sentence).
const maxAudioOutputTokens = 400

// ErrAudioType is returned for a format the provider does not take.
var ErrAudioType = errors.New("expenseparse: unsupported audio type")

// audioTypes maps what browsers send to what the provider takes. Safari
// records audio/mp4, which Gemini lists as audio/m4a.
var audioTypes = map[string]string{
	"audio/webm":  "audio/webm",
	"audio/ogg":   "audio/ogg",
	"audio/opus":  "audio/opus",
	"audio/mp4":   "audio/m4a",
	"audio/m4a":   "audio/m4a",
	"audio/x-m4a": "audio/m4a",
	"audio/aac":   "audio/aac",
	"audio/mpeg":  "audio/mpeg",
	"audio/wav":   "audio/wav",
	"audio/x-wav": "audio/wav",
}

// AudioType returns the provider MIME type for a Content-Type header
// ("audio/webm;codecs=opus" is audio/webm).
func AudioType(contentType string) (string, bool) {
	base, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", false
	}
	t, ok := audioTypes[base]
	return t, ok
}

// audioAnswer is the typed-sentence answer plus what was heard.
type audioAnswer struct {
	answer
	Transcript string `json:"transcript"`
}

var audioSchema = mustJSON(map[string]any{
	"type": "object",
	"properties": map[string]any{
		"intent":       map[string]any{"type": "string", "enum": []string{intentExpense, intentNotFinance}},
		"transcript":   map[string]any{"type": "string"},
		"amount":       map[string]any{"type": "number"},
		"currency":     map[string]any{"type": "string", "enum": []string{"", "ARS", "USD", "EUR"}},
		"date":         map[string]any{"type": "string"},
		"installments": map[string]any{"type": "integer"},
		"payment":      map[string]any{"type": "string"},
		"description":  map[string]any{"type": "string"},
		"shared":       map[string]any{"type": "boolean"},
	},
	"required": []string{"intent", "transcript", "amount", "currency", "date", "installments", "payment",
		"description", "shared"},
	"additionalProperties": false,
})

// ParseAudio reads a voice note for userID. When the AI is off, over a
// limit or fails, the result is empty (no transcript) and the app asks the
// person to type it.
func (p *Parser) ParseAudio(ctx context.Context, userID, householdID uuid.UUID, mimeType string, data []byte) (Result, error) {
	mimeType, ok := AudioType(mimeType)
	if !ok {
		return Result{}, ErrAudioType
	}
	if len(data) == 0 || p.ai == nil || !p.ai.Enabled() {
		return Result{}, nil
	}
	today := p.today()
	day := today.Format(time.DateOnly)
	hh := householdID
	var a audioAnswer
	err := p.ai.Run(ctx, ai.Request{
		UserID: userID, HouseholdID: &hh,
		Task: ai.TaskParseAudio, Channel: ai.ChannelApp,
		PromptVersion: p.audio.Version, System: p.audio.System,
		Parts:           []ai.Part{ai.TextPart("<today>" + day + "</today>"), ai.MediaPart(mimeType, data)},
		Schema:          audioSchema,
		MaxOutputTokens: maxAudioOutputTokens,
	}, &a)
	if err != nil {
		p.logger.InfoContext(ctx, "expenseparse: no AI answer for audio", "prompt", p.audio.Version, "reason", err.Error())
		return Result{}, nil
	}
	transcript := truncate(strings.Join(strings.Fields(a.Transcript), " "), MaxInputRunes)
	if transcript == "" {
		return Result{Source: SourceAI}, nil
	}
	if a.Intent == intentNotFinance {
		return Result{NotFinance: true, Source: SourceAI, Transcript: transcript}, nil
	}

	methods, err := p.store.Methods(ctx, userID)
	if err != nil {
		return Result{}, err
	}
	r := parseRules(transcript, today, methods)
	res := merge(Result{Fields: r.Fields, Understood: r.amountClear}, a.answer, transcript, today, methods)
	res.Transcript = transcript
	p.categorize(ctx, userID, householdID, &res)
	return res, nil
}

// Validate is promoted from the embedded answer.
var _ ai.Validator = (*audioAnswer)(nil)
