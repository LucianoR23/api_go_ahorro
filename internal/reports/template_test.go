package reports_test

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/reports"
)

// Plan 1.10c: the monthly email writes amounts es-AR, like the app.
func TestRenderMonthlyHTMLMoney(t *testing.T) {
	html, err := reports.RenderMonthlyHTML("Valuo", "Casa", reports.MonthlyReport{
		BaseCurrency: "ARS",
		Month:        "2026-10",
		Spent:        decimal.RequireFromString("19450.00"),
		Billed:       decimal.RequireFromString("1050.5"),
		Due:          decimal.Zero,
		ByCategory: []reports.CategoryBreakdownItem{
			{CategoryName: "Súper", Total: decimal.RequireFromString("1234567.89"), Pct: 50},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"$ 19.450", "$ 1.050,50", "$ 1.234.567,89"} {
		if !strings.Contains(html, want) {
			t.Errorf("email has no %q", want)
		}
	}
	if strings.Contains(html, "19450.00") || strings.Contains(html, "1050.50 ARS") {
		t.Error("email still has the old amount format")
	}
}
