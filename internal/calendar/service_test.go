package calendar

// Plan 7.2 without a database: occurrences of recurring series and how a
// month is put together and ordered.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

func date(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

func days(ts []time.Time) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Format(time.DateOnly)
	}
	return out
}

func TestOccurrences(t *testing.T) {
	feb := date("2027-02-01")
	tests := []struct {
		name     string
		active   bool
		freq     string
		dom, dow *int
		moy      *int
		starts   string
		ends     *time.Time
		want     []string
	}{
		{"monthly", true, "monthly", ptr(10), nil, nil, "2026-01-01", nil, []string{"2027-02-10"}},
		{"day 31 falls on February's last day", true, "monthly", ptr(31), nil, nil, "2026-01-01", nil, []string{"2027-02-28"}},
		{"weekly on Mondays", true, "weekly", nil, ptr(1), nil, "2026-01-01", nil, []string{"2027-02-01", "2027-02-08", "2027-02-15", "2027-02-22"}},
		{"yearly in its month", true, "yearly", ptr(14), nil, ptr(2), "2026-01-01", nil, []string{"2027-02-14"}},
		{"yearly in another month", true, "yearly", ptr(14), nil, ptr(3), "2026-01-01", nil, nil},
		{"paused", false, "monthly", ptr(10), nil, nil, "2026-01-01", nil, nil},
		{"starts after the day", true, "monthly", ptr(10), nil, nil, "2027-02-11", nil, nil},
		{"starts on the day", true, "monthly", ptr(10), nil, nil, "2027-02-10", nil, []string{"2027-02-10"}},
		{"ended before the day", true, "monthly", ptr(10), nil, nil, "2026-01-01", ptr(date("2027-02-09")), nil},
		{"weekly until mid month", true, "weekly", nil, ptr(1), nil, "2026-01-01", ptr(date("2027-02-08")), []string{"2027-02-01", "2027-02-08"}},
	}
	for _, tt := range tests {
		got := days(occurrences(tt.active, tt.freq, tt.dom, tt.dow, tt.moy, date(tt.starts), tt.ends, feb))
		if len(got) != len(tt.want) || (len(got) > 0 && !equal(got, tt.want)) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func equal(a, b []string) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type fakeCards struct {
	dates []domain.CardDate
	err   error
	owner uuid.UUID
	month time.Time
}

func (f *fakeCards) CardDates(_ context.Context, owner uuid.UUID, month time.Time) ([]domain.CardDate, error) {
	f.owner, f.month = owner, month
	return f.dates, f.err
}

type fakeExpenses struct {
	list []domain.RecurringExpense
	err  error
}

func (f fakeExpenses) List(context.Context, uuid.UUID) ([]domain.RecurringExpense, error) {
	return f.list, f.err
}

type fakeIncomes struct {
	list []domain.RecurringIncome
	err  error
}

func (f fakeIncomes) ListRecurring(context.Context, uuid.UUID) ([]domain.RecurringIncome, error) {
	return f.list, f.err
}

func TestMonth(t *testing.T) {
	ctx := context.Background()
	user, hh, visa, cash := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	feb := date("2027-02-01")
	cards := &fakeCards{dates: []domain.CardDate{
		{Kind: domain.CardReminderClosing, PaymentMethodID: visa, CardName: "Visa", PeriodYM: "2027-02", Date: date("2027-02-24")},
		{
			Kind: domain.CardReminderDue, PaymentMethodID: visa, CardName: "Visa", PeriodYM: "2027-01", Date: date("2027-02-03"),
			Totals: []domain.CurrencyAmount{{Currency: "ARS", Amount: decimal.NewFromInt(1000)}}, Status: domain.StatementClosed, InstallmentCount: 1,
		},
	}}
	expenses := fakeExpenses{list: []domain.RecurringExpense{
		{ID: uuid.New(), PaymentMethodID: cash, Amount: decimal.NewFromInt(250000), Currency: "ARS", Description: "Alquiler",
			Frequency: "monthly", DayOfMonth: ptr(3), IsActive: true, StartsAt: date("2026-01-01")},
		{ID: uuid.New(), PaymentMethodID: cash, Amount: decimal.NewFromInt(30000), Currency: "ARS", Description: "luz",
			Frequency: "monthly", DayOfMonth: ptr(3), IsActive: true, StartsAt: date("2026-01-01"), AmountIsVariable: true},
		{ID: uuid.New(), PaymentMethodID: cash, Amount: decimal.NewFromInt(9000), Currency: "ARS", Description: "Pausado",
			Frequency: "monthly", DayOfMonth: ptr(5), IsActive: false, StartsAt: date("2026-01-01")},
	}}
	incomes := fakeIncomes{list: []domain.RecurringIncome{
		{ID: uuid.New(), Amount: decimal.NewFromInt(1500), Currency: "USD", Description: "Sueldo",
			Frequency: "monthly", DayOfMonth: ptr(3), IsActive: true, StartsAt: date("2026-01-01")},
	}}

	m, err := NewService(cards, expenses, incomes).Month(ctx, user, hh, feb)
	if err != nil {
		t.Fatal(err)
	}
	if cards.owner != user || !cards.month.Equal(feb) {
		t.Errorf("cards asked for %v %v", cards.owner, cards.month)
	}
	if m.Month != "2027-02" {
		t.Errorf("month = %q", m.Month)
	}
	type row struct {
		day   string
		kind  Kind
		title string
	}
	want := []row{
		// The 3rd: the salary comes in, then the card, then the recurring
		// expenses by name ignoring case. The paused one is left out.
		{"2027-02-03", KindRecurringIncome, "Sueldo"},
		{"2027-02-03", KindCardDue, "Visa"},
		{"2027-02-03", KindRecurringExpense, "Alquiler"},
		{"2027-02-03", KindRecurringExpense, "luz"},
		{"2027-02-24", KindCardClosing, "Visa"},
	}
	if len(m.Events) != len(want) {
		t.Fatalf("events = %+v", m.Events)
	}
	for i, w := range want {
		e := m.Events[i]
		if e.Date.Format(time.DateOnly) != w.day || e.Kind != w.kind || e.Title != w.title {
			t.Errorf("event %d = %s %s %s, want %+v", i, e.Date.Format(time.DateOnly), e.Kind, e.Title, w)
		}
	}
	due := m.Events[1]
	if len(due.Totals) != 1 || !due.Totals[0].Amount.Equal(decimal.NewFromInt(1000)) || due.Status != domain.StatementClosed ||
		due.InstallmentCount != 1 || *due.PaymentMethodID != visa || due.PeriodYM != "2027-01" || due.Amount != nil {
		t.Errorf("card due = %+v", due)
	}
	if luz := m.Events[3]; !luz.Variable || *luz.PaymentMethodID != cash || luz.RecurringID == nil || !luz.Amount.Equal(decimal.NewFromInt(30000)) {
		t.Errorf("variable expense = %+v", luz)
	}
	if salary := m.Events[0]; salary.Currency != "USD" || !salary.Amount.Equal(decimal.NewFromInt(1500)) || salary.PaymentMethodID != nil {
		t.Errorf("salary = %+v", salary)
	}
	if closing := m.Events[4]; closing.Totals != nil || closing.Status != "" {
		t.Errorf("closing = %+v", closing)
	}

	boom := errors.New("boom")
	for name, svc := range map[string]*Service{
		"cards":    NewService(&fakeCards{err: boom}, expenses, incomes),
		"expenses": NewService(&fakeCards{}, fakeExpenses{err: boom}, incomes),
		"incomes":  NewService(&fakeCards{}, expenses, fakeIncomes{err: boom}),
	} {
		if _, err := svc.Month(ctx, user, hh, feb); !errors.Is(err, boom) {
			t.Errorf("%s error: %v", name, err)
		}
	}

	empty, err := NewService(&fakeCards{}, fakeExpenses{}, fakeIncomes{}).Month(ctx, user, hh, feb)
	if err != nil || empty.Events == nil || len(empty.Events) != 0 {
		t.Errorf("empty month = %+v, %v (events must be [] for JSON)", empty, err)
	}
}

func TestParseMonth(t *testing.T) {
	if m, err := ParseMonth("2027-02"); err != nil || !m.Equal(date("2027-02-01")) {
		t.Errorf("2027-02 = %v, %v", m, err)
	}
	if m, err := ParseMonth(""); err != nil || m.Day() != 1 {
		t.Errorf("empty = %v, %v", m, err)
	}
	var ve *domain.ValidationError
	if _, err := ParseMonth("2027-2-1"); !errors.As(err, &ve) || ve.Field != "month" {
		t.Errorf("bad month: %v", err)
	}
}
