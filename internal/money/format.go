package money

import (
	"strings"

	"github.com/shopspring/decimal"
)

// prefixes: the symbol each currency shows in front of an amount, the same
// as the app's formatAmount. Unknown currencies show their code.
var prefixes = map[string]string{"ARS": "$", "USD": "US$", "EUR": "€"}

// Format writes an amount for people, es-AR style (plan 1.10c): "$ 19.450",
// "US$ 500,50", "-$ 1.200". Thousands with ".", cents with ",", and ",00"
// dropped when the amount is whole. It matches the app's formatAmount, so a
// notification reads like the rest of the screen, and the app's discreet
// mode masks it by pattern.
func Format(d decimal.Decimal, currency string) string {
	prefix, ok := prefixes[currency]
	if !ok {
		prefix = currency
	}
	d = d.Round(AmountScale)
	sign := ""
	if d.IsNegative() {
		sign = "-"
		d = d.Neg()
	}
	whole, cents, _ := strings.Cut(d.StringFixed(AmountScale), ".")
	out := sign + prefix + " " + groupThousands(whole)
	if cents != "00" {
		out += "," + cents
	}
	return out
}

// groupThousands puts a "." every three digits from the right: 1234567 is
// 1.234.567.
func groupThousands(digits string) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	head := len(digits) % 3
	if head > 0 {
		b.WriteString(digits[:head])
	}
	for i := head; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}
