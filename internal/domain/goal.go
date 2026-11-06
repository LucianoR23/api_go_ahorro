package domain

import (
	"github.com/shopspring/decimal"
	"time"

	"github.com/google/uuid"
)

const (
	GoalScopeHousehold = "household"
	GoalScopeUser      = "user"

	GoalTypeCategoryLimit = "category_limit"
	GoalTypeTotalLimit    = "total_limit"
	GoalTypeSavings       = "savings"

	GoalPeriodMonthly = "monthly"
	GoalPeriodWeekly  = "weekly"
)

type BudgetGoal struct {
	ID           uuid.UUID
	HouseholdID  uuid.UUID
	Scope        string
	UserID       *uuid.UUID
	CategoryID   *uuid.UUID
	GoalType     string
	TargetAmount decimal.Decimal
	Currency     string
	Period       string
	IsActive     bool
	CreatedAt    time.Time
}

// BudgetGoalProgress: snapshot de progreso vivo de un goal.
// CurrentAmount está en base currency (ARS). Para limits, percent=current/target.
// Para savings, current puede ser negativo (gastos > ingresos).
type BudgetGoalProgress struct {
	Goal          BudgetGoal
	PeriodStart   time.Time
	PeriodEnd     time.Time
	CurrentAmount decimal.Decimal // in BaseCurrency
	TargetAmount  decimal.Decimal // in the goal's own currency
	// TargetAmountBase: the target in BaseCurrency (today's rate when the goal
	// is in another currency). Percent and Status are measured against it.
	TargetAmountBase decimal.Decimal
	BaseCurrency     string
	FxRate           *decimal.Decimal // nil when no conversion was needed
	Percent          float64
	Status           string // ok | warning | exceeded
}
