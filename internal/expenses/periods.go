package expenses

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
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

// resolveCreditPeriod calcula billing/due del período en el que cae `spentAt`.
// Si hay override en credit_card_periods lo usa; si no, deriva de los
// defaults de la credit_card.
//
// Regla con defaults (confirmada con el user):
//   - si spentAt.day <= closing_day  → cierra este mes
//   - si spentAt.day >  closing_day  → cierra el mes siguiente
//   - due_day > closing_day          → vence el mismo mes que cierra
//   - due_day <= closing_day         → vence el mes siguiente
func resolveCreditPeriod(
	ctx context.Context,
	reader periodsReader,
	cc domain.CreditCard,
	spentAt time.Time,
) (resolvedPeriod, error) {
	closingMonth := spentAt
	if spentAt.Day() > cc.DefaultClosingDay {
		closingMonth = spentAt.AddDate(0, 1, 0)
	}
	return resolveForClosingMonth(ctx, reader, cc, closingMonth)
}

// resolveForClosingMonth: igual que resolveCreditPeriod pero partiendo de un
// month base explícito (para installments 2..N).
func resolveForClosingMonth(
	ctx context.Context,
	reader periodsReader,
	cc domain.CreditCard,
	closingMonth time.Time,
) (resolvedPeriod, error) {
	ym := domain.PeriodYMFromDate(closingMonth)

	// 1) Override explícito para ese mes.
	p, err := reader.Get(ctx, cc.ID, ym)
	if err == nil {
		return resolvedPeriod{BillingDate: p.ClosingDate, DueDate: p.DueDate}, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return resolvedPeriod{}, err
	}

	// 2) Fallback a defaults.
	closing := clampDay(closingMonth.Year(), int(closingMonth.Month()), cc.DefaultClosingDay)

	dueYear, dueMonth := closingMonth.Year(), int(closingMonth.Month())
	if cc.DefaultDueDay <= cc.DefaultClosingDay {
		// Due es en el mes siguiente al de cierre.
		nextDueMonth := closingMonth.AddDate(0, 1, 0)
		dueYear, dueMonth = nextDueMonth.Year(), int(nextDueMonth.Month())
	}
	due := clampDay(dueYear, dueMonth, cc.DefaultDueDay)

	return resolvedPeriod{BillingDate: closing, DueDate: due}, nil
}

// addMonths devuelve el "closing month" del installment n (0-indexed).
// n=0 es el mes base; n=1 un mes después; etc.
func addMonths(base time.Time, n int) time.Time {
	return base.AddDate(0, n, 0)
}

// clampDay construye una fecha (year, month, day) pero si `day` excede los
// días del mes (ej: day=31 en febrero) lo colapsa al último día válido.
// time.Date con day=31 en feb normaliza a "3 de marzo", lo cual no queremos.
func clampDay(year, month, day int) time.Time {
	last := lastDayOfMonth(year, month)
	if day > last {
		day = last
	}
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

func lastDayOfMonth(year, month int) int {
	// Día 0 del mes siguiente == último día del mes actual.
	t := time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, time.UTC)
	return t.Day()
}
