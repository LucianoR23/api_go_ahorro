package fxrates

// Characterization tests (plan 0.11) for Convert with a warm cache. The DB
// fallback (cold cache) is covered by the repository tests in 0.21.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math"
	"testing"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

func warmService(rates ...domain.ExchangeRate) *Service {
	s := NewService(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.cache.setMany(rates)
	return s
}

func TestConvert(t *testing.T) {
	s := warmService(
		domain.ExchangeRate{Currency: "USD", Source: domain.RateSourceBlue, RateAvg: 1200},
		domain.ExchangeRate{Currency: "EUR", Source: domain.RateSourceBlue, RateAvg: 1300},
		// Oficial is cached too but Convert always uses blue.
		domain.ExchangeRate{Currency: "USD", Source: domain.RateSourceOficial, RateAvg: 1000},
	)
	tests := []struct {
		name               string
		amount             float64
		from, to           string
		wantAmount, wantFx float64
	}{
		{name: "same currency is identity", amount: 50, from: "USD", to: "USD", wantAmount: 50, wantFx: 1},
		{name: "foreign to ARS multiplies by blue", amount: 10, from: "USD", to: "ARS", wantAmount: 12000, wantFx: 1200},
		{name: "ARS to foreign divides by blue", amount: 60000, from: "ARS", to: "USD", wantAmount: 50, wantFx: 1200},
		{name: "cross rate goes through ARS", amount: 13, from: "EUR", to: "USD", wantAmount: 13 * 1300.0 / 1200.0, wantFx: 1300.0 / 1200.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fx, err := s.Convert(context.Background(), tt.amount, tt.from, tt.to)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(got-tt.wantAmount) > 1e-9 || math.Abs(fx-tt.wantFx) > 1e-12 {
				t.Errorf("Convert(%v %s→%s) = %v @ %v, want %v @ %v", tt.amount, tt.from, tt.to, got, fx, tt.wantAmount, tt.wantFx)
			}
		})
	}
}

func TestConvertRequiresCurrencies(t *testing.T) {
	s := warmService()
	for _, c := range [][2]string{{"", "ARS"}, {"USD", ""}} {
		_, _, err := s.Convert(context.Background(), 1, c[0], c[1])
		var verr *domain.ValidationError
		if !errors.As(err, &verr) {
			t.Errorf("Convert(%q→%q) err = %v, want a validation error", c[0], c[1], err)
		}
	}
}
