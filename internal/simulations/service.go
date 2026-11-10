package simulations

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/expenses"
	"github.com/LucianoR23/valuo_api/internal/goals"
	"github.com/LucianoR23/valuo_api/internal/money"
)

// DefaultMonths and MaxMonths bound the projection, like "Comprometido".
const (
	DefaultMonths = 6
	MaxMonths     = 12
)

type planner interface {
	Plan(ctx context.Context, in expenses.CreateInput) (expenses.PlannedPurchase, error)
}

type committedReader interface {
	Committed(ctx context.Context, ownerID uuid.UUID, months int) (domain.Committed, error)
}

type recurringExpensesLister interface {
	List(ctx context.Context, householdID uuid.UUID) ([]domain.RecurringExpense, error)
}

type recurringIncomesLister interface {
	ListRecurring(ctx context.Context, householdID uuid.UUID) ([]domain.RecurringIncome, error)
}

type goalsProgress interface {
	ProgressList(ctx context.Context, householdID uuid.UUID, f goals.ListFilters, at time.Time) ([]domain.BudgetGoalProgress, error)
}

type paymentMethods interface {
	GetPaymentMethod(ctx context.Context, pmID uuid.UUID) (domain.PaymentMethod, error)
}

type converter interface {
	Convert(ctx context.Context, amount decimal.Decimal, from, to string) (converted, rate decimal.Decimal, err error)
}

type categoryParents interface {
	CategoryParent(ctx context.Context, id uuid.UUID) (*uuid.UUID, error)
}

// Deps: the services the simulator reads from.
type Deps struct {
	Planner           planner
	Committed         committedReader
	RecurringExpenses recurringExpensesLister
	RecurringIncomes  recurringIncomesLister
	Goals             goalsProgress
	PaymentMethods    paymentMethods
	FX                converter
	Categories        categoryParents
}

type Service struct {
	d   Deps
	now func() time.Time
}

func NewService(d Deps) *Service { return &Service{d: d, now: time.Now} }

// SetClock injects the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Input: the purchase to simulate. SpentAt zero means today.
type Input struct {
	HouseholdID     uuid.UUID
	UserID          uuid.UUID
	PaymentMethodID uuid.UUID
	CategoryID      *uuid.UUID
	Amount          decimal.Decimal
	Currency        string
	Installments    int
	SpentAt         time.Time
	Months          int
}

// Purchase: the simulated purchase as it would be loaded.
type Purchase struct {
	PaymentMethodID uuid.UUID
	IsCredit        bool
	Amount          decimal.Decimal
	Currency        string
	AmountBase      decimal.Decimal
	Installments    []PlannedInstallment
}

// Result: the answer to "¿Me lo puedo comprar?".
type Result struct {
	BaseCurrency string
	Purchase     Purchase
	Months       []Month
	Card         []CardStatement // nil when the purchase is not on a credit card
	Goals        []GoalImpact
	HasIncome    bool // some recurring income in the months shown
	// TodayRate: some amount was converted with today's rate.
	TodayRate bool
}

// Purchase simulates in. The cards are the user's (Mi plata); recurring
// expenses, incomes and goals are the current space's.
func (s *Service) Purchase(ctx context.Context, in Input) (Result, error) {
	if in.Months == 0 {
		in.Months = DefaultMonths
	}
	if in.Months < 1 || in.Months > MaxMonths {
		return Result{}, domain.NewValidationError("months", "debe estar entre 1 y 12")
	}
	today := dates.DateOf(s.now())
	if in.SpentAt.IsZero() {
		in.SpentAt = today
	}
	if in.SpentAt.Before(today.AddDate(0, 0, -31)) || in.SpentAt.After(today.AddDate(1, 0, 0)) {
		return Result{}, domain.NewValidationError("spentAt", "debe estar entre el último mes y el próximo año")
	}

	plan, err := s.d.Planner.Plan(ctx, expenses.CreateInput{
		HouseholdID: in.HouseholdID, CreatedBy: in.UserID, CategoryID: in.CategoryID,
		PaymentMethodID: in.PaymentMethodID, Amount: in.Amount, Currency: in.Currency,
		SpentAt: in.SpentAt, Installments: in.Installments,
	})
	if err != nil {
		return Result{}, err
	}
	base := plan.BaseCurrency
	cv := &conv{fx: s.d.FX, base: base, todayRate: plan.Currency != base}

	committed, err := s.d.Committed.Committed(ctx, in.UserID, in.Months)
	if err != nil {
		return Result{}, err
	}
	periods := make([]string, len(committed.Months))
	for i, m := range committed.Months {
		periods[i] = m.PeriodYM
	}

	isCredit := plan.PaymentMethod.Kind == domain.KindCredit
	installments := make([]PlannedInstallment, len(plan.Installments))
	for i, x := range plan.Installments {
		period := x.BillingDate.Format("2006-01")
		if !isCredit && len(periods) > 0 && period < periods[0] {
			// Cash or debit today, when every card already closed this
			// month: show it in the first month.
			period = periods[0]
		}
		installments[i] = PlannedInstallment{Number: x.InstallmentNumber, AmountBase: x.InstallmentAmountBase, PeriodYM: period, DueDate: x.DueDate}
	}

	cardsBefore, err := cv.periodTotals(ctx, committed.Months)
	if err != nil {
		return Result{}, err
	}
	var cardBefore map[string]decimal.Decimal
	if isCredit {
		cardBefore = map[string]decimal.Decimal{}
		for _, c := range committed.Cards {
			if c.Card.ID == plan.PaymentMethod.ID {
				if cardBefore, err = cv.periodTotals(ctx, c.Months); err != nil {
					return Result{}, err
				}
			}
		}
	}

	fixed, err := s.fixed(ctx, cv, in.HouseholdID, periods, today)
	if err != nil {
		return Result{}, err
	}
	income, err := s.income(ctx, cv, in.HouseholdID, periods)
	if err != nil {
		return Result{}, err
	}
	impacts, err := s.goals(ctx, in, installments)
	if err != nil {
		return Result{}, err
	}

	proj := project(projectInput{
		Periods: periods, CardsBefore: cardsBefore, CardBefore: cardBefore,
		Installments: installments, Fixed: fixed, Income: income,
	})
	hasIncome := false
	for _, m := range proj.Months {
		hasIncome = hasIncome || m.Income.Sign() > 0
	}
	return Result{
		BaseCurrency: base,
		Purchase: Purchase{
			PaymentMethodID: plan.PaymentMethod.ID, IsCredit: isCredit,
			Amount: plan.Amount, Currency: plan.Currency, AmountBase: plan.AmountBase,
			Installments: installments,
		},
		Months: proj.Months, Card: proj.Card, Goals: impacts,
		HasIncome: hasIncome, TodayRate: cv.todayRate,
	}, nil
}

