package fxrates

import (
	"context"
	"fmt"
	"github.com/LucianoR23/valuo_api/internal/money"
	"github.com/shopspring/decimal"
	"log/slog"

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

// Refresh pega a bluelytics, persiste y actualiza el caché.
// Si bluelytics falla, devolvemos error pero el caché no se modifica
// (mantenemos la última cotización conocida).
func (s *Service) Refresh(ctx context.Context) error {
	rates, err := s.fetcher.Fetch(ctx)
	if err != nil {
		return err
	}
	valid := make([]domain.ExchangeRate, 0, len(rates))
	for _, r := range rates {
		// Validación mínima: bluelytics rara vez devuelve 0, pero si pasa
		// no queremos persistir un valor que haga Convert() dividir por 0.
		if r.RateAvg <= 0 {
			s.logger.WarnContext(ctx, "fxrates: rate inválido descartado", "currency", r.Currency, "source", r.Source)
			continue
		}
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

// Convert convierte un monto entre monedas soportadas. Usa "blue" por default.
//
// Modelo mental: todas las rates son "1 unidad de `currency` = rateAvg ARS".
//   - ARS → X: amount / rate(X)
//   - X → ARS: amount * rate(X)
//   - X → Y:   amount * rate(X) / rate(Y)
//
// Si `from == to` devuelve el mismo monto + rate=1. The converted amount
// is not rounded (plan 0.7): callers round it to cents when they store it.
// El rate retornado es el que se usó para convertir (para guardar en expense).
func (s *Service) Convert(ctx context.Context, amount decimal.Decimal, from, to string) (converted, rate decimal.Decimal, err error) {
	if from == "" || to == "" {
		return decimal.Zero, decimal.Zero, domain.NewValidationError("currency", "from/to requeridos")
	}
	one := decimal.NewFromInt(1)
	if from == to {
		return amount, one, nil
	}

	const ars = "ARS"
	source := domain.RateSourceBlue

	// get: the rate of a foreign currency against ARS. Rates are NUMERIC(12,4)
	// in the database; FromFloat restores those 4 decimals exactly.
	get := func(currency string) (decimal.Decimal, error) {
		if r, ok := s.cache.get(currency, source); ok && r.RateAvg > 0 {
			return money.FromFloat(r.RateAvg), nil
		}
		// Fallback a DB: caché puede estar frío justo después de reiniciar.
		r, err := s.repo.GetLatest(ctx, currency, source)
		if err != nil {
			return decimal.Zero, fmt.Errorf("sin cotización para %s: %w", currency, err)
		}
		s.cache.set(r)
		return money.FromFloat(r.RateAvg), nil
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
