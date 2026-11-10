package domain

import (
	"github.com/shopspring/decimal"
	"time"

	"github.com/google/uuid"
)

// Expense: un gasto registrado. Puede tener 1..N cuotas.
// Los campos de conversión están denormalizados (se preservan cuando
// cambia el tipo de cambio a futuro).
type Expense struct {
	ID              uuid.UUID
	HouseholdID     uuid.UUID
	CreatedBy       uuid.UUID
	CategoryID      *uuid.UUID
	PaymentMethodID uuid.UUID

	Amount       decimal.Decimal
	Currency     string
	AmountBase   decimal.Decimal
	BaseCurrency string
	RateUsed     *decimal.Decimal
	RateAt       *time.Time
	RateSource   *RateSource // where RateUsed came from; nil without conversion

	Description  string
	SpentAt      time.Time
	Installments int
	IsShared     bool

	RecurringExpenseID *uuid.UUID

	// Status: "draft" para gastos generados por una recurrente de monto
	// variable que esperan confirmación; "confirmed" en el resto de los
	// casos (default). Los drafts no entran en reportes ni balances hasta
	// que el usuario los confirme con el monto real de la factura.
	Status string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ExpenseInstallment: una cuota del gasto. Para non-credit siempre hay 1
// (billing_date = spent_at, is_paid = true, due_date nil).
type ExpenseInstallment struct {
	ID                    uuid.UUID
	ExpenseID             uuid.UUID
	InstallmentNumber     int
	InstallmentAmount     decimal.Decimal
	InstallmentAmountBase decimal.Decimal
	BillingDate           time.Time
	DueDate               *time.Time
	IsPaid                bool
	PaidAt                *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// InstallmentShare: cuánto debe el user X por la cuota Y.
type InstallmentShare struct {
	InstallmentID  uuid.UUID
	UserID         uuid.UUID
	AmountBaseOwed decimal.Decimal
}

// ExpenseDetail: vista completa (expense + installments + shares)
// para el endpoint GET /expenses/{id}.
type ExpenseDetail struct {
	Expense      Expense
	Installments []InstallmentWithShares
}

type InstallmentWithShares struct {
	Installment ExpenseInstallment
	Shares      []InstallmentShare
}
