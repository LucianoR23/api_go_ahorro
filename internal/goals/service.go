package goals

import (
	"context"
	"github.com/LucianoR23/valuo_api/internal/money"
	"github.com/shopspring/decimal"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/ownership"
)

// householdLookup: valida membresía para scope=user (el user del goal debe
// ser miembro del hogar) y para el creador del goal en general.
type householdLookup interface {
	IsMember(ctx context.Context, householdID, userID uuid.UUID) (bool, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.Household, error)
}

// fxConverter: converts a goal's target into the household's base currency,
// with the same rates expenses use.
type fxConverter interface {
	Convert(ctx context.Context, amount decimal.Decimal, from, to string) (converted, rate decimal.Decimal, err error)
}

type Service struct {
	own        *ownership.Checker
	repo       *Repository
	households householdLookup
	fx         fxConverter
}

// SetOwnership wires the checks that client ids belong where they claim
// (plan 0.6). Without it, writes that carry those ids fail.
func (s *Service) SetOwnership(c *ownership.Checker) { s.own = c }

func NewService(repo *Repository, households householdLookup, fx fxConverter) *Service {
	return &Service{repo: repo, households: households, fx: fx}
}

// ===================== CRUD =====================

type CreateInput struct {
	HouseholdID  uuid.UUID
	CreatedBy    uuid.UUID
	Scope        string
	UserID       *uuid.UUID
	CategoryID   *uuid.UUID
	GoalType     string
	TargetAmount decimal.Decimal
	Currency     string
	Period       string
}

func (s *Service) Create(ctx context.Context, in CreateInput) (domain.BudgetGoal, error) {
	in.Scope = strings.ToLower(strings.TrimSpace(in.Scope))
	in.GoalType = strings.ToLower(strings.TrimSpace(in.GoalType))
	in.Period = strings.ToLower(strings.TrimSpace(in.Period))
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Scope == "" {
		in.Scope = domain.GoalScopeHousehold
	}
	if in.Period == "" {
		in.Period = domain.GoalPeriodMonthly
	}
	if in.Currency == "" {
		in.Currency = "ARS"
	}

	if err := validateGoal(in.Scope, in.GoalType, in.Period, in.Currency, in.CategoryID, in.UserID, in.TargetAmount); err != nil {
		return domain.BudgetGoal{}, err
	}
	if err := s.own.Category(ctx, in.CategoryID, in.HouseholdID); err != nil {
		return domain.BudgetGoal{}, err
	}
	ok, err := s.households.IsMember(ctx, in.HouseholdID, in.CreatedBy)
	if err != nil {
		return domain.BudgetGoal{}, err
	}
	if !ok {
		return domain.BudgetGoal{}, domain.NewValidationError("createdBy", "no es miembro del hogar")
	}
	if in.Scope == domain.GoalScopeUser {
		uok, err := s.households.IsMember(ctx, in.HouseholdID, *in.UserID)
		if err != nil {
			return domain.BudgetGoal{}, err
		}
		if !uok {
			return domain.BudgetGoal{}, domain.NewValidationError("userId", "no es miembro del hogar")
		}
	}

	return s.repo.Create(ctx, CreateParams{
		HouseholdID:  in.HouseholdID,
		Scope:        in.Scope,
		UserID:       in.UserID,
		CategoryID:   in.CategoryID,
		GoalType:     in.GoalType,
		TargetAmount: in.TargetAmount,
		Currency:     in.Currency,
		Period:       in.Period,
		IsActive:     true,
	})
}

func (s *Service) Get(ctx context.Context, householdID, id uuid.UUID) (domain.BudgetGoal, error) {
	g, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.BudgetGoal{}, err
	}
	if g.HouseholdID != householdID {
		return domain.BudgetGoal{}, domain.ErrNotFound
	}
	return g, nil
}

func (s *Service) List(ctx context.Context, householdID uuid.UUID, f ListFilters) ([]domain.BudgetGoal, error) {
	return s.repo.ListByHousehold(ctx, householdID, f)
}

type UpdateInput struct {
	CategoryID   *uuid.UUID
	TargetAmount decimal.Decimal
	Currency     string
	Period       string
}

