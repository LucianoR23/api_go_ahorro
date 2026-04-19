package domain

import (
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

	Amount       float64
	Currency     string
	AmountBase   float64
	BaseCurrency string
	RateUsed     *float64
	RateAt       *time.Time

	Description  string
	SpentAt      time.Time
	Installments int
	IsShared     bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ExpenseInstallment: una cuota del gasto. Para non-credit siempre hay 1
// (billing_date = spent_at, is_paid = true, due_date nil).
type ExpenseInstallment struct {
	ID                    uuid.UUID
	ExpenseID             uuid.UUID
	InstallmentNumber     int
	InstallmentAmount     float64
	InstallmentAmountBase float64
	BillingDate           time.Time
	DueDate               *time.Time
	IsPaid                bool
	PaidAt                *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// InstallmentShare: cuánto debe el user X por la cuota Y.
type InstallmentShare struct {
	InstallmentID   uuid.UUID
	UserID          uuid.UUID
	AmountBaseOwed  float64
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
