// Package creditperiods: cierre y vencimiento por mes de una tarjeta de
// crédito. Tabla separada de credit_cards para permitir overrides mensuales.
// Si no hay row para un (card, ym) dado, el service cae a los defaults de
// la credit_card.
package creditperiods

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
)

type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

func (r *Repository) Upsert(ctx context.Context, creditCardID uuid.UUID, periodYM string, closing, due time.Time) (domain.CreditCardPeriod, error) {
	row, err := r.q.UpsertCreditCardPeriod(ctx, sqlcgen.UpsertCreditCardPeriodParams{
		CreditCardID: creditCardID,
		PeriodYm:     periodYM,
		ClosingDate:  pgtype.Date{Time: closing, Valid: true},
		DueDate:      pgtype.Date{Time: due, Valid: true},
	})
	if err != nil {
		return domain.CreditCardPeriod{}, fmt.Errorf("creditperiods.Upsert: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) Get(ctx context.Context, creditCardID uuid.UUID, periodYM string) (domain.CreditCardPeriod, error) {
	row, err := r.q.GetCreditCardPeriod(ctx, sqlcgen.GetCreditCardPeriodParams{
		CreditCardID: creditCardID,
		PeriodYm:     periodYM,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CreditCardPeriod{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CreditCardPeriod{}, fmt.Errorf("creditperiods.Get: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) List(ctx context.Context, creditCardID uuid.UUID) ([]domain.CreditCardPeriod, error) {
	rows, err := r.q.ListCreditCardPeriods(ctx, creditCardID)
	if err != nil {
		return nil, fmt.Errorf("creditperiods.List: %w", err)
	}
	out := make([]domain.CreditCardPeriod, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

func (r *Repository) Delete(ctx context.Context, creditCardID uuid.UUID, periodYM string) error {
	if err := r.q.DeleteCreditCardPeriod(ctx, sqlcgen.DeleteCreditCardPeriodParams{
		CreditCardID: creditCardID,
		PeriodYm:     periodYM,
	}); err != nil {
		return fmt.Errorf("creditperiods.Delete: %w", err)
	}
	return nil
}

// GetLatest devuelve el período más reciente por period_ym DESC.
// Usado por status() para detectar si el vencimiento ya pasó y hay que
// cargar el siguiente mes.
func (r *Repository) GetLatest(ctx context.Context, creditCardID uuid.UUID) (domain.CreditCardPeriod, error) {
	row, err := r.q.GetLatestCreditCardPeriod(ctx, creditCardID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CreditCardPeriod{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.CreditCardPeriod{}, fmt.Errorf("creditperiods.GetLatest: %w", err)
	}
	return toDomain(row), nil
}

// CardWithLatestPeriod is an active credit card of a household member with
// its most recent period.
type CardWithLatestPeriod struct {
	PaymentMethodID uuid.UUID
	OwnerUserID     uuid.UUID
	CreditCardID    uuid.UUID
	Alias           string
	Latest          domain.CreditCardPeriod
}

// ListActiveCardsWithLatestPeriodByHousehold returns, in one query, the
// active credit cards of every member of the household with their latest
// period. Cards without periods are left out.
func (r *Repository) ListActiveCardsWithLatestPeriodByHousehold(ctx context.Context, householdID uuid.UUID) ([]CardWithLatestPeriod, error) {
	rows, err := r.q.ListActiveCardsWithLatestPeriodByHousehold(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("creditperiods.ListActiveCardsWithLatestPeriodByHousehold: %w", err)
	}
	out := make([]CardWithLatestPeriod, len(rows))
	for i, row := range rows {
		out[i] = CardWithLatestPeriod{
			PaymentMethodID: row.PaymentMethodID,
			OwnerUserID:     row.OwnerUserID,
			CreditCardID:    row.CreditCardID,
			Alias:           row.Alias,
			Latest: domain.CreditCardPeriod{
				CreditCardID: row.CreditCardID,
				PeriodYM:     row.PeriodYm,
				ClosingDate:  row.ClosingDate.Time,
				DueDate:      row.DueDate.Time,
			},
		}
	}
	return out, nil
}

func toDomain(p sqlcgen.CreditCardPeriod) domain.CreditCardPeriod {
	return domain.CreditCardPeriod{
		CreditCardID: p.CreditCardID,
		PeriodYM:     p.PeriodYm,
		ClosingDate:  p.ClosingDate.Time,
		DueDate:      p.DueDate.Time,
		CreatedAt:    p.CreatedAt.Time,
		UpdatedAt:    p.UpdatedAt.Time,
	}
}
