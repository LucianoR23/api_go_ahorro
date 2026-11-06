// Package money does the arithmetic on amounts (plan 0.7). Amounts are
// decimal.Decimal, never float64: NUMERIC(12,2) in the database, a JSON
// number on the wire.
package money

import (
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
)

// Scales of the NUMERIC columns.
const (
	AmountScale = 2 // amounts: NUMERIC(12,2) / (14,2)
	RateScale   = 4 // FX rates and split weights: NUMERIC(12,4)
)

func init() {
	// Keep the API contract: amounts go out as JSON numbers (123.45), not
	// strings. Decoding accepts both.
	decimal.MarshalJSONWithoutQuotes = true
}

// MaxDigits: integer digits an amount may have (NUMERIC(12,2) holds up to
// 9999999999.99).
const MaxDigits = 10

// InRange reports whether d fits an amount column. It only looks at the
// coefficient's digit count and the exponent, so it is cheap even for input
// like 1e9999 (the decoder's limit), which must be rejected before any
// arithmetic: rounding or comparing it builds a number with 10,000 digits.
func InRange(d decimal.Decimal) bool {
	if d.IsZero() {
		return true
	}
	e := int64(d.Exponent())
	if e < -12 || e > MaxDigits {
		return false
	}
	return int64(d.NumDigits())+e <= MaxDigits
}

// Round rounds to scale decimals, half away from zero (what NUMERIC does on
// insert).
func Round(d decimal.Decimal, scale int32) decimal.Decimal { return d.Round(scale) }

// Allocate splits total (already at AmountScale) into len(weights) parts in
// proportion to weights. Every part but the last is rounded down to cents;
// the last takes the rest, so the parts add up to total exactly and none is
// negative when total and the weights are not negative. All weights zero
// (or none positive) splits evenly. Order is kept.
func Allocate(total decimal.Decimal, weights []decimal.Decimal) []decimal.Decimal {
	n := len(weights)
	if n == 0 {
		return nil
	}
	sumW := decimal.Zero
	for _, w := range weights {
		sumW = sumW.Add(w)
	}
	out := make([]decimal.Decimal, n)
	allocated := decimal.Zero
	for i := 0; i < n-1; i++ {
		var share decimal.Decimal
		if sumW.Sign() > 0 {
			share = total.Mul(weights[i]).Div(sumW)
		} else {
			share = total.Div(decimal.NewFromInt(int64(n)))
		}
		out[i] = share.RoundDown(AmountScale)
		allocated = allocated.Add(out[i])
	}
	out[n-1] = total.Sub(allocated)
	return out
}

// Even splits total into n equal parts with the same rule as Allocate.
func Even(total decimal.Decimal, n int) []decimal.Decimal {
	if n <= 0 {
		return nil
	}
	w := make([]decimal.Decimal, n)
	for i := range w {
		w[i] = decimal.NewFromInt(1)
	}
	return Allocate(total, w)
}

// ToNumeric converts d to a NUMERIC with scale decimals (rounded half away
// from zero).
func ToNumeric(d decimal.Decimal, scale int32) pgtype.Numeric {
	r := d.Round(scale)
	return pgtype.Numeric{Int: r.Shift(scale).BigInt(), Exp: -scale, Valid: true}
}

// FromNumeric converts a NUMERIC to a decimal; NULL or invalid is zero.
func FromNumeric(n pgtype.Numeric) decimal.Decimal {
	if !n.Valid || n.Int == nil {
		return decimal.Zero
	}
	return decimal.NewFromBigInt(new(big.Int).Set(n.Int), n.Exp)
}

// FromFloat converts a float64, such as an amount decoded from a request
// body (kept float64 there so a wrong type still names the field in the
// 422). It uses the shortest decimal that round-trips, so it is exact for
// every amount a column can hold (at most 12 significant digits): 0.1 stays
// 0.1.
func FromFloat(f float64) decimal.Decimal { return decimal.NewFromFloat(f) }

// Parse reads a decimal string; used by tests and config.
func Parse(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, fmt.Errorf("monto inválido %q: %w", s, err)
	}
	return d, nil
}