func (s *Service) Update(ctx context.Context, householdID, id uuid.UUID, in UpdateInput) (domain.BudgetGoal, error) {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.BudgetGoal{}, err
	}
	if existing.HouseholdID != householdID {
		return domain.BudgetGoal{}, domain.ErrNotFound
	}
	in.Period = strings.ToLower(strings.TrimSpace(in.Period))
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	if in.Period == "" {
		in.Period = existing.Period
	}
	if in.Currency == "" {
		in.Currency = existing.Currency
	}
	if err := validateGoal(existing.Scope, existing.GoalType, in.Period, in.Currency, in.CategoryID, existing.UserID, in.TargetAmount); err != nil {
		return domain.BudgetGoal{}, err
	}
	if err := s.own.Category(ctx, in.CategoryID, householdID); err != nil {
		return domain.BudgetGoal{}, err
	}
	return s.repo.Update(ctx, id, UpdateParams{
		CategoryID:   in.CategoryID,
		TargetAmount: in.TargetAmount,
		Currency:     in.Currency,
		Period:       in.Period,
	})
}

func (s *Service) SetActive(ctx context.Context, householdID, id uuid.UUID, active bool) error {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return s.repo.SetActive(ctx, id, active)
}

func (s *Service) Delete(ctx context.Context, householdID, id uuid.UUID) error {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if existing.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return s.repo.Delete(ctx, id)
}

// ===================== progress =====================

// Progress: calcula el estado vivo del goal para el período que contiene `at`.
// Para limits: current = gastos del período, target = target_amount.
// Para savings: current = incomes - gastos (puede ser negativo).
func (s *Service) Progress(ctx context.Context, householdID uuid.UUID, id uuid.UUID, at time.Time) (domain.BudgetGoalProgress, error) {
	g, err := s.Get(ctx, householdID, id)
	if err != nil {
		return domain.BudgetGoalProgress{}, err
	}
	start, end := periodRange(g.Period, at)
	current, err := s.computeCurrent(ctx, g, start, end)
	if err != nil {
		return domain.BudgetGoalProgress{}, err
	}
	target, err := s.targetInBase(ctx, g)
	if err != nil {
		return domain.BudgetGoalProgress{}, err
	}
	return buildProgress(g, start, end, current, target), nil
}

// ProgressList: todos los goals activos del hogar con progreso calculado.
// Para dashboard. Si querés filtrar por usuario, pasá f.UserID.
func (s *Service) ProgressList(ctx context.Context, householdID uuid.UUID, f ListFilters, at time.Time) ([]domain.BudgetGoalProgress, error) {
	items, err := s.repo.ListByHousehold(ctx, householdID, f)
	if err != nil {
		return nil, err
	}
	out := make([]domain.BudgetGoalProgress, 0, len(items))
	for _, g := range items {
		start, end := periodRange(g.Period, at)
		current, err := s.computeCurrent(ctx, g, start, end)
		if err != nil {
			return nil, err
		}
		target, err := s.targetInBase(ctx, g)
		if err != nil {
			return nil, err
		}
		out = append(out, buildProgress(g, start, end, current, target))
	}
	return out, nil
}

// baseTarget: a goal's target in the household's base currency. Spending and
// incomes are summed in the base currency, so a USD or EUR target is
// converted with today's rate (decided in 0.1: simple, and the app says the
// percentage uses today's rate).
type baseTarget struct {
	Amount   decimal.Decimal
	Currency string
	Rate     *decimal.Decimal // nil when the goal is already in the base currency
}

func (s *Service) targetInBase(ctx context.Context, g domain.BudgetGoal) (baseTarget, error) {
	hh, err := s.households.GetByID(ctx, g.HouseholdID)
	if err != nil {
		return baseTarget{}, err
	}
	if g.Currency == hh.BaseCurrency {
		return baseTarget{Amount: g.TargetAmount, Currency: hh.BaseCurrency}, nil
	}
	amount, rate, err := s.fx.Convert(ctx, g.TargetAmount, g.Currency, hh.BaseCurrency)
	if err != nil {
		return baseTarget{}, err
	}
	return baseTarget{Amount: amount.Round(money.AmountScale), Currency: hh.BaseCurrency, Rate: &rate}, nil
}

func (s *Service) computeCurrent(ctx context.Context, g domain.BudgetGoal, from, to time.Time) (decimal.Decimal, error) {
	switch g.GoalType {
	case domain.GoalTypeCategoryLimit, domain.GoalTypeTotalLimit:
		var catFilter *uuid.UUID
		if g.GoalType == domain.GoalTypeCategoryLimit {
			catFilter = g.CategoryID
		}
		if g.Scope == domain.GoalScopeUser && g.UserID != nil {
			return s.repo.SumUserInstallments(ctx, g.HouseholdID, *g.UserID, catFilter, from, to)
		}
		return s.repo.SumHouseholdInstallments(ctx, g.HouseholdID, catFilter, from, to)

	case domain.GoalTypeSavings:
		if g.Scope == domain.GoalScopeUser && g.UserID != nil {
			inc, err := s.repo.SumUserIncomes(ctx, g.HouseholdID, *g.UserID, from, to)
			if err != nil {
				return decimal.Zero, err
			}
			exp, err := s.repo.SumUserInstallments(ctx, g.HouseholdID, *g.UserID, nil, from, to)
			if err != nil {
				return decimal.Zero, err
			}
			return inc.Sub(exp), nil
		}
		inc, err := s.repo.SumHouseholdIncomes(ctx, g.HouseholdID, from, to)
		if err != nil {
			return decimal.Zero, err
		}
		exp, err := s.repo.SumHouseholdInstallments(ctx, g.HouseholdID, nil, from, to)
		if err != nil {
			return decimal.Zero, err
		}
		return inc.Sub(exp), nil
	}
	return decimal.Zero, nil
}

