package recurringexpenses

// Characterization tests (plan 0.11). incomes and recurringexpenses carry
// identical copies of recurrenceMatches/clampDay; both test files share this
// table so 0.15b can merge them safely.

import (
	"testing"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

func ptr(v int) *int { return &v }

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestClampDay(t *testing.T) {
	tests := []struct{ d, y, m, want int }{
		{15, 2026, 3, 15},
		{31, 2026, 2, 28},
		{31, 2028, 2, 29},
		{31, 2026, 4, 30},
		{30, 2026, 2, 28},
		{31, 2026, 12, 31},
	}
	for _, tt := range tests {
		if got := clampDay(tt.d, tt.y, tt.m); got != tt.want {
			t.Errorf("clampDay(%d, %d, %d) = %d, want %d", tt.d, tt.y, tt.m, got, tt.want)
		}
	}
}

func TestRecurrenceMatches(t *testing.T) {
	tests := []struct {
		name string
		tpl  domain.RecurringExpense
		date time.Time
		want bool
	}{
		{"monthly on its day", domain.RecurringExpense{Frequency: "monthly", DayOfMonth: ptr(10)}, day(2026, 3, 10), true},
		{"monthly on another day", domain.RecurringExpense{Frequency: "monthly", DayOfMonth: ptr(10)}, day(2026, 3, 11), false},
		{"monthly 31 falls on the last day of a short month", domain.RecurringExpense{Frequency: "monthly", DayOfMonth: ptr(31)}, day(2026, 2, 28), true},
		{"monthly 31 in April on the 30th", domain.RecurringExpense{Frequency: "monthly", DayOfMonth: ptr(31)}, day(2026, 4, 30), true},
		{"monthly 31 does not match the 30th of a 31-day month", domain.RecurringExpense{Frequency: "monthly", DayOfMonth: ptr(31)}, day(2026, 3, 30), false},
		{"monthly without day never matches", domain.RecurringExpense{Frequency: "monthly"}, day(2026, 3, 10), false},
		{"weekly on its weekday", domain.RecurringExpense{Frequency: "weekly", DayOfWeek: ptr(int(time.Wednesday))}, day(2026, 11, 4), true},
		{"weekly on another weekday", domain.RecurringExpense{Frequency: "weekly", DayOfWeek: ptr(int(time.Monday))}, day(2026, 11, 4), false},
		{"weekly Sunday is 0", domain.RecurringExpense{Frequency: "weekly", DayOfWeek: ptr(0)}, day(2026, 11, 8), true},
		{"weekly without weekday never matches", domain.RecurringExpense{Frequency: "weekly"}, day(2026, 11, 4), false},
		{"yearly on its date", domain.RecurringExpense{Frequency: "yearly", DayOfMonth: ptr(15), MonthOfYear: ptr(6)}, day(2026, 6, 15), true},
		{"yearly same day, other month", domain.RecurringExpense{Frequency: "yearly", DayOfMonth: ptr(15), MonthOfYear: ptr(6)}, day(2026, 7, 15), false},
		{"yearly Feb 29 falls on Feb 28 in non-leap years", domain.RecurringExpense{Frequency: "yearly", DayOfMonth: ptr(29), MonthOfYear: ptr(2)}, day(2027, 2, 28), true},
		{"yearly Feb 29 in a leap year", domain.RecurringExpense{Frequency: "yearly", DayOfMonth: ptr(29), MonthOfYear: ptr(2)}, day(2028, 2, 29), true},
		{"yearly Feb 29 does not also fire on the 28th of a leap year", domain.RecurringExpense{Frequency: "yearly", DayOfMonth: ptr(29), MonthOfYear: ptr(2)}, day(2028, 2, 28), false},
		{"yearly without month never matches", domain.RecurringExpense{Frequency: "yearly", DayOfMonth: ptr(15)}, day(2026, 6, 15), false},
		{"unknown frequency never matches", domain.RecurringExpense{Frequency: "daily", DayOfMonth: ptr(10)}, day(2026, 3, 10), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := recurrenceMatches(tt.tpl, tt.date); got != tt.want {
				t.Errorf("recurrenceMatches = %v, want %v", got, tt.want)
			}
		})
	}
}
