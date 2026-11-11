package exports

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestPeriodFilename(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{"2026-11-01", "2026-11-30", "gastos-2026-11.xlsx"},
		{"2028-02-01", "2028-02-29", "gastos-2028-02.xlsx"},
		{"2026-01-01", "2026-12-31", "gastos-2026.xlsx"},
		{"2026-11-01", "2026-11-29", "gastos-2026-11-01-a-2026-11-29.xlsx"},
		{"2026-03-01", "2026-10-31", "gastos-2026-03-01-a-2026-10-31.xlsx"},
		{"2026-11-15", "2026-11-15", "gastos-2026-11-15-a-2026-11-15.xlsx"},
	}
	for _, c := range cases {
		if got := (Period{From: day(c.from), To: day(c.to)}).Filename("gastos"); got != c.want {
			t.Errorf("%s..%s = %q, want %q", c.from, c.to, got, c.want)
		}
	}
}

func TestPeriodValidate(t *testing.T) {
	ok := []Period{
		{From: day("2026-11-01"), To: day("2026-11-01")},
		{From: day("2028-01-01"), To: day("2028-12-31")}, // 366 days
	}
	for _, p := range ok {
		if err := p.Validate(); err != nil {
			t.Errorf("%v: %v", p, err)
		}
	}
	bad := []Period{
		{From: day("2026-11-02"), To: day("2026-11-01")},
		{From: day("2026-01-01"), To: day("2027-01-02")}, // 367 days
	}
	for _, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("%v: want an error", p)
		}
	}
}

func TestCategoryLabel(t *testing.T) {
	text := func(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
	if got := categoryLabel(pgtype.Text{}, pgtype.Text{}); got != "Sin categoría" {
		t.Errorf("none = %q", got)
	}
	if got := categoryLabel(text("Luz"), pgtype.Text{}); got != "Luz" {
		t.Errorf("top = %q", got)
	}
	if got := categoryLabel(text("Luz"), text("Servicios")); got != "Servicios > Luz" {
		t.Errorf("sub = %q", got)
	}
}

func TestIncomeSourceLabel(t *testing.T) {
	if incomeSourceLabel("salary") != "Salario" || incomeSourceLabel("refund") != "Reembolso" || incomeSourceLabel("new") != "new" {
		t.Error("labels")
	}
}
