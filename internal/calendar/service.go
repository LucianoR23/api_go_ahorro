// Package calendar: what is due or comes in, day by day, in a month (plan
// 7.2). Read-only, no tables of its own: the user's card closing and due
// dates, plus the space's recurring expenses and incomes.
//
// Card installments are not listed one by one: they are already inside
// each statement's total, so listing them too would count them twice.
package calendar

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Kind of an event.
type Kind string

const (
	KindRecurringIncome  Kind = "recurring_income"
	KindCardDue          Kind = "card_due"
	KindRecurringExpense Kind = "recurring_expense"
	KindCardClosing      Kind = "card_closing"
)

// order: on the same day, what comes in first, then what is paid, then
// what only closes.
var order = map[Kind]int{KindRecurringIncome: 0, KindCardDue: 1, KindRecurringExpense: 2, KindCardClosing: 3}

// Event: one thing on one day.
type Event struct {
	Date  time.Time
	Kind  Kind
	Title string
	// Amount and Currency: a recurring expense or income (the estimate when
	// the amount is variable).
	Amount   *decimal.Decimal
	Currency string
	// Totals, Status and InstallmentCount: a card's due date.
	Totals           []domain.CurrencyAmount
	Status           domain.StatementStatus
	InstallmentCount int
	// PaymentMethodID: the card, or how a recurring expense is paid.
	PaymentMethodID *uuid.UUID
	PeriodYM        string
	RecurringID     *uuid.UUID
	CategoryID      *uuid.UUID
	Variable        bool
}

// Month: the events of a month, by day.
type Month struct {
	Month  string // YYYY-MM
	Events []Event
}

type cards interface {
	CardDates(ctx context.Context, ownerID uuid.UUID, month time.Time) ([]domain.CardDate, error)
}

type recurringExpenses interface {
	List(ctx context.Context, householdID uuid.UUID) ([]domain.RecurringExpense, error)
}

type recurringIncomes interface {
	ListRecurring(ctx context.Context, householdID uuid.UUID) ([]domain.RecurringIncome, error)
}

type Service struct {
	cards    cards
	expenses recurringExpenses
	incomes  recurringIncomes
}

func NewService(c cards, e recurringExpenses, i recurringIncomes) *Service {
	return &Service{cards: c, expenses: e, incomes: i}
}

// ParseMonth reads YYYY-MM; empty is the current month in the app's timezone.
func ParseMonth(s string) (time.Time, error) {
	if s == "" {
		t := dates.Today()
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC), nil
	}
	m, err := time.Parse("2006-01", s)
	if err != nil {
		return time.Time{}, domain.NewValidationError("month", "formato esperado YYYY-MM")
	}
	return m, nil
}

// Month returns the events of month (its first day) for userID in
// householdID: the user's cards, as in Tarjetas, and the space's recurring
// expenses and incomes.
func (s *Service) Month(ctx context.Context, userID, householdID uuid.UUID, month time.Time) (Month, error) {
	out := Month{Month: month.Format("2006-01"), Events: []Event{}}

	cardDates, err := s.cards.CardDates(ctx, userID, month)
	if err != nil {
		return Month{}, err
	}
	for _, c := range cardDates {
		pm := c.PaymentMethodID
		e := Event{Date: c.Date, Title: c.CardName, PaymentMethodID: &pm, PeriodYM: c.PeriodYM}
		if c.Kind == domain.CardReminderDue {
			e.Kind, e.Totals, e.Status, e.InstallmentCount = KindCardDue, c.Totals, c.Status, c.InstallmentCount
		} else {
			e.Kind = KindCardClosing
		}
		out.Events = append(out.Events, e)
	}

	expenses, err := s.expenses.List(ctx, householdID)
	if err != nil {
		return Month{}, err
	}
	for _, re := range expenses {
		r := re
		for _, day := range occurrences(r.IsActive, r.Frequency, r.DayOfMonth, r.DayOfWeek, r.MonthOfYear, r.StartsAt, r.EndsAt, month) {
			amount, pm, id := r.Amount, r.PaymentMethodID, r.ID
			out.Events = append(out.Events, Event{
				Date: day, Kind: KindRecurringExpense, Title: r.Description,
				Amount: &amount, Currency: r.Currency, PaymentMethodID: &pm,
				RecurringID: &id, CategoryID: r.CategoryID, Variable: r.AmountIsVariable,
			})
		}
	}

	incomes, err := s.incomes.ListRecurring(ctx, householdID)
	if err != nil {
		return Month{}, err
	}
	for _, ri := range incomes {
		r := ri
		for _, day := range occurrences(r.IsActive, r.Frequency, r.DayOfMonth, r.DayOfWeek, r.MonthOfYear, r.StartsAt, r.EndsAt, month) {
			amount, id := r.Amount, r.ID
			out.Events = append(out.Events, Event{
				Date: day, Kind: KindRecurringIncome, Title: r.Description,
				Amount: &amount, Currency: r.Currency, PaymentMethodID: r.PaymentMethodID, RecurringID: &id,
			})
		}
	}

	sort.SliceStable(out.Events, func(i, j int) bool {
		a, b := out.Events[i], out.Events[j]
		if !a.Date.Equal(b.Date) {
			return a.Date.Before(b.Date)
		}
		if order[a.Kind] != order[b.Kind] {
			return order[a.Kind] < order[b.Kind]
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
	return out, nil
}

// day: a DATE column (midnight UTC) as a calendar day. Not dates.DateOf,
// which would move it to the day before in the app's timezone.
func day(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// occurrences: the days of month a recurring series falls on, inside its
// start and end. A paused series has none.
func occurrences(active bool, frequency string, dayOfMonth, dayOfWeek, monthOfYear *int, starts time.Time, ends *time.Time, month time.Time) []time.Time {
	if !active {
		return nil
	}
	start := day(starts)
	var days []time.Time
	for d := month; d.Month() == month.Month(); d = d.AddDate(0, 0, 1) {
		if d.Before(start) || (ends != nil && d.After(day(*ends))) {
			continue
		}
		if domain.RecurrenceMatches(frequency, dayOfMonth, dayOfWeek, monthOfYear, d) {
			days = append(days, d)
		}
	}
	return days
}
