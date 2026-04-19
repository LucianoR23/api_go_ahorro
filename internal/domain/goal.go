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
	CurrentAmount float64
	TargetAmount  float64
	Percent       float64
	Status        string // ok | warning | exceeded
}
