package domain

import (
	"time"

	"github.com/google/uuid"
)

// Income: ingreso ya materializado (sueldo cobrado, freelance pagado, etc.).
// Mismo patrón que Expense para fx: amount/currency es lo que el user cargó,
// amount_base/base_currency + rate congelado al momento de crear para que
// los reportes históricos sean estables.
//
// No tiene cuotas ni shares — la plata entra y ya. Se asocia al user que
// la recibió (no al hogar completo) para que balances futuros por miembro
// puedan sumar "cuánto aportó cada uno".
type Income struct {
	ID              uuid.UUID
	HouseholdID     uuid.UUID
	ReceivedBy      uuid.UUID
	PaymentMethodID *uuid.UUID // nullable: no siempre hay medio (ej: gift en mano)

	Amount       float64
	Currency     string
	AmountBase   float64
	BaseCurrency string
	RateUsed     *float64
	RateAt       *time.Time

	Source      string
	Description string
	ReceivedAt  time.Time
	CreatedAt   time.Time
}

// RecurringIncome: plantilla que el worker de incomes reproduce según
// frequency. Los día/mes nullables dependen del frequency:
//   - monthly → day_of_month (1..31), el resto NULL
//   - weekly  → day_of_week (0..6), el resto NULL
//   - yearly  → day_of_month + month_of_year
//
// last_generated evita doble-creación si el worker tickea dos veces el
// mismo día (fallos de red, restart).
type RecurringIncome struct {
	ID              uuid.UUID
	HouseholdID     uuid.UUID
	ReceivedBy      uuid.UUID
	PaymentMethodID *uuid.UUID

	Amount      float64
	Currency    string
	Description string
	Source      string

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

// Fuentes válidas (matchean los CHECK de la DB). El service valida antes
// de persistir para devolver 400 en vez de 500 cuando el input está mal.
var IncomeSources = []string{"salary", "freelance", "gift", "investment", "refund", "other"}

// Frecuencias válidas de recurring_incomes.
var IncomeFrequencies = []string{"monthly", "weekly", "yearly"}
