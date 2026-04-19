package fxrates

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
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
	for _, r := range rates {
		// Validación mínima: bluelytics rara vez devuelve 0, pero si pasa
		// no queremos persistir un valor que haga Convert() dividir por 0.
		if r.RateAvg <= 0 {
			s.logger.WarnContext(ctx, "fxrates: rate inválido descartado", "currency", r.Currency, "source", r.Source)
			continue
		}
		if err := s.repo.Upsert(ctx, r); err != nil {
			s.logger.ErrorContext(ctx, "fxrates: upsert", "error", err, "currency", r.Currency)
			continue
		}
	}
	s.cache.setMany(rates)
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
// Si `from == to` devuelve el mismo monto + rate=1.
// El rate retornado es el que se usó para convertir (para guardar en expense).
func (s *Service) Convert(ctx context.Context, amount float64, from, to string) (converted, rate float64, err error) {
	if from == "" || to == "" {
		return 0, 0, domain.NewValidationError("currency", "from/to requeridos")
	}
	if from == to {
		return amount, 1, nil
	}

	const ars = "ARS"
	source := domain.RateSourceBlue

	// Helper: obtener el rate de una moneda extranjera contra ARS.
	get := func(currency string) (float64, error) {
		if r, ok := s.cache.get(currency, source); ok && r.RateAvg > 0 {
			return r.RateAvg, nil
		}
		// Fallback a DB: caché puede estar frío justo después de reiniciar.
		r, err := s.repo.GetLatest(ctx, currency, source)
		if err != nil {
			return 0, fmt.Errorf("sin cotización para %s: %w", currency, err)
		}
		s.cache.set(r)
		return r.RateAvg, nil
	}

	switch {
	case from == ars:
		r, err := get(to)
		if err != nil {
			return 0, 0, err
		}
		return amount / r, r, nil
	case to == ars:
		r, err := get(from)
		if err != nil {
			return 0, 0, err
		}
		return amount * r, r, nil
	default:
		rFrom, err := get(from)
		if err != nil {
			return 0, 0, err
		}
		rTo, err := get(to)
		if err != nil {
			return 0, 0, err
		}
		return amount * rFrom / rTo, rFrom / rTo, nil
	}
}
