package domain

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreditCardPeriod: cierre y vencimiento reales cargados para un mes
// específico de una tarjeta. Si no existe para un mes dado, el service
// cae a los defaults de la credit_card.
type CreditCardPeriod struct {
	CreditCardID uuid.UUID
	PeriodYM     string // "2026-05"
	ClosingDate  time.Time
	DueDate      time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PeriodYMFromDate formatea "YYYY-MM" a partir de una fecha. Usado para
// derivar el period_ym del closing_date al upsertear períodos.
func PeriodYMFromDate(t time.Time) string {
	return fmt.Sprintf("%04d-%02d", t.Year(), int(t.Month()))
}

// CardPeriodDates resolves the closing and due date of a card's period
// whose closing falls in closingMonth: the loaded override if there is one,
// otherwise the card's default days. A due day not after the closing day
// means the due date is in the next month. Days past the end of a month
// clamp to its last day (closing 31 in February is the 28th or 29th).
func CardPeriodDates(cc CreditCard, closingMonth time.Time, override *CreditCardPeriod) (closing, due time.Time) {
	if override != nil {
		return override.ClosingDate, override.DueDate
	}
	closing = clampDate(closingMonth.Year(), closingMonth.Month(), cc.DefaultClosingDay)
	dueMonth := closingMonth
	if cc.DefaultDueDay <= cc.DefaultClosingDay {
		dueMonth = time.Date(closingMonth.Year(), closingMonth.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	}
	return closing, clampDate(dueMonth.Year(), dueMonth.Month(), cc.DefaultDueDay)
}

// ParsePeriodYM reads "YYYY-MM" as the first day of that month (UTC).
func ParsePeriodYM(ym string) (time.Time, error) {
	t, err := time.Parse("2006-01", ym)
	if err != nil {
		return time.Time{}, NewValidationError("periodYm", "formato debe ser YYYY-MM")
	}
	return t, nil
}

func clampDate(year int, month time.Month, day int) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