// ===================== helpers =====================

func validateGoal(scope, goalType, period, currency string, categoryID, userID *uuid.UUID, target decimal.Decimal) error {
	switch scope {
	case domain.GoalScopeHousehold:
		if userID != nil {
			return domain.NewValidationError("userId", "debe ser nulo si scope=household")
		}
	case domain.GoalScopeUser:
		if userID == nil {
			return domain.NewValidationError("userId", "requerido si scope=user")
		}
	default:
		return domain.NewValidationError("scope", "debe ser household o user")
	}
	switch goalType {
	case domain.GoalTypeCategoryLimit:
		if categoryID == nil {
			return domain.NewValidationError("categoryId", "requerido si goalType=category_limit")
		}
	case domain.GoalTypeTotalLimit, domain.GoalTypeSavings:
		// categoryId ignorado en estos tipos.
	default:
		return domain.NewValidationError("goalType", "debe ser category_limit / total_limit / savings")
	}
	if period != domain.GoalPeriodMonthly && period != domain.GoalPeriodWeekly {
		return domain.NewValidationError("period", "debe ser monthly o weekly")
	}
	if !money.InRange(target) {
		return domain.NewValidationError("targetAmount", "monto fuera de rango")
	}
	if target.Sign() <= 0 {
		return domain.NewValidationError("targetAmount", "debe ser mayor a cero")
	}
	if currency != "ARS" && currency != "USD" && currency != "EUR" {
		return domain.NewValidationError("currency", "debe ser ARS / USD / EUR")
	}
	return nil
}

// periodRange: para monthly → 1er día..último día del mes de `at`.
// Para weekly → lunes..domingo que contienen `at`.
func periodRange(period string, at time.Time) (time.Time, time.Time) {
	y, m, d := at.Date()
	at = time.Date(y, m, d, 0, 0, 0, 0, at.Location())
	switch period {
	case domain.GoalPeriodWeekly:
		// ISO: lunes=1, domingo=0. Ajustamos para que lunes sea el 1er día.
		wd := int(at.Weekday())
		if wd == 0 {
			wd = 7
		}
		start := at.AddDate(0, 0, -(wd - 1))
		end := start.AddDate(0, 0, 6)
		return start, end
	default:
		start := time.Date(y, m, 1, 0, 0, 0, 0, at.Location())
		end := start.AddDate(0, 1, -1)
		return start, end
	}
}

// buildProgress: current is in the base currency, so percent and status use
// the target converted to it. For savings, percent is how much of the target
// is already saved.
func buildProgress(g domain.BudgetGoal, start, end time.Time, current decimal.Decimal, target baseTarget) domain.BudgetGoalProgress {
	percent := 0.0
	if target.Amount.Sign() > 0 {
		percent = current.Div(target.Amount).Mul(decimal.NewFromInt(100)).InexactFloat64()
	}
	return domain.BudgetGoalProgress{
		Goal:             g,
		PeriodStart:      start,
		PeriodEnd:        end,
		CurrentAmount:    current,
		TargetAmount:     g.TargetAmount,
		TargetAmountBase: target.Amount,
		BaseCurrency:     target.Currency,
		FxRate:           target.Rate,
		Percent:          percent,
		Status:           statusFor(g.GoalType, current, target.Amount),
	}
}

func statusFor(goalType string, current, target decimal.Decimal) string {
	if target.Sign() <= 0 {
		return "ok"
	}
	switch goalType {
	case domain.GoalTypeSavings:
		// Below the target is a warning at any point; exceeded never applies
		// (saving more than the target is good).
		if current.GreaterThanOrEqual(target) {
			return "ok"
		}
		return "warning"
	default:
		if current.GreaterThan(target) {
			return "exceeded"
		}
		if current.GreaterThanOrEqual(target.Mul(decimal.New(8, -1))) {
			return "warning"
		}
		return "ok"
	}
}
