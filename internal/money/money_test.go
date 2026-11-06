package money_test

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/api_go_ahorra/internal/money"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func strs(ds []decimal.Decimal) []string {
	out := make([]string, len(ds))
	for i, x := range ds {
		out[i] = x.StringFixed(2)
	}
	return out
}

func TestAllocate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		total   string
		weights []string
		want    []string
	}{
		{"even thirds: last absorbs", "100.00", []string{"1", "1", "1"}, []string{"33.33", "33.33", "33.34"}},
		{"rounds down, never up", "200.00", []string{"1", "1", "1"}, []string{"66.66", "66.66", "66.68"}},
		{"weighted 60/40", "100.01", []string{"0.6", "0.4"}, []string{"60.00", "40.01"}},
		{"tiny total, many parts: none negative", "0.04", []string{"1", "1", "1", "1", "1", "1"}, []string{"0.00", "0.00", "0.00", "0.00", "0.00", "0.04"}},
		{"all weights zero: even", "10.00", []string{"0", "0"}, []string{"5.00", "5.00"}},
		{"one part", "7.77", []string{"3"}, []string{"7.77"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := make([]decimal.Decimal, len(tc.weights))
			for i, s := range tc.weights {
				w[i] = d(s)
			}
			got := money.Allocate(d(tc.total), w)
			sum := decimal.Zero
			for i, g := range got {
				if g.StringFixed(2) != tc.want[i] {
					t.Fatalf("Allocate = %v, want %v", strs(got), tc.want)
				}
				sum = sum.Add(g)
			}
			if !sum.Equal(d(tc.total)) {
				t.Errorf("sum %s, want %s", sum, tc.total)
			}
		})
	}
	if money.Allocate(d("1"), nil) != nil || money.Even(d("1"), 0) != nil {
		t.Error("no parts must give nil")
	}
}

func TestEvenSixtyInstallments(t *testing.T) {
	got := money.Even(d("0.50"), 60)
	if got[0].Sign() != 0 || !got[59].Equal(d("0.50")) {
		t.Errorf("first %s last %s", got[0], got[59])
	}
}

func TestNumericRoundTrip(t *testing.T) {
	n := money.ToNumeric(d("1234.565"), money.AmountScale)
	if n.Exp != -2 || n.Int.Int64() != 123457 {
		t.Errorf("ToNumeric = %v e%d", n.Int, n.Exp)
	}
	if got := money.FromNumeric(n); !got.Equal(d("1234.57")) {
		t.Errorf("FromNumeric = %s", got)
	}
	if !money.FromNumeric(pgtype.Numeric{}).IsZero() {
		t.Error("NULL must be zero")
	}
	if got := money.FromFloat(0.1).Add(money.FromFloat(0.2)); !got.Equal(d("0.3")) {
		t.Errorf("0.1+0.2 = %s", got)
	}
	if _, err := money.Parse("x"); err == nil {
		t.Error("Parse must reject garbage")
	}
}

func TestJSONIsANumber(t *testing.T) {
	b, _ := json.Marshal(struct{ A decimal.Decimal }{d("12.50")})
	if string(b) != `{"A":12.5}` {
		t.Errorf("json = %s", b)
	}
	var v struct{ A decimal.Decimal }
	if err := json.Unmarshal([]byte(`{"A":0.1}`), &v); err != nil || !v.A.Equal(d("0.1")) {
		t.Errorf("unmarshal = %s %v", v.A, err)
	}
}
