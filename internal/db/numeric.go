// Package db helpers compartidos de conversión pgtype.Numeric ⇄ float64.
// Movidos acá para que múltiples paquetes (expenses, splitrules, settlements)
// no dupliquen el código.
package db

import (
	"fmt"
	"math"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"
)

// NumericFromFloat convierte float64 → pgtype.Numeric con `scale` decimales
// (redondeo half away from zero). scale=2 para amounts, scale=4 para rates
// y weights.
func NumericFromFloat(v float64, scale int) (pgtype.Numeric, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return pgtype.Numeric{}, fmt.Errorf("valor inválido: %v", v)
	}
	mult := math.Pow10(scale)
	scaled := v * mult
	if scaled > 1e18 || scaled < -1e18 {
		return pgtype.Numeric{}, fmt.Errorf("valor fuera de rango: %v", v)
	}
	var i64 int64
	if scaled >= 0 {
		i64 = int64(scaled + 0.5)
	} else {
		i64 = int64(scaled - 0.5)
	}
	return pgtype.Numeric{
		Int:   new(big.Int).SetInt64(i64),
		Exp:   int32(-scale),
		Valid: true,
	}, nil
}

// FloatFromNumeric convierte pgtype.Numeric → float64. Precisión suficiente
// para montos de hasta ~15 dígitos significativos.
func FloatFromNumeric(n pgtype.Numeric) float64 {
	if !n.Valid || n.Int == nil {
		return 0
	}
	f, _ := new(big.Float).SetInt(n.Int).Float64()
	if n.Exp < 0 {
		f /= math.Pow10(int(-n.Exp))
	} else if n.Exp > 0 {
		f *= math.Pow10(int(n.Exp))
	}
	return f
}

// RoundTo redondea a `scale` decimales (half away from zero).
// Uso típico: normalización de shares donde el último absorbe el residuo.
func RoundTo(v float64, scale int) float64 {
	mult := math.Pow10(scale)
	if v >= 0 {
		return math.Floor(v*mult+0.5) / mult
	}
	return math.Ceil(v*mult-0.5) / mult
}
