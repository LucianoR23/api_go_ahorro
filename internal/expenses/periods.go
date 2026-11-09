package expenses

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// periodsReader es la dependencia mínima que el resolver necesita del
// package creditperiods. Lo definimos como interface para no acoplar
// directamente al struct del otro package (y para poder testear).
type periodsReader interface {
	Get(ctx context.Context, creditCardID uuid.UUID, periodYM string) (domain.CreditCardPeriod, error)
}

// resolvedPeriod: lo que el service necesita para armar un installment.
type resolvedPeriod struct {
	BillingDate time.Time // closing_date del período
	DueDate     time.Time // due_date del período
}

// resolveForClosingMonth: resuelve el período partiendo de un
// month base explícito (para installments 2..N).
func resolveForClosingMonth(
	ctx context.Context,
	reader periodsReader,
	cc domain.CreditCard,
	closingMonth time.Time,
) (resolvedPeriod, error) {
	ym := domain.PeriodYMFromDate(closingMonth)

	// Override for that month if loaded, the card's defaults otherwise.
	var override *domain.CreditCardPeriod
	p, err := reader.Get(ctx, cc.ID, ym)
	switch {
	case err == nil:
		override = &p
	case !errors.Is(err, domain.ErrNotFound):
		return resolvedPeriod{}, err
	}
	closing, due := domain.CardPeriodDates(cc, closingMonth, override)
	return resolvedPeriod{BillingDate: closing, DueDate: due}, nil
}

// addMonths returns the closing month of installment n (0-indexed) as the
// first day of that month: n=0 is the base month, n=1 the one after, etc.
// It moves from day 1 because AddDate normalizes overflow (Oct 31 + 1 month
// is Dec 1), which skipped a statement for month-end purchases (plan 1.10d).
func addMonths(base time.Time, n int) time.Time {
	return time.Date(base.Year(), base.Month()+time.Month(n), 1, 0, 0, 0, 0, base.Location())
}
