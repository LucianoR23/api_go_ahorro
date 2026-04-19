// Package fxrates: cotizaciones ARS/USD/EUR vía bluelytics.
//
// Responsabilidades:
//   - Fetcher: pega a bluelytics y parsea la respuesta.
//   - Repository: persiste y lee exchange_rates.
//   - Cache: RWMutex sobre map[currency]domain.ExchangeRate para lecturas hot.
//   - Worker: dispara el fetch cada N minutos.
//   - Service: API pública (Current, Convert, Refresh).
//
// El API handler solo consume Service. El resto es interno.
package fxrates

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// Upsert inserta una cotización; si ya existe (mismo currency+source+last_update)
// la ignora (ON CONFLICT DO NOTHING en la query).
func (r *Repository) Upsert(ctx context.Context, rate domain.ExchangeRate) error {
	avg, err := numericFromFloat(rate.RateAvg)
	if err != nil {
		return fmt.Errorf("rate_avg: %w", err)
	}
	buy, err := numericFromFloat(rate.RateBuy)
	if err != nil {
		return fmt.Errorf("rate_buy: %w", err)
	}
	sell, err := numericFromFloat(rate.RateSell)
	if err != nil {
		return fmt.Errorf("rate_sell: %w", err)
	}
	return r.q.UpsertExchangeRate(ctx, sqlcgen.UpsertExchangeRateParams{
		Currency:   rate.Currency,
		Source:     string(rate.Source),
		LastUpdate: pgtype.Timestamptz{Time: rate.LastUpdate, Valid: true},
		RateAvg:    avg,
		RateBuy:    buy,
		RateSell:   sell,
	})
}

// GetLatest devuelve la cotización más reciente para (currency, source).
// ErrNotFound si nunca se registró nada (bootstrap sin fetch previo).
func (r *Repository) GetLatest(ctx context.Context, currency string, source domain.RateSource) (domain.ExchangeRate, error) {
	row, err := r.q.GetLatestExchangeRate(ctx, sqlcgen.GetLatestExchangeRateParams{
		Currency: currency,
		Source:   string(source),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExchangeRate{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ExchangeRate{}, fmt.Errorf("fxrates.GetLatest: %w", err)
	}
	return toDomain(row), nil
}

// ListLatest devuelve la última fila por (currency, source). Se usa para
// hidratar el caché al arrancar y para el endpoint /current.
func (r *Repository) ListLatest(ctx context.Context) ([]domain.ExchangeRate, error) {
	rows, err := r.q.ListLatestExchangeRates(ctx)
	if err != nil {
		return nil, fmt.Errorf("fxrates.ListLatest: %w", err)
	}
	out := make([]domain.ExchangeRate, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

// compile-time guard para que no se rompa el paquete si se mueve time sin uso.
var _ = time.Time{}
