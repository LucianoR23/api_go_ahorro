package expenses

import (
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/api_go_ahorra/internal/money"
)

// numericFromDecimal: a decimal as NUMERIC with scale decimals (plan 0.7).
// The error is always nil; it keeps the shape of the old float helper.
func numericFromDecimal(d decimal.Decimal, scale int32) (pgtype.Numeric, error) {
	return money.ToNumeric(d, scale), nil
}
