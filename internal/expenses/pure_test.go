package expenses

// Characterization tests (plan 0.11) for the pure money/date logic of expenses.
// They pin today's behavior; known bugs are KnownBug tests flipped by their item.

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil"
)

var (
	userA  = uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	userB  = uuid.MustParse("00000000-0000-0000-0000-00000000000b")
	cardID = uuid.MustParse("00000000-0000-0000-0000-0000000000cc")
	pmID   = uuid.MustParse("00000000-0000-0000-0000-0000000000dd")
)

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// fakePeriods returns overrides keyed by "YYYY-MM"; anything else is ErrNotFound.
type fakePeriods struct {
	overrides map[string]domain.CreditCardPeriod
	err       error
}

func (f fakePeriods) Get(_ context.Context, _ uuid.UUID, ym string) (domain.CreditCardPeriod, error) {
	if f.err != nil {
		return domain.CreditCardPeriod{}, f.err
	}
	if p, ok := f.overrides[ym]; ok {
		return p, nil
	}
	return domain.CreditCardPeriod{}, domain.ErrNotFound
}

type fakePaymentMethods struct{ card domain.CreditCard }

func (f fakePaymentMethods) GetPaymentMethod(context.Context, uuid.UUID) (domain.PaymentMethod, error) {
	return domain.PaymentMethod{}, errors.New("not used")
}

func (f fakePaymentMethods) CreditCardByPaymentMethod(context.Context, uuid.UUID) (domain.CreditCard, error) {
	return f.card, nil
}

func card(closing, due int) domain.CreditCard {
	return domain.CreditCard{ID: cardID, PaymentMethodID: pmID, DefaultClosingDay: closing, DefaultDueDay: due}
}

func TestClampDay(t *testing.T) {
	tests := []struct {
		y, m, d int
		want    time.Time
	}{
		{2026, 3, 15, date(2026, 3, 15)},
		{2026, 2, 31, date(2026, 2, 28)},
		{2028, 2, 31, date(2028, 2, 29)}, // leap year
		{2026, 4, 31, date(2026, 4, 30)},
		{2026, 12, 31, date(2026, 12, 31)},
	}
	for _, tt := range tests {
		if got := clampDay(tt.y, tt.m, tt.d); !got.Equal(tt.want) {
			t.Errorf("clampDay(%d,%d,%d) = %s, want %s", tt.y, tt.m, tt.d, got, tt.want)
		}
	}
}

func TestResolveForClosingMonth(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		card        domain.CreditCard
		periods     fakePeriods
		month       time.Time
		wantBilling time.Time
		wantDue     time.Time
	}{
		{
			name: "due before closing day falls in the next month",
			card: card(25, 5), month: date(2026, 3, 1),
			wantBilling: date(2026, 3, 25), wantDue: date(2026, 4, 5),
		},
		{
			name: "due after closing day stays in the same month",
			card: card(10, 20), month: date(2026, 3, 1),
			wantBilling: date(2026, 3, 10), wantDue: date(2026, 3, 20),
		},
		{
			name: "due equal to closing day goes to the next month",
			card: card(15, 15), month: date(2026, 3, 1),
			wantBilling: date(2026, 3, 15), wantDue: date(2026, 4, 15),
		},
		{
			name: "day 31 is clamped in short months",
			card: card(31, 31), month: date(2026, 2, 10),
			wantBilling: date(2026, 2, 28), wantDue: date(2026, 3, 31),
		},
		{
			name: "December rolls the due date into January",
			card: card(25, 5), month: date(2026, 12, 1),
			wantBilling: date(2026, 12, 25), wantDue: date(2027, 1, 5),
		},
		{
			name: "override for that month wins over defaults",
			card: card(25, 5), month: date(2026, 3, 1),
			periods: fakePeriods{overrides: map[string]domain.CreditCardPeriod{
				"2026-03": {ClosingDate: date(2026, 3, 27), DueDate: date(2026, 4, 8)},
			}},
			wantBilling: date(2026, 3, 27), wantDue: date(2026, 4, 8),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveForClosingMonth(ctx, tt.periods, tt.card, tt.month)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.BillingDate.Equal(tt.wantBilling) || !got.DueDate.Equal(tt.wantDue) {
				t.Errorf("got billing %s due %s, want %s / %s",
					got.BillingDate.Format(time.DateOnly), got.DueDate.Format(time.DateOnly),
					tt.wantBilling.Format(time.DateOnly), tt.wantDue.Format(time.DateOnly))
			}
		})
	}

	t.Run("reader errors other than not found are returned", func(t *testing.T) {
		boom := errors.New("db down")
		_, err := resolveForClosingMonth(ctx, fakePeriods{err: boom}, card(25, 5), date(2026, 3, 1))
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want %v", err, boom)
		}
	})
}

