package expenses

import (
	"fmt"
	"math"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"
)

// numericFromFloat convierte float64 → pgtype.Numeric con `scale` decimales
// (redondeo al más cercano). scale=2 para amounts (NUMERIC(12,2)), scale=4
// para rates (NUMERIC(12,4)).
func numericFromFloat(v float64, scale int) (pgtype.Numeric, error) {
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

// floatFromNumeric convierte pgtype.Numeric → float64.
// Precisión: suficiente para amounts de hasta 12 dígitos con 2 decimales.
func floatFromNumeric(n pgtype.Numeric) float64 {
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

// roundTo redondea a `scale` decimales (half away from zero).
// Lo usamos para splits de shares: si hay 3 miembros y total=100.01,
// cada uno recibe 33.34 / 33.34 / 33.33 (el último absorbe el residuo).
func roundTo(v float64, scale int) float64 {
	mult := math.Pow10(scale)
	if v >= 0 {
		return math.Floor(v*mult+0.5) / mult
	}
	return math.Ceil(v*mult-0.5) / mult
}
