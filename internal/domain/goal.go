package domain

import (
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
	TargetAmount float64
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
	CurrentAmount float64 // in BaseCurrency
	TargetAmount  float64 // in the goal's own currency
	// TargetAmountBase: the target in BaseCurrency (today's rate when the goal
	// is in another currency). Percent and Status are measured against it.
	TargetAmountBase float64
	BaseCurrency     string
	FxRate           *float64 // nil when no conversion was needed
	Percent          float64
	Status           string // ok | warning | exceeded
}
