package domain

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Credit card statements (plan 4.1). A statement is computed, not stored:
// the installments of a card billed in a closing month plus the manual
// adjustments. Only what the user enters is persisted.

type StatementStatus string

const (
	// StatementOpen: the period hasn't closed yet; its total still grows.
	StatementOpen StatementStatus = "open"
	// StatementClosed: closed and not yet due.
	StatementClosed StatementStatus = "closed"
	// StatementOverdue: past its due date and not marked paid.
	StatementOverdue StatementStatus = "overdue"
	StatementPaid    StatementStatus = "paid"
)

// StatementStatusOn derives the status of a statement on a given day.
// Dates are calendar days (midnight UTC); the closing day still belongs to
// the open period and the due day is not yet overdue.
func StatementStatusOn(closing, due time.Time, paidAt *time.Time, today time.Time) StatementStatus {
	switch {
	case paidAt != nil:
		return StatementPaid
	case !today.After(closing):
		return StatementOpen
	case today.After(due):
		return StatementOverdue
	default:
		return StatementClosed
	}
}

// OpenPeriodMonth is the closing month of the period that is open on
// today: this month if its closing is today or later, the next one
// otherwise. periodDates resolves a month's closing (override or default).
func OpenPeriodMonth(today time.Time, periodDates func(closingMonth time.Time) (closing time.Time)) time.Time {
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	if today.After(periodDates(month)) {
		return month.AddDate(0, 1, 0)
	}
	return month
}

type AdjustmentKind string

const (
	AdjustmentInterest AdjustmentKind = "interest"
	AdjustmentTax      AdjustmentKind = "tax"
	AdjustmentFee      AdjustmentKind = "fee"
	AdjustmentCredit   AdjustmentKind = "credit"
	AdjustmentOther    AdjustmentKind = "other"
)

func (k AdjustmentKind) Valid() bool {
	switch k {
	case AdjustmentInterest, AdjustmentTax, AdjustmentFee, AdjustmentCredit, AdjustmentOther:
		return true
	}
	return false
}

// Signed: a credit lowers the statement, everything else raises it.
func (k AdjustmentKind) Signed(amount decimal.Decimal) decimal.Decimal {
	if k == AdjustmentCredit {
		return amount.Neg()
	}
	return amount
}

type StatementAdjustment struct {
	ID          uuid.UUID
	Kind        AdjustmentKind
	Description string
	Amount      decimal.Decimal
	Currency    string
	CreatedAt   time.Time
}

// StatementRecord: the persisted part of a statement (zero value when the
// user never touched it).
type StatementRecord struct {
	MinimumPayment *decimal.Decimal
	PaidAt         *time.Time
	PaidAmount     *decimal.Decimal
	PaidCurrency   *string
}

// CurrencyAmount: statements keep each currency apart, as banks print them.
type CurrencyAmount struct {
	Currency string
	Amount   decimal.Decimal
}

type CardStatement struct {
	PeriodYM         string
	ClosingDate      time.Time
	DueDate          time.Time
	Status           StatementStatus
	Totals           []CurrencyAmount
	InstallmentCount int
	StatementRecord
}

// StatementInstallment: one installment of a statement, with what the
// screen needs from its expense. HouseholdID tells Mi plata from Hogar.
type StatementInstallment struct {
	InstallmentID     uuid.UUID
	ExpenseID         uuid.UUID
	HouseholdID       uuid.UUID
	CategoryID        *uuid.UUID
	Description       string
	SpentAt           time.Time
	InstallmentNumber int
	TotalInstallments int
	Amount            decimal.Decimal
	Currency          string
	IsPaid            bool
}

type CardStatementDetail struct {
	CardStatement
	Installments []StatementInstallment
	Adjustments  []StatementAdjustment
}