func sumShares(s []domain.InstallmentShare) float64 {
	total := 0.0
	for _, x := range s {
		total += x.AmountBaseOwed
	}
	return total
}

func TestBuildShares(t *testing.T) {
	tests := []struct {
		name    string
		base    float64
		weights []weightedUser
		want    []float64
	}{
		{name: "no weights means personal expense", base: 100, weights: nil, want: nil},
		{name: "equal split", base: 100, weights: []weightedUser{{userA, 1}, {userB, 1}}, want: []float64{50, 50}},
		{name: "weighted split", base: 100, weights: []weightedUser{{userA, 0.6}, {userB, 0.4}}, want: []float64{60, 40}},
		{
			name: "last member absorbs the rounding remainder",
			base: 100, weights: []weightedUser{{userA, 1}, {userB, 1}, {uuid.New(), 1}},
			want: []float64{33.33, 33.33, 33.34},
		},
		{
			name: "all-zero weights fall back to an equal split",
			base: 10, weights: []weightedUser{{userA, 0}, {userB, 0}, {uuid.New(), 0}},
			want: []float64{3.33, 3.33, 3.34},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildShares(tt.base, tt.weights)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d shares, want %d", len(got), len(tt.want))
			}
			for i, w := range tt.want {
				if got[i].AmountBaseOwed != w {
					t.Errorf("share %d = %v, want %v", i, got[i].AmountBaseOwed, w)
				}
				if got[i].UserID != tt.weights[i].UserID {
					t.Errorf("share %d user = %s, want %s (order must be kept)", i, got[i].UserID, tt.weights[i].UserID)
				}
			}
			if tt.want != nil && math.Abs(sumShares(got)-tt.base) > 1e-9 {
				t.Errorf("shares sum %v, want %v", sumShares(got), tt.base)
			}
		})
	}
}

func TestBuildSharesNegativeRemainder(t *testing.T) {
	testutil.KnownBug(t, "0.7", "rounding each share up can leave the last member with a negative amount", func(t testing.TB) {
		weights := make([]weightedUser, 6)
		for i := range weights {
			weights[i] = weightedUser{UserID: uuid.New(), Weight: 1}
		}
		// 0.04 / 6 = 0.0067 → each rounds to 0.01, so the last one gets -0.01.
		for i, s := range buildShares(0.04, weights) {
			if s.AmountBaseOwed < 0 {
				t.Errorf("share %d is negative: %v", i, s.AmountBaseOwed)
			}
		}
	})
}

func newCreditService(c domain.CreditCard, periods fakePeriods) *Service {
	return &Service{paymMethods: fakePaymentMethods{card: c}, periodsReader: periods}
}

func TestBuildInstallmentsNonCredit(t *testing.T) {
	s := &Service{}
	in := CreateInput{Amount: 1500, Installments: 1, SpentAt: date(2026, 3, 20)}

	got, err := s.buildInstallments(context.Background(), in, domain.PaymentMethod{Kind: domain.KindDebit}, 1500, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d installments, want 1", len(got))
	}
	inst := got[0].Installment
	if inst.InstallmentNumber != 1 || inst.InstallmentAmount != 1500 || inst.InstallmentAmountBase != 1500 {
		t.Errorf("installment = %+v", inst)
	}
	if !inst.BillingDate.Equal(in.SpentAt) || inst.DueDate != nil {
		t.Errorf("billing %s due %v, want spentAt and no due date", inst.BillingDate, inst.DueDate)
	}
	if !inst.IsPaid || inst.PaidAt == nil {
		t.Errorf("non-credit installment must be paid at creation: %+v", inst)
	}
	if got[0].Shares != nil {
		t.Errorf("personal expense must have no shares, got %v", got[0].Shares)
	}
}

