package expenses

import (
	"context"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Regression for plan 1.10d: moving a purchase date by whole months used to
// overflow on days 29 to 31 (Oct 31 + 1 month = Dec 1), so month-end card
// purchases skipped a statement.
func TestBuildInstallmentsCreditMonthEnd(t *testing.T) {
	credit := domain.PaymentMethod{ID: pmID, Kind: domain.KindCredit}
	tests := []struct {
		name         string
		closing, due int
		spentAt      time.Time
		n            int
		wantBilling  []time.Time
		wantDueDates []time.Time
	}{
		{
			name:    "bought Oct 31 after an Oct 2 closing: November statement",
			closing: 2, due: 10, spentAt: date(2026, 10, 31), n: 1,
			wantBilling:  []time.Time{date(2026, 11, 2)},
			wantDueDates: []time.Time{date(2026, 11, 10)},
		},
		{
			name:    "3 installments from Jan 30 with a closing on the 31st: Jan, Feb, Mar",
			closing: 31, due: 10, spentAt: date(2027, 1, 30), n: 3,
			wantBilling:  []time.Time{date(2027, 1, 31), date(2027, 2, 28), date(2027, 3, 31)},
			wantDueDates: []time.Time{date(2027, 2, 10), date(2027, 3, 10), date(2027, 4, 10)},
		},
		{
			name:    "bought Jan 31 after a Jan 15 closing, 2 installments: Feb, Mar",
			closing: 15, due: 5, spentAt: date(2027, 1, 31), n: 2,
			wantBilling:  []time.Time{date(2027, 2, 15), date(2027, 3, 15)},
			wantDueDates: []time.Time{date(2027, 3, 5), date(2027, 4, 5)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newCreditService(card(tt.closing, tt.due), fakePeriods{})
			in := CreateInput{Amount: dec(300), Installments: tt.n, SpentAt: tt.spentAt}
			got, err := s.buildInstallments(context.Background(), in, credit, dec(300), nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.n {
				t.Fatalf("got %d installments, want %d", len(got), tt.n)
			}
			for i := range got {
				g := got[i].Installment
				if !g.BillingDate.Equal(tt.wantBilling[i]) || g.DueDate == nil || !g.DueDate.Equal(tt.wantDueDates[i]) {
					t.Errorf("installment %d: billing %s due %v, want %s / %s", i+1,
						g.BillingDate.Format(time.DateOnly), g.DueDate,
						tt.wantBilling[i].Format(time.DateOnly), tt.wantDueDates[i].Format(time.DateOnly))
				}
			}
		})
	}
}
