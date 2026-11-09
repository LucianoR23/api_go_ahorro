package domain_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestCardPeriodDates(t *testing.T) {
	visa := domain.CreditCard{DefaultClosingDay: 24, DefaultDueDay: 3}
	tests := []struct {
		name         string
		cc           domain.CreditCard
		month        time.Time
		override     *domain.CreditCardPeriod
		closing, due time.Time
	}{
		{"due next month when its day is not after closing", visa, day(2026, 11, 1), nil, day(2026, 11, 24), day(2026, 12, 3)},
		{"due same month when its day is after closing", domain.CreditCard{DefaultClosingDay: 5, DefaultDueDay: 20}, day(2026, 11, 1), nil, day(2026, 11, 5), day(2026, 11, 20)},
		{"December rolls the due date into January", visa, day(2026, 12, 1), nil, day(2026, 12, 24), day(2027, 1, 3)},
		{"closing 31 clamps to February's last day", domain.CreditCard{DefaultClosingDay: 31, DefaultDueDay: 10}, day(2026, 2, 1), nil, day(2026, 2, 28), day(2026, 3, 10)},
		{"leap year", domain.CreditCard{DefaultClosingDay: 31, DefaultDueDay: 31}, day(2028, 2, 1), nil, day(2028, 2, 29), day(2028, 3, 31)},
		{"due 31 clamps in a 30-day month", domain.CreditCard{DefaultClosingDay: 31, DefaultDueDay: 31}, day(2026, 3, 1), nil, day(2026, 3, 31), day(2026, 4, 30)},
		{"a loaded period wins", visa, day(2026, 11, 1),
			&domain.CreditCardPeriod{ClosingDate: day(2026, 11, 26), DueDate: day(2026, 12, 7)}, day(2026, 11, 26), day(2026, 12, 7)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			closing, due := domain.CardPeriodDates(tt.cc, tt.month, tt.override)
			if !closing.Equal(tt.closing) || !due.Equal(tt.due) {
				t.Errorf("got %s / %s, want %s / %s", closing.Format(time.DateOnly), due.Format(time.DateOnly),
					tt.closing.Format(time.DateOnly), tt.due.Format(time.DateOnly))
			}
		})
	}
}

func TestStatementStatusOn(t *testing.T) {
	closing, due := day(2026, 11, 24), day(2026, 12, 3)
	paid := day(2026, 12, 1)
	tests := []struct {
		today  time.Time
		paidAt *time.Time
		want   domain.StatementStatus
	}{
		{day(2026, 11, 10), nil, domain.StatementOpen},
		{day(2026, 11, 24), nil, domain.StatementOpen}, // the closing day still counts
		{day(2026, 11, 25), nil, domain.StatementClosed},
		{day(2026, 12, 3), nil, domain.StatementClosed}, // due today: not overdue yet
		{day(2026, 12, 4), nil, domain.StatementOverdue},
		{day(2026, 12, 4), &paid, domain.StatementPaid},
	}
	for _, tt := range tests {
		if got := domain.StatementStatusOn(closing, due, tt.paidAt, tt.today); got != tt.want {
			t.Errorf("%s: got %s, want %s", tt.today.Format(time.DateOnly), got, tt.want)
		}
	}
}

func TestOpenPeriodMonth(t *testing.T) {
	closingOn24 := func(m time.Time) time.Time { return day(m.Year(), m.Month(), 24) }
	tests := []struct{ today, want time.Time }{
		{day(2026, 11, 8), day(2026, 11, 1)},
		{day(2026, 11, 24), day(2026, 11, 1)},
		{day(2026, 11, 25), day(2026, 12, 1)},
		{day(2026, 12, 31), day(2027, 1, 1)},
	}
	for _, tt := range tests {
		if got := domain.OpenPeriodMonth(tt.today, closingOn24); !got.Equal(tt.want) {
			t.Errorf("%s: got %s, want %s", tt.today.Format(time.DateOnly), got.Format(time.DateOnly), tt.want.Format(time.DateOnly))
		}
	}
}

func TestAdjustmentKind(t *testing.T) {
	ten := decimal.NewFromInt(10)
	if got := domain.AdjustmentCredit.Signed(ten); !got.Equal(ten.Neg()) {
		t.Errorf("credit = %s, want -10", got)
	}
	for _, k := range []domain.AdjustmentKind{domain.AdjustmentInterest, domain.AdjustmentTax, domain.AdjustmentFee, domain.AdjustmentOther} {
		if !k.Valid() || !k.Signed(ten).Equal(ten) {
			t.Errorf("%s should be valid and add", k)
		}
	}
	if domain.AdjustmentKind("bonus").Valid() {
		t.Error("unknown kind accepted")
	}
}

func TestParsePeriodYM(t *testing.T) {
	if got, err := domain.ParsePeriodYM("2026-11"); err != nil || !got.Equal(day(2026, 11, 1)) {
		t.Errorf("got %s, %v", got, err)
	}
	for _, bad := range []string{"2026-13", "26-11", "2026-1", ""} {
		if _, err := domain.ParsePeriodYM(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
