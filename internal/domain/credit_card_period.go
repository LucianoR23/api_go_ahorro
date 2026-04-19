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
