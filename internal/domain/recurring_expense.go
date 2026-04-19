package domain

import (
	"time"

	"github.com/google/uuid"
)

// RecurringExpense: plantilla de gasto fijo (alquiler, Netflix, prepaga).
// El worker 00:30 la reproduce el día que corresponda llamando a
// expenses.Service.Create, así hereda toda la lógica de cuotas/shares/FX
// sin duplicarse.
//
// Nota: no guardamos SharesOverride en la plantilla. Si el user cambia los
// weights del hogar, los futuros gastos generados los toman al vuelo —
// más simple y suele ser lo que el user quiere (ej: si alguien se va del
// hogar, los recurrentes se redistribuyen solos).
type RecurringExpense struct {
	ID              uuid.UUID
	HouseholdID     uuid.UUID
	CreatedBy       uuid.UUID
	CategoryID      *uuid.UUID
	PaymentMethodID uuid.UUID

	Amount       float64
	Currency     string
	Description  string
	Installments int
	IsShared     bool

	Frequency   string
	DayOfMonth  *int
	DayOfWeek   *int
	MonthOfYear *int

	IsActive      bool
	StartsAt      time.Time
	EndsAt        *time.Time
	LastGenerated *time.Time
	CreatedAt     time.Time
}
