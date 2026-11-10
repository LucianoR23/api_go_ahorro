package fxrates

// Convert with a warm cache (plan 0.11, sources from plan 5.1). The DB
// fallback (cold cache) is covered in service_test.go.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

func warmService(rates ...domain.ExchangeRate) *Service {
	s := NewService(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.cache.setMany(rates)
	return s
}

func usd(src domain.RateSource, sell float64) domain.ExchangeRate {
	return domain.ExchangeRate{Currency: "USD", Source: src, RateAvg: sell - 10, RateBuy: sell - 20, RateSell: sell}
}

func TestConvert(t *testing.T) {
	s := warmService(
		usd(domain.RateSourceOficial, 1000),
		usd(domain.RateSourceBolsa, 1200),
		usd(domain.RateSourceBlue, 1250),
		domain.ExchangeRate{Currency: "EUR", Source: domain.RateSourceOficial, RateAvg: 1090, RateSell: 1100},
		domain.ExchangeRate{Currency: "EUR", Source: domain.RateSourceBlue, RateAvg: 1300}, // no selling price: the average
	)
	tests := []struct {
		name               string
		amount             string
		from, to           string
		source             domain.RateSource
		wantAmount, wantFx string
	}{
		{"same currency is identity", "50", "USD", "USD", domain.RateSourceBolsa, "50", "1"},
		{"foreign to ARS multiplies by the selling price of the source", "10", "USD", "ARS", domain.RateSourceBolsa, "12000", "1200"},
		{"another source, another rate", "10", "USD", "ARS", domain.RateSourceBlue, "12500", "1250"},
		{"ARS to foreign divides", "60000", "ARS", "USD", domain.RateSourceBolsa, "50", "1200"},
		{"cents are exact", "0.10", "USD", "ARS", domain.RateSourceOficial, "100", "1000"},
		{"the average when there is no selling price", "1", "EUR", "ARS", domain.RateSourceBlue, "1300", "1300"},
		{"a euro without that source goes through the dollar", "1", "EUR", "ARS", domain.RateSourceBolsa, "1320", "1320"},
		{"cross rate goes through ARS", "12", "EUR", "USD", domain.RateSourceOficial, "13.2", "1.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fx, err := s.Convert(context.Background(), decimal.RequireFromString(tt.amount), tt.from, tt.to, tt.source)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(decimal.RequireFromString(tt.wantAmount)) || !fx.Equal(decimal.RequireFromString(tt.wantFx)) {
				t.Errorf("Convert(%v %s→%s, %s) = %v @ %v, want %v @ %v", tt.amount, tt.from, tt.to, tt.source, got, fx, tt.wantAmount, tt.wantFx)
			}
		})
	}
}

func TestConvertErrors(t *testing.T) {
	s := warmService(usd(domain.RateSourceBolsa, 1200))
	ctx := context.Background()
	for _, c := range [][2]string{{"", "ARS"}, {"USD", ""}} {
		_, _, err := s.Convert(ctx, decimal.NewFromInt(1), c[0], c[1], domain.RateSourceBolsa)
		var verr *domain.ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("Convert(%q→%q) err = %v, want a validation error", c[0], c[1], err)
		}
	}
	var verr *domain.ValidationError
	if _, _, err := s.Convert(ctx, decimal.NewFromInt(1), "USD", "ARS", "dolar_futuro"); !errors.As(err, &verr) || verr.Field != "rateSource" {
		t.Errorf("unknown source err = %v", err)
	}
	if _, _, err := s.Convert(ctx, decimal.NewFromInt(1), "USD", "ARS", domain.RateSourceTarjeta); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing dollar err = %v, want ErrNotFound", err)
	}
	// A euro cannot go through the dollar without the official rates.
	if _, _, err := s.Convert(ctx, decimal.NewFromInt(1), "EUR", "ARS", domain.RateSourceBolsa); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("missing euro err = %v, want ErrNotFound", err)
	}
}

func TestPickRateSource(t *testing.T) {
	for _, tc := range []struct {
		picked, household, want domain.RateSource
	}{
		{"", domain.RateSourceBolsa, domain.RateSourceBolsa},
		{domain.RateSourceTarjeta, domain.RateSourceBolsa, domain.RateSourceTarjeta},
		{"", "", domain.DefaultRateSource},
	} {
		if got, err := domain.PickRateSource(tc.picked, tc.household); err != nil || got != tc.want {
			t.Errorf("PickRateSource(%q, %q) = %q, %v; want %q", tc.picked, tc.household, got, err, tc.want)
		}
	}
	if _, err := domain.PickRateSource("nope", domain.RateSourceBolsa); err == nil {
		t.Error("unknown source accepted")
	}
}
