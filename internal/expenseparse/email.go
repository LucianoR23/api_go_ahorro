package expenseparse

// Forwarded emails (plan 3.5). internal/inbound reads the mails of the banks
// and wallets it knows with fixed rules; any other mail comes here and one AI
// call reads it with the receipt schema. What was read is kept as a Reading
// and turned into a sentence when the app shows it, so "ayer" is relative
// to the day the person looks at it. The mail text is only held in memory.

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/ai"
)

// MaxEmailRunes is how much of a mail's text goes to the AI: notification
// mails put the payment at the top, and the rest is legal text.
const MaxEmailRunes = 4000

// Reading is what a mail says about one expense. Date is YYYY-MM-DD or empty;
// Payment is up to three lower-case words ("visa galicia", "mercado pago").
type Reading struct {
	Amount       float64 `json:"amount"`
	Currency     string  `json:"currency"`
	Date         string  `json:"date"`
	Installments int     `json:"installments"`
	Payment      string  `json:"payment"`
	Description  string  `json:"description"`
}

// Sentence writes a reading the way a person would type it, in words the
// rules read ("Carrefour 15430,50 mercado pago ayer"), relative to today.
func Sentence(r Reading, today time.Time) string {
	return receiptSentence(answer{
		Intent: intentExpense, Amount: r.Amount, Currency: r.Currency, Date: r.Date,
		Installments: r.Installments, Payment: r.Payment, Description: r.Description,
	}, today)
}

// ReadEmail reads one mail for userID with the AI. ok is false when the mail
// is not an expense, or when the AI is off, over a limit or fails; the
// caller then keeps it as unreadable.
func (p *Parser) ReadEmail(ctx context.Context, userID, householdID uuid.UUID, subject, text string) (r Reading, finance, ok bool) {
	if p.ai == nil || !p.ai.Enabled() {
		return Reading{}, false, false
	}
	day := p.today().Format(time.DateOnly)
	hh := householdID
	var ra receiptAnswer
	err := p.ai.Run(ctx, ai.Request{
		UserID: userID, HouseholdID: &hh,
		Task: ai.TaskParseEmail, Channel: ai.ChannelEmail,
		PromptVersion: p.email.Version, System: p.email.System,
		Parts: []ai.Part{ai.TextPart("<today>" + day + "</today>\n<subject>" + truncate(oneLine(subject), 200) +
			"</subject>\n<mail>" + truncate(text, MaxEmailRunes) + "</mail>")},
		Schema:          receiptSchema,
		MaxOutputTokens: maxOutputTokens,
	}, &ra)
	if err != nil {
		p.logger.InfoContext(ctx, "expenseparse: no AI answer for email", "prompt", p.email.Version, "reason", err.Error())
		return Reading{}, false, false
	}
	if ra.Intent == intentNotFinance {
		return Reading{}, false, true
	}
	return Reading{
		Amount: ra.Amount, Currency: ra.Currency, Date: ra.Date, Installments: ra.Installments,
		Payment: oneLine(ra.Payment), Description: oneLine(ra.Description),
	}, true, true
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
