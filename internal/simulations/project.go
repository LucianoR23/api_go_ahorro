// Package simulations answers "¿Me lo puedo comprar?" (plan 4.8): what a
// purchase would do to the next months before buying it. Nothing is saved.
package simulations

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Every amount here is in the household's base currency. Other currencies
// are converted with today's rate before projecting.

// PlannedInstallment: one installment of the simulated purchase.
type PlannedInstallment struct {
	Number     int
	AmountBase decimal.Decimal
	PeriodYM   string     // statement (closing month) it is billed in
	DueDate    *time.Time // nil for cash, debit and transfers
}

// Month: one month of the projection, keyed like "Comprometido" by the
// statement period (closing month).
type Month struct {
	PeriodYM    string
	CardsBefore decimal.Decimal // already billed on the user's cards
	Purchase    decimal.Decimal // this purchase's installment
	CardsAfter  decimal.Decimal
	Fixed       decimal.Decimal // recurring expenses not yet billed on a card
	Income      decimal.Decimal // recurring incomes
	Left        decimal.Decimal // Income - CardsAfter - Fixed
	// PctOfIncome: CardsAfter as a percentage of Income; nil without income.
	PctOfIncome *float64
}

// CardStatement: the chosen card's statement before and after the purchase.
type CardStatement struct {
	PeriodYM      string
	Before, After decimal.Decimal
}

// GoalImpact: a spending limit the purchase counts against.
type GoalImpact struct {
	GoalID     uuid.UUID
	GoalType   string
	Scope      string
	CategoryID *uuid.UUID
	Target     decimal.Decimal
	// Current: spent in the goal's current period. ThisPeriod: what the
	// purchase adds to it (installments due in the period).
	Current, ThisPeriod, After decimal.Decimal
	PctAfter                   float64
	Exceeds                    bool
	// PerInstallment: the largest installment, and its share of the target:
	// how much of the limit the purchase takes in each month it is billed.
	PerInstallment    decimal.Decimal
	PctPerInstallment float64
}

// projectInput: everything already fetched and converted.
type projectInput struct {
	Periods      []string                   // months to show, in order
	CardsBefore  map[string]decimal.Decimal // all the user's cards, by period
	CardBefore   map[string]decimal.Decimal // the chosen card, by period (nil: not a card)
	Installments []PlannedInstallment
	Fixed        map[string]decimal.Decimal
	Income       map[string]decimal.Decimal
}

type projection struct {
	Months []Month
	Card   []CardStatement // nil when the purchase is not on a credit card
}

func project(in projectInput) projection {
	purchase := map[string]decimal.Decimal{}
	for _, inst := range in.Installments {
		purchase[inst.PeriodYM] = purchase[inst.PeriodYM].Add(inst.AmountBase)
	}
	out := projection{Months: make([]Month, len(in.Periods))}
	hundred := decimal.NewFromInt(100)
	for i, p := range in.Periods {
		m := Month{
			PeriodYM:    p,
			CardsBefore: in.CardsBefore[p],
			Purchase:    purchase[p],
			Fixed:       in.Fixed[p],
			Income:      in.Income[p],
		}
		m.CardsAfter = m.CardsBefore.Add(m.Purchase)
		m.Left = m.Income.Sub(m.CardsAfter).Sub(m.Fixed)
		if m.Income.Sign() > 0 {
			pct := m.CardsAfter.Div(m.Income).Mul(hundred).Round(1).InexactFloat64()
			m.PctOfIncome = &pct
		}
		out.Months[i] = m
	}
	if in.CardBefore != nil {
		out.Card = make([]CardStatement, len(in.Periods))
		for i, p := range in.Periods {
			out.Card[i] = CardStatement{PeriodYM: p, Before: in.CardBefore[p], After: in.CardBefore[p].Add(purchase[p])}
		}
	}
	return out
}

// goalImpact: what the purchase does to one limit. ok is false for goals it
// does not count against (savings, weekly, another category).
func goalImpact(p domain.BudgetGoalProgress, categoryID, parentID *uuid.UUID, installments []PlannedInstallment, purchaseDay time.Time) (GoalImpact, bool) {
	g := p.Goal
	if g.Period != domain.GoalPeriodMonthly || p.TargetAmountBase.Sign() <= 0 {
		return GoalImpact{}, false
	}
	switch g.GoalType {
	case domain.GoalTypeTotalLimit:
	case domain.GoalTypeCategoryLimit:
		if g.CategoryID == nil || categoryID == nil {
			return GoalImpact{}, false
		}
		if *g.CategoryID != *categoryID && (parentID == nil || *g.CategoryID != *parentID) {
			return GoalImpact{}, false
		}
	default:
		return GoalImpact{}, false
	}
	out := GoalImpact{
		GoalID: g.ID, GoalType: g.GoalType, Scope: g.Scope, CategoryID: g.CategoryID,
		Target: p.TargetAmountBase, Current: p.CurrentAmount,
	}
	for _, inst := range installments {
		// Limits count an installment on its due date, or on the purchase day
		// when it has none (goals.SumInstallmentsForHouseholdGoal).
		day := purchaseDay
		if inst.DueDate != nil {
			day = *inst.DueDate
		}
		if !day.Before(p.PeriodStart) && !day.After(p.PeriodEnd) {
			out.ThisPeriod = out.ThisPeriod.Add(inst.AmountBase)
		}
		if inst.AmountBase.GreaterThan(out.PerInstallment) {
			out.PerInstallment = inst.AmountBase
		}
	}
	hundred := decimal.NewFromInt(100)
	out.After = out.Current.Add(out.ThisPeriod)
	out.PctAfter = out.After.Div(out.Target).Mul(hundred).Round(1).InexactFloat64()
	out.Exceeds = out.After.GreaterThan(out.Target)
	out.PctPerInstallment = out.PerInstallment.Div(out.Target).Mul(hundred).Round(1).InexactFloat64()
	return out, true
}

// occurrences: how many times a recurring series falls inside [from, to]
// (calendar days), within its own start and end, counting only days after
// notBefore when it is set.
func occurrences(r recurring, from, to time.Time, notBefore *time.Time) int {
	n := 0
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		if d.Before(dayOf(r.StartsAt)) || (r.EndsAt != nil && d.After(dayOf(*r.EndsAt))) {
			continue
		}
		if notBefore != nil && !d.After(*notBefore) {
			continue
		}
		if domain.RecurrenceMatches(r.Frequency, r.DayOfMonth, r.DayOfWeek, r.MonthOfYear, d) {
			n++
		}
	}
	return n
}

// recurring: the calendar of a recurring expense or income.
type recurring struct {
	Frequency                          string
	DayOfMonth, DayOfWeek, MonthOfYear *int
	StartsAt                           time.Time
	EndsAt                             *time.Time
}

func dayOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// monthBounds: the first and last day of the period YYYY-MM.
func monthBounds(periodYM string) (time.Time, time.Time) {
	start, _ := time.Parse("2006-01", periodYM)
	return start, start.AddDate(0, 1, -1)
}
