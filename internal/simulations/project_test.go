package simulations

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func date(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

func TestProject(t *testing.T) {
	due := func(s string) *time.Time { return ptr(date(s)) }
	got := project(projectInput{
		Periods:     []string{"2026-11", "2026-12", "2027-01"},
		CardsBefore: map[string]decimal.Decimal{"2026-11": d("1000"), "2026-12": d("500")},
		CardBefore:  map[string]decimal.Decimal{"2026-11": d("400")},
		Installments: []PlannedInstallment{
			{Number: 1, AmountBase: d("3333.33"), PeriodYM: "2026-12", DueDate: due("2027-01-03")},
			{Number: 2, AmountBase: d("3333.33"), PeriodYM: "2027-01", DueDate: due("2027-02-03")},
			{Number: 3, AmountBase: d("3333.34"), PeriodYM: "2027-02", DueDate: due("2027-03-03")}, // outside the months shown
		},
		Fixed:  map[string]decimal.Decimal{"2026-11": d("100"), "2026-12": d("100"), "2027-01": d("100")},
		Income: map[string]decimal.Decimal{"2026-11": d("10000"), "2026-12": d("10000")},
	})
	want := []struct {
		period                        string
		before, purchase, after, left string
		pct                           *float64
	}{
		{"2026-11", "1000", "0", "1000", "8900", ptr(10.0)},
		{"2026-12", "500", "3333.33", "3833.33", "6066.67", ptr(38.3)},
		{"2027-01", "0", "3333.33", "3333.33", "-3433.33", nil}, // no income: no percentage
	}
	if len(got.Months) != len(want) {
		t.Fatalf("months = %d", len(got.Months))
	}
	for i, w := range want {
		m := got.Months[i]
		if m.PeriodYM != w.period || !m.CardsBefore.Equal(d(w.before)) || !m.Purchase.Equal(d(w.purchase)) ||
			!m.CardsAfter.Equal(d(w.after)) || !m.Left.Equal(d(w.left)) {
			t.Errorf("month %s = %+v", w.period, m)
		}
		if (m.PctOfIncome == nil) != (w.pct == nil) || (w.pct != nil && *m.PctOfIncome != *w.pct) {
			t.Errorf("month %s pct = %v, want %v", w.period, m.PctOfIncome, w.pct)
		}
	}
	if len(got.Card) != 3 || !got.Card[0].After.Equal(d("400")) || !got.Card[1].After.Equal(d("3333.33")) {
		t.Errorf("card = %+v", got.Card)
	}

	if cash := project(projectInput{Periods: []string{"2026-11"}}); cash.Card != nil {
		t.Errorf("not a card: card = %+v", cash.Card)
	}
}

func TestGoalImpact(t *testing.T) {
	cat, parent, other := uuid.New(), uuid.New(), uuid.New()
	nov := func(goalType string, category *uuid.UUID, current, target string) domain.BudgetGoalProgress {
		return domain.BudgetGoalProgress{
			Goal:          domain.BudgetGoal{ID: uuid.New(), GoalType: goalType, CategoryID: category, Period: domain.GoalPeriodMonthly, Scope: domain.GoalScopeHousehold},
			PeriodStart:   date("2026-11-01"),
			PeriodEnd:     date("2026-11-30"),
			CurrentAmount: d(current), TargetAmountBase: d(target),
		}
	}
	card := []PlannedInstallment{
		{AmountBase: d("300"), DueDate: ptr(date("2026-11-10"))},
		{AmountBase: d("300"), DueDate: ptr(date("2026-12-10"))},
	}
	cash := []PlannedInstallment{{AmountBase: d("600")}}
	purchaseDay := date("2026-11-09")

	t.Run("total limit counts the installment due this period", func(t *testing.T) {
		gi, ok := goalImpact(nov(domain.GoalTypeTotalLimit, nil, "800", "1000"), nil, nil, card, purchaseDay)
		if !ok || !gi.ThisPeriod.Equal(d("300")) || !gi.After.Equal(d("1100")) || !gi.Exceeds || gi.PctAfter != 110 ||
			!gi.PerInstallment.Equal(d("300")) || gi.PctPerInstallment != 30 {
			t.Errorf("impact = %+v ok=%v", gi, ok)
		}
	})
	t.Run("cash counts on the purchase day", func(t *testing.T) {
		gi, ok := goalImpact(nov(domain.GoalTypeTotalLimit, nil, "0", "1000"), nil, nil, cash, purchaseDay)
		if !ok || !gi.ThisPeriod.Equal(d("600")) || gi.Exceeds {
			t.Errorf("impact = %+v", gi)
		}
	})
	t.Run("category limit: its own category or the parent", func(t *testing.T) {
		if _, ok := goalImpact(nov(domain.GoalTypeCategoryLimit, &cat, "0", "1000"), &cat, nil, card, purchaseDay); !ok {
			t.Error("same category not counted")
		}
		if _, ok := goalImpact(nov(domain.GoalTypeCategoryLimit, &parent, "0", "1000"), &cat, &parent, card, purchaseDay); !ok {
			t.Error("parent category not counted")
		}
		if _, ok := goalImpact(nov(domain.GoalTypeCategoryLimit, &other, "0", "1000"), &cat, &parent, card, purchaseDay); ok {
			t.Error("another category counted")
		}
		if _, ok := goalImpact(nov(domain.GoalTypeCategoryLimit, &cat, "0", "1000"), nil, nil, card, purchaseDay); ok {
			t.Error("a purchase without category counted in a category limit")
		}
	})
	t.Run("savings, weekly and zero targets are left out", func(t *testing.T) {
		weekly := nov(domain.GoalTypeTotalLimit, nil, "0", "1000")
		weekly.Goal.Period = domain.GoalPeriodWeekly
		for name, p := range map[string]domain.BudgetGoalProgress{
			"savings": nov(domain.GoalTypeSavings, nil, "0", "1000"),
			"weekly":  weekly,
			"zero":    nov(domain.GoalTypeTotalLimit, nil, "0", "0"),
		} {
			if _, ok := goalImpact(p, nil, nil, card, purchaseDay); ok {
				t.Errorf("%s counted", name)
			}
		}
	})
}

func TestOccurrences(t *testing.T) {
	from, to := monthBounds("2026-11")
	monthly := recurring{Frequency: "monthly", DayOfMonth: ptr(31), StartsAt: date("2026-01-01")}
	weekly := recurring{Frequency: "weekly", DayOfWeek: ptr(1), StartsAt: date("2026-01-01")} // Mondays
	yearly := recurring{Frequency: "yearly", DayOfMonth: ptr(15), MonthOfYear: ptr(11), StartsAt: date("2026-01-01")}

	for _, tc := range []struct {
		name      string
		r         recurring
		notBefore *time.Time
		want      int
	}{
		{"monthly on the 31st falls on the 30th in November", monthly, nil, 1},
		{"weekly: five Mondays in November 2026", weekly, nil, 5},
		{"yearly in its month", yearly, nil, 1},
		{"starts after the month", recurring{Frequency: "monthly", DayOfMonth: ptr(5), StartsAt: date("2026-12-01")}, nil, 0},
		{"ended before the day", recurring{Frequency: "monthly", DayOfMonth: ptr(20), StartsAt: date("2026-01-01"), EndsAt: ptr(date("2026-11-19"))}, nil, 0},
		{"only after notBefore", weekly, ptr(date("2026-11-16")), 2}, // the 23rd and the 30th
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := occurrences(tc.r, from, to, tc.notBefore); got != tc.want {
				t.Errorf("occurrences = %d, want %d", got, tc.want)
			}
		})
	}
	if _, end := monthBounds("2027-02"); end != date("2027-02-28") {
		t.Errorf("February ends %s", end)
	}
}
