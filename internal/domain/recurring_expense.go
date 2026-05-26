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

	// AmountIsVariable: cuando es true, el worker crea el expense en status
	// 'draft' usando Amount como estimado. El user confirma con el monto real
	// (luz, expensas, wifi). Cuando es false, comportamiento clásico (Netflix).
	AmountIsVariable bool
	// AlertThresholdPct: si el monto confirmado sube más de este % vs
	// LastAmount, se dispara un insight 'recurring_spike'. NULL = sin alerta.
	AlertThresholdPct *float64
	// LastAmount + LastConfirmedAt: cache del último monto realmente pagado,
	// poblado al confirmar un draft. Permite mostrar la serie sin recomputar.
	LastAmount      *float64
	LastConfirmedAt *time.Time
}

// SeriesStats: resumen analítico de una serie (recurring_expense con monto
// variable) calculado a partir de sus expenses confirmados. Lo devuelve el
// endpoint /recurring-expenses/{id}/stats.
type SeriesStats struct {
	RecurringExpenseID uuid.UUID
	History            []SeriesPoint // ordenado desc por SpentAt
	AverageLastN       float64       // promedio de los N puntos devueltos
	LastVariationPct   *float64      // delta % del último vs anteúltimo
}

// SeriesPoint: un cargo confirmado dentro de una serie. VariationPct compara
// contra el punto inmediatamente anterior (más viejo). nil en el más antiguo.
type SeriesPoint struct {
	ExpenseID     uuid.UUID
	Amount        float64
	Currency      string
	SpentAt       time.Time
	VariationPct  *float64
}