// fixed: recurring expenses per period. One on a credit card counts only
// after today: what already happened is in the card's statement.
func (s *Service) fixed(ctx context.Context, cv *conv, householdID uuid.UUID, periods []string, today time.Time) (map[string]decimal.Decimal, error) {
	list, err := s.d.RecurringExpenses.List(ctx, householdID)
	if err != nil {
		return nil, err
	}
	kinds := map[uuid.UUID]domain.PaymentMethodKind{}
	out := map[string]decimal.Decimal{}
	for _, r := range list {
		if !r.IsActive {
			continue
		}
		kind, ok := kinds[r.PaymentMethodID]
		if !ok {
			pm, err := s.d.PaymentMethods.GetPaymentMethod(ctx, r.PaymentMethodID)
			if err != nil {
				return nil, err
			}
			kind, kinds[r.PaymentMethodID] = pm.Kind, pm.Kind
		}
		var notBefore *time.Time
		if kind == domain.KindCredit {
			notBefore = &today
		}
		amount := r.Amount
		if r.AmountIsVariable && r.LastAmount != nil {
			amount = *r.LastAmount
		}
		cal := recurring{r.Frequency, r.DayOfMonth, r.DayOfWeek, r.MonthOfYear, r.StartsAt, r.EndsAt}
		if err := cv.addOccurrences(ctx, out, periods, cal, amount, r.Currency, notBefore); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// income: recurring incomes per period.
func (s *Service) income(ctx context.Context, cv *conv, householdID uuid.UUID, periods []string) (map[string]decimal.Decimal, error) {
	list, err := s.d.RecurringIncomes.ListRecurring(ctx, householdID)
	if err != nil {
		return nil, err
	}
	out := map[string]decimal.Decimal{}
	for _, r := range list {
		if !r.IsActive {
			continue
		}
		cal := recurring{r.Frequency, r.DayOfMonth, r.DayOfWeek, r.MonthOfYear, r.StartsAt, r.EndsAt}
		if err := cv.addOccurrences(ctx, out, periods, cal, r.Amount, r.Currency, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// goals: the monthly limits of the space the purchase counts against: the
// space's own and the user's personal ones there.
func (s *Service) goals(ctx context.Context, in Input, installments []PlannedInstallment) ([]GoalImpact, error) {
	active := true
	progs, err := s.d.Goals.ProgressList(ctx, in.HouseholdID, goals.ListFilters{OnlyActive: &active}, s.now())
	if err != nil {
		return nil, err
	}
	var parent *uuid.UUID
	if in.CategoryID != nil {
		if parent, err = s.d.Categories.CategoryParent(ctx, *in.CategoryID); err != nil {
			return nil, err
		}
	}
	out := []GoalImpact{}
	for _, p := range progs {
		if p.Goal.Scope == domain.GoalScopeUser && (p.Goal.UserID == nil || *p.Goal.UserID != in.UserID) {
			continue
		}
		if gi, ok := goalImpact(p, in.CategoryID, parent, installments, in.SpentAt); ok {
			out = append(out, gi)
		}
	}
	return out, nil
}

// conv converts amounts to the base currency with today's rate, rounded to
// cents, and remembers whether it had to.
type conv struct {
	fx        converter
	base      string
	todayRate bool
}

func (c *conv) toBase(ctx context.Context, amount decimal.Decimal, currency string) (decimal.Decimal, error) {
	if currency == c.base || amount.IsZero() {
		return amount, nil
	}
	v, _, err := c.fx.Convert(ctx, amount, currency, c.base)
	if err != nil {
		return decimal.Zero, err
	}
	c.todayRate = true
	return v.Round(money.AmountScale), nil
}

func (c *conv) periodTotals(ctx context.Context, months []domain.PeriodTotals) (map[string]decimal.Decimal, error) {
	out := map[string]decimal.Decimal{}
	for _, m := range months {
		for _, t := range m.Totals {
			v, err := c.toBase(ctx, t.Amount, t.Currency)
			if err != nil {
				return nil, err
			}
			out[m.PeriodYM] = out[m.PeriodYM].Add(v)
		}
	}
	return out, nil
}

func (c *conv) addOccurrences(ctx context.Context, out map[string]decimal.Decimal, periods []string, cal recurring, amount decimal.Decimal, currency string, notBefore *time.Time) error {
	each, err := c.toBase(ctx, amount, currency)
	if err != nil {
		return err
	}
	for _, p := range periods {
		from, to := monthBounds(p)
		if n := occurrences(cal, from, to, notBefore); n > 0 {
			out[p] = out[p].Add(each.Mul(decimal.NewFromInt(int64(n))))
		}
	}
	return nil
}
