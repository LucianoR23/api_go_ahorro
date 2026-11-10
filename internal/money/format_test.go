package money_test

import (
	"testing"

	"github.com/LucianoR23/valuo_api/internal/money"
)

func TestFormat(t *testing.T) {
	for _, tc := range []struct {
		amount, currency, want string
	}{
		{"19450", "ARS", "$ 19.450"},
		{"19450.00", "ARS", "$ 19.450"},
		{"1050.5", "ARS", "$ 1.050,50"},
		{"128450.55", "ARS", "$ 128.450,55"},
		{"999", "ARS", "$ 999"},
		{"1000", "ARS", "$ 1.000"},
		{"1234567.89", "ARS", "$ 1.234.567,89"},
		{"9999999999.99", "ARS", "$ 9.999.999.999,99"},
		{"0", "ARS", "$ 0"},
		{"0.3", "ARS", "$ 0,30"},
		{"500", "USD", "US$ 500"},
		{"1200.75", "EUR", "€ 1.200,75"},
		{"10", "BRL", "BRL 10"},
		{"-20", "ARS", "-$ 20"},
		{"-1500.5", "USD", "-US$ 1.500,50"},
		{"2.005", "ARS", "$ 2,01"}, // rounds half away from zero, like NUMERIC
		{"-0.001", "ARS", "$ 0"},   // rounds to zero: no sign
		{"1999.999", "ARS", "$ 2.000"},
	} {
		t.Run(tc.amount+" "+tc.currency, func(t *testing.T) {
			if got := money.Format(d(tc.amount), tc.currency); got != tc.want {
				t.Errorf("Format(%s, %s) = %q, want %q", tc.amount, tc.currency, got, tc.want)
			}
		})
	}
}
