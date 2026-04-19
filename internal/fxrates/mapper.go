package fxrates

import (
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// numericFromFloat convierte un float64 a pgtype.Numeric conservando
// hasta 4 decimales (que es el scale de la columna NUMERIC(12,4)).
// Si alguna vez entra NaN o Inf devolvemos inválido y el caller lo filtra.
func numericFromFloat(v float64) (pgtype.Numeric, error) {
	// Escalamos por 10000 (4 decimales) y redondeamos al entero más cercano.
	scaled := v * 10000
	if scaled > 1e18 || scaled < -1e18 {
		return pgtype.Numeric{}, fmt.Errorf("valor fuera de rango: %v", v)
	}
	i := new(big.Int).SetInt64(int64(scaled + 0.5))
	return pgtype.Numeric{Int: i, Exp: -4, Valid: true}, nil
}

// floatFromNumeric convierte a float64. Pierde precisión si los decimales
// son muchos, pero para cotizaciones con 4 decimales alcanza y sobra.
func floatFromNumeric(n pgtype.Numeric) float64 {
	if !n.Valid || n.Int == nil {
		return 0
	}
	f, _ := new(big.Float).SetInt(n.Int).Float64()
	// Aplicar Exp: el valor real = Int * 10^Exp.
	for i := int32(0); i < -n.Exp; i++ {
		f /= 10
	}
	for i := int32(0); i < n.Exp; i++ {
		f *= 10
	}
	return f
}

func toDomain(r sqlcgen.ExchangeRate) domain.ExchangeRate {
	return domain.ExchangeRate{
		Currency:   r.Currency,
		Source:     domain.RateSource(r.Source),
		LastUpdate: r.LastUpdate.Time,
		RateAvg:    floatFromNumeric(r.RateAvg),
		RateBuy:    floatFromNumeric(r.RateBuy),
		RateSell:   floatFromNumeric(r.RateSell),
		FetchedAt:  r.FetchedAt.Time,
	}
}
