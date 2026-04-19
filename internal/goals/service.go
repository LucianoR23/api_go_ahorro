package goals

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// householdLookup: valida membresía para scope=user (el user del goal debe
// ser miembro del hogar) y para el creador del goal en general.
type householdLookup interface {
	IsMember(ctx context.Context, householdID, userID uuid.UUID) (bool, error)
}

type Service struct {
	repo       *Repository
	households householdLookup
}

func NewService(repo *Repository, households householdLookup) *Service {
	return &Service{repo: repo, households: households}
}

// ===================== CRUD =====================

type CreateInput struct {
	HouseholdID  uuid.UUID
	CreatedBy    uuid.UUID
	Scope        string
	UserID       *uuid.UUID
	CategoryID   *uuid.UUID
	GoalType     string
	TargetAmount float64
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
	TargetAmount float64
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
	return buildProgress(g, start, end, current), nil
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
		out = append(out, buildProgress(g, start, end, current))
	}
	return out, nil
}

func (s *Service) computeCurrent(ctx context.Context, g domain.BudgetGoal, from, to time.Time) (float64, error) {
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
				return 0, err
			}
			exp, err := s.repo.SumUserInstallments(ctx, g.HouseholdID, *g.UserID, nil, from, to)
			if err != nil {
				return 0, err
			}
			return inc - exp, nil
		}
		inc, err := s.repo.SumHouseholdIncomes(ctx, g.HouseholdID, from, to)
		if err != nil {
			return 0, err
		}
		exp, err := s.repo.SumHouseholdInstallments(ctx, g.HouseholdID, nil, from, to)
		if err != nil {
			return 0, err
		}
		return inc - exp, nil
	}
	return 0, nil
}

// ===================== helpers =====================

func validateGoal(scope, goalType, period, currency string, categoryID, userID *uuid.UUID, target float64) error {
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
	if target <= 0 {
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

func buildProgress(g domain.BudgetGoal, start, end time.Time, current float64) domain.BudgetGoalProgress {
	percent := 0.0
	if g.TargetAmount > 0 {
		switch g.GoalType {
		case domain.GoalTypeSavings:
			// Para savings, percent expresa cuánto del target ya ahorramos.
			percent = (current / g.TargetAmount) * 100
		default:
			percent = (current / g.TargetAmount) * 100
		}
	}
	status := statusFor(g.GoalType, current, g.TargetAmount)
	return domain.BudgetGoalProgress{
		Goal:          g,
		PeriodStart:   start,
		PeriodEnd:     end,
		CurrentAmount: current,
		TargetAmount:  g.TargetAmount,
		Percent:       percent,
		Status:        status,
	}
}

func statusFor(goalType string, current, target float64) string {
	switch goalType {
	case domain.GoalTypeSavings:
		// Warning si vamos <50% del target a cualquier altura; exceeded nunca
		// aplica (ahorrar más que el target es bueno).
		if target <= 0 {
			return "ok"
		}
		ratio := current / target
		if ratio >= 1 {
			return "ok"
		}
		if ratio >= 0.5 {
			return "warning"
		}
		return "warning"
	default:
		if target <= 0 {
			return "ok"
		}
		ratio := current / target
		if ratio > 1 {
			return "exceeded"
		}
		if ratio >= 0.8 {
			return "warning"
		}
		return "ok"
	}
}
