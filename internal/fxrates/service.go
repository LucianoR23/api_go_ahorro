package fxrates

import (
	"context"
	"fmt"
	"github.com/LucianoR23/valuo_api/internal/money"
	"github.com/shopspring/decimal"
	"log/slog"
	"time"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Service: cara pública del paquete. Los handlers y los services de
// expenses/incomes (más adelante) van a depender de este.
//
// Fuente de verdad para Convert: el caché en memoria. Si la moneda
// pedida no está cacheada, cae a DB y por último devuelve error.
type Service struct {
	repo    *Repository
	fetcher *Fetcher
	cache   *cache
	logger  *slog.Logger
}

func NewService(repo *Repository, fetcher *Fetcher, logger *slog.Logger) *Service {
	return &Service{
		repo:    repo,
		fetcher: fetcher,
		cache:   newCache(),
		logger:  logger,
	}
}

// Hydrate carga el caché desde DB. Se llama una vez al arrancar para
// que el API sirva Convert aunque el primer fetch todavía no corrió.
func (s *Service) Hydrate(ctx context.Context) error {
	rates, err := s.repo.ListLatest(ctx)
	if err != nil {
		return fmt.Errorf("fxrates.Hydrate: %w", err)
	}
	s.cache.setMany(rates)
	s.logger.InfoContext(ctx, "fxrates: caché hidratado desde DB", "count", len(rates))
	return nil
}

// Refresh fetches DolarAPI and Bluelytics, stores and caches the rates.
// When neither answers it returns the error and the cache keeps the last
// known rates; when only one fails, it logs it and keeps the other's.
func (s *Service) Refresh(ctx context.Context) error {
	rates, err := s.fetcher.Fetch(ctx)
	if len(rates) == 0 {
		if err == nil {
			err = fmt.Errorf("fxrates.Refresh: no rates")
		}
		return err
	}
	if err != nil {
		s.logger.WarnContext(ctx, "fxrates: one upstream failed", "error", err)
	}
	valid := make([]domain.ExchangeRate, 0, len(rates))
	for _, r := range rates {
		// Validación mínima: bluelytics rara vez devuelve 0, pero si pasa
		// no queremos persistir un valor que haga Convert() dividir por 0.
		if r.RateAvg <= 0 {
			s.logger.WarnContext(ctx, "fxrates: rate inválido descartado", "currency", r.Currency, "source", r.Source)
			continue
		}
		r.FetchedAt = time.Now().UTC()
		valid = append(valid, r)
		if err := s.repo.Upsert(ctx, r); err != nil {
			s.logger.ErrorContext(ctx, "fxrates: upsert", "error", err, "currency", r.Currency)
			continue
		}
	}
	// Only valid rates reach the cache: a discarded rate must not replace the
	// last good one served by GET /exchange-rates/current.
	s.cache.setMany(valid)
	return nil
}

// Current devuelve un snapshot del caché. No pega a DB.
func (s *Service) Current() []domain.ExchangeRate {
	return s.cache.snapshot()
}

// Convert converts amount between supported currencies with the rates of
// source (the household's, or the one picked for a movement; plan 5.1).
//
// Every rate is "1 unit of currency = rate ARS", the source's selling price
// (venta: what the dollar costs, as DolarAPI publishes it), or the average
// when an upstream gave no selling price.
//   - ARS → X: amount / rate(X)
//   - X → ARS: amount * rate(X)
//   - X → Y:   amount * rate(X) / rate(Y)
//
// A currency without that source (only the official and blue euro exist)
// goes through the dollar: rate(EUR, s) = rate(EUR, oficial) × rate(USD, s)
// / rate(USD, oficial). Same currencies return amount and rate 1. The
// converted amount is not rounded (plan 0.7): callers round it to cents when
// they store it. The returned rate is the one used, to store with the row.
func (s *Service) Convert(ctx context.Context, amount decimal.Decimal, from, to string, source domain.RateSource) (converted, rate decimal.Decimal, err error) {
	if from == "" || to == "" {
		return decimal.Zero, decimal.Zero, domain.NewValidationError("currency", "from/to requeridos")
	}
	if !source.Valid() {
		return decimal.Zero, decimal.Zero, domain.NewValidationError("rateSource", "cotización desconocida")
	}
	one := decimal.NewFromInt(1)
	if from == to {
		return amount, one, nil
	}

	const ars = "ARS"
	get := func(currency string) (decimal.Decimal, error) {
		r, err := s.rate(ctx, currency, source)
		if err == nil || currency == "USD" || source == domain.RateSourceOficial {
			return r, err
		}
		cur, errC := s.rate(ctx, currency, domain.RateSourceOficial)
		usd, errU := s.rate(ctx, "USD", source)
		usdOf, errO := s.rate(ctx, "USD", domain.RateSourceOficial)
		if errC != nil || errU != nil || errO != nil {
			return decimal.Zero, err
		}
		return cur.Mul(usd).Div(usdOf).Round(4), nil
	}

	switch {
	case from == ars:
		r, err := get(to)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		return amount.Div(r), r, nil
	case to == ars:
		r, err := get(from)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		return amount.Mul(r), r, nil
	default:
		rFrom, err := get(from)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		rTo, err := get(to)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		cross := rFrom.Div(rTo)
		return amount.Mul(rFrom).Div(rTo), cross, nil
	}
}

// rate: the rate of a foreign currency against ARS for source, from the
// cache or, cold, from the database. Rates are NUMERIC(12,4); FromFloat
// restores those 4 decimals exactly.
func (s *Service) rate(ctx context.Context, currency string, source domain.RateSource) (decimal.Decimal, error) {
	r, ok := s.cache.get(currency, source)
	if !ok && s.repo != nil {
		got, err := s.repo.GetLatest(ctx, currency, source)
		if err != nil {
			return decimal.Zero, fmt.Errorf("sin cotización %s %s: %w", currency, source, err)
		}
		s.cache.set(got)
		r, ok = got, true
	}
	if !ok {
		return decimal.Zero, fmt.Errorf("sin cotización %s %s: %w", currency, source, domain.ErrNotFound)
	}
	v := r.RateSell
	if v <= 0 {
		v = r.RateAvg
	}
	if v <= 0 {
		return decimal.Zero, fmt.Errorf("sin cotización %s %s: %w", currency, source, domain.ErrNotFound)
	}
	return money.FromFloat(v), nil
}

// Days of history: 30 by default, a year at most (plan 5.2).
const (
	DefaultHistoryDays = 30
	MaxHistoryDays     = 365
)

// HistoryPoint: a day's last quote.
type HistoryPoint struct {
	Day  time.Time // calendar day in the app's timezone
	Rate domain.ExchangeRate
}

// History returns one quote per day for currency at source over the last
// days days, oldest first.
func (s *Service) History(ctx context.Context, currency string, source domain.RateSource, days int, now time.Time) ([]HistoryPoint, error) {
	if currency != "USD" && currency != "EUR" {
		return nil, domain.NewValidationError("currency", "debe ser USD o EUR")
	}
	if !source.Valid() {
		return nil, domain.NewValidationError("source", "cotización desconocida")
	}
	if days == 0 {
		days = DefaultHistoryDays
	}
	if days < 1 || days > MaxHistoryDays {
		return nil, domain.NewValidationError("days", "debe estar entre 1 y 365")
	}
	return s.repo.History(ctx, currency, source, now.AddDate(0, 0, -days))
}