func TestBuildInstallmentsCredit(t *testing.T) {
	credit := domain.PaymentMethod{ID: pmID, Kind: domain.KindCredit}
	type inst struct {
		amount           float64
		billing, dueDate time.Time
	}
	tests := []struct {
		name    string
		spentAt time.Time
		amount  float64
		n       int
		periods fakePeriods
		want    []inst
	}{
		{
			name:    "before closing: first installment in this month's statement",
			spentAt: date(2026, 3, 20), amount: 100, n: 3,
			want: []inst{
				{33.33, date(2026, 3, 25), date(2026, 4, 5)},
				{33.33, date(2026, 4, 25), date(2026, 5, 5)},
				{33.34, date(2026, 5, 25), date(2026, 6, 5)},
			},
		},
		{
			name:    "on the closing day: still this month's statement",
			spentAt: date(2026, 3, 25), amount: 100, n: 1,
			want: []inst{{100, date(2026, 3, 25), date(2026, 4, 5)}},
		},
		{
			name:    "after closing: first installment in next month's statement",
			spentAt: date(2026, 3, 26), amount: 90, n: 2,
			want: []inst{
				{45, date(2026, 4, 25), date(2026, 5, 5)},
				{45, date(2026, 5, 25), date(2026, 6, 5)},
			},
		},
		{
			name:    "override moves this month's closing later, so the expense stays in it",
			spentAt: date(2026, 3, 26), amount: 50, n: 1,
			periods: fakePeriods{overrides: map[string]domain.CreditCardPeriod{
				"2026-03": {ClosingDate: date(2026, 3, 28), DueDate: date(2026, 4, 9)},
			}},
			want: []inst{{50, date(2026, 3, 28), date(2026, 4, 9)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCreditService(card(25, 5), tt.periods)
			in := CreateInput{Amount: tt.amount, Installments: tt.n, SpentAt: tt.spentAt}

			got, err := s.buildInstallments(context.Background(), in, credit, tt.amount, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d installments, want %d", len(got), len(tt.want))
			}
			for i, w := range tt.want {
				g := got[i].Installment
				if g.InstallmentNumber != i+1 || g.InstallmentAmount != w.amount || g.InstallmentAmountBase != w.amount {
					t.Errorf("installment %d: number %d amount %v base %v, want %v", i, g.InstallmentNumber, g.InstallmentAmount, g.InstallmentAmountBase, w.amount)
				}
				if !g.BillingDate.Equal(w.billing) || g.DueDate == nil || !g.DueDate.Equal(w.dueDate) {
					t.Errorf("installment %d: billing %s due %v, want %s / %s", i, g.BillingDate.Format(time.DateOnly), g.DueDate, w.billing.Format(time.DateOnly), w.dueDate.Format(time.DateOnly))
				}
				if g.IsPaid {
					t.Errorf("installment %d: credit installments start unpaid", i)
				}
			}
		})
	}

	t.Run("shared expense splits every installment", func(t *testing.T) {
		s := newCreditService(card(25, 5), fakePeriods{})
		in := CreateInput{Amount: 100, Installments: 2, SpentAt: date(2026, 3, 20)}
		got, err := s.buildInstallments(context.Background(), in, credit, 100, []weightedUser{{userA, 1}, {userB, 1}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for i, g := range got {
			if len(g.Shares) != 2 || g.Shares[0].AmountBaseOwed != 25 || g.Shares[1].AmountBaseOwed != 25 {
				t.Errorf("installment %d shares = %+v, want 25/25", i, g.Shares)
			}
		}
	})
}

func TestBuildInstallmentsNegativeLastInstallment(t *testing.T) {
	testutil.KnownBug(t, "0.7", "rounding each installment up can make the last installment negative", func(t testing.TB) {
		s := newCreditService(card(25, 5), fakePeriods{})
		in := CreateInput{Amount: 0.5, Installments: 60, SpentAt: date(2026, 3, 20)}
		got, err := s.buildInstallments(context.Background(), in, domain.PaymentMethod{ID: pmID, Kind: domain.KindCredit}, 0.5, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// 0.50 / 60 = 0.0083 → 0.01 each; the 60th gets 0.50 - 0.59 = -0.09.
		for _, g := range got {
			if g.Installment.InstallmentAmount < 0 {
				t.Errorf("installment %d is negative: %v", g.Installment.InstallmentNumber, g.Installment.InstallmentAmount)
			}
		}
	})
}
