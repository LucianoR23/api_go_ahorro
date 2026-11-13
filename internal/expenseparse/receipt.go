package expenseparse

// Receipts and screenshots (plan 3.3). The app sends one photo of a paper
// ticket or a screenshot of a Mercado Pago transfer or payment, resized to
// at most 1600 px; one AI call reads its fields. The server then writes a
// short sentence from those fields ("Carrefour 15430,50 visa ayer") that
// the app shows in the box, and the rules read that sentence like a typed
// one, so a correction typed there gives the same reading without sending
// the image again. The image is only held in memory for the call and
// never stored (plan §8b).

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/ai"
)

// MaxImageBytes caps an image (plan §8.3). The app resizes before sending,
// so a real photo arrives at a few hundred KB.
const MaxImageBytes = 5 << 20

// ErrImageType is returned for a format the provider does not take, or a
// body that is not the image its Content-Type says.
var ErrImageType = errors.New("expenseparse: unsupported image type")

var imageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// ImageType returns the base MIME type for a Content-Type header when it
// is one the provider takes.
func ImageType(contentType string) (string, bool) {
	base, _, err := mime.ParseMediaType(contentType)
	if err != nil || !imageTypes[base] {
		return "", false
	}
	return base, true
}

// sniffImage checks the bytes start like the type they claim, so nothing
// else reaches the provider under an image label.
func sniffImage(mimeType string, data []byte) bool {
	return http.DetectContentType(data) == mimeType
}

var receiptSchema = mustJSON(map[string]any{
	"type": "object",
	"properties": map[string]any{
		"intent":       map[string]any{"type": "string", "enum": []string{intentExpense, intentNotFinance}},
		"amount":       map[string]any{"type": "number"},
		"currency":     map[string]any{"type": "string", "enum": []string{"", "ARS", "USD", "EUR"}},
		"date":         map[string]any{"type": "string"},
		"installments": map[string]any{"type": "integer"},
		"payment":      map[string]any{"type": "string"},
		"description":  map[string]any{"type": "string"},
	},
	"required":             []string{"intent", "amount", "currency", "date", "installments", "payment", "description"},
	"additionalProperties": false,
})

// receiptAnswer has no "shared": an image never says it.
type receiptAnswer struct {
	Intent       string  `json:"intent"`
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Date         string  `json:"date"`
	Installments int     `json:"installments"`
	Payment      string  `json:"payment"`
	Description  string  `json:"description"`
}

func (r *receiptAnswer) Validate() error {
	a := r.answer()
	return a.Validate()
}

func (r *receiptAnswer) answer() answer {
	return answer{
		Intent: r.Intent, Amount: r.Amount, Currency: r.Currency, Date: r.Date,
		Installments: r.Installments, Payment: r.Payment, Description: r.Description,
	}
}

var _ ai.Validator = (*receiptAnswer)(nil)

// ParseReceipt reads an image for userID. When the AI is off, over a limit
// or fails, the result is empty and the app asks the person to type it.
func (p *Parser) ParseReceipt(ctx context.Context, userID, householdID uuid.UUID, mimeType string, data []byte) (Result, error) {
	mimeType, ok := ImageType(mimeType)
	if !ok || len(data) > 0 && !sniffImage(mimeType, data) {
		return Result{}, ErrImageType
	}
	if len(data) == 0 || p.ai == nil || !p.ai.Enabled() {
		return Result{}, nil
	}
	today := p.today()
	day := today.Format(time.DateOnly)
	hh := householdID
	var ra receiptAnswer
	err := p.ai.Run(ctx, ai.Request{
		UserID: userID, HouseholdID: &hh,
		Task: ai.TaskParseReceipt, Channel: ai.ChannelApp,
		PromptVersion: p.receipt.Version, System: p.receipt.System,
		Parts:           []ai.Part{ai.TextPart("<today>" + day + "</today>"), ai.MediaPart(mimeType, data)},
		Schema:          receiptSchema,
		MaxOutputTokens: maxOutputTokens,
	}, &ra)
	if err != nil {
		p.logger.InfoContext(ctx, "expenseparse: no AI answer for image", "prompt", p.receipt.Version, "reason", err.Error())
		return Result{}, nil
	}
	a := ra.answer()
	a.Description = strings.Join(strings.Fields(a.Description), " ")
	a.Payment = strings.Join(strings.Fields(a.Payment), " ")
	if a.Intent == intentNotFinance {
		return Result{NotFinance: true, Source: SourceAI}, nil
	}
	sentence := receiptSentence(a, today)
	if sentence == "" {
		return Result{Source: SourceAI}, nil
	}

	methods, err := p.store.Methods(ctx, userID)
	if err != nil {
		return Result{}, err
	}
	r := parseRules(sentence, today, methods)
	res := merge(Result{Fields: r.Fields, Understood: r.amountClear}, a, sentence, today, methods)
	res.Transcript = sentence
	p.categorize(ctx, userID, householdID, &res)
	return res, nil
}

// receiptSentence writes the fields the way a person would type them, in
// words the rules read (whitespace already collapsed): "Carrefour 15430,50 en 3 cuotas visa ayer".
func receiptSentence(a answer, today time.Time) string {
	var parts []string
	if a.Description != "" {
		parts = append(parts, truncate(a.Description, maxDescriptionRunes))
	}
	if a.Amount > 0 {
		n := strconv.FormatFloat(a.Amount, 'f', 2, 64)
		n = strings.TrimSuffix(n, ".00")
		n = strings.Replace(n, ".", ",", 1)
		switch a.Currency {
		case "USD":
			n = "u$s " + n
		case "EUR":
			n += " euros"
		}
		parts = append(parts, n)
	}
	if a.Installments > 1 {
		parts = append(parts, "en "+strconv.Itoa(a.Installments)+" cuotas")
	}
	if a.Payment != "" {
		parts = append(parts, a.Payment)
	}
	if d, err := time.ParseInLocation(time.DateOnly, a.Date, today.Location()); err == nil && !d.After(today) {
		switch {
		case d.Equal(today):
		case d.Equal(today.AddDate(0, 0, -1)):
			parts = append(parts, "ayer")
		case d.Year() == today.Year():
			parts = append(parts, d.Format("2/1"))
		default:
			parts = append(parts, d.Format("2/1/2006"))
		}
	}
	return truncate(strings.Join(parts, " "), MaxInputRunes)
}
