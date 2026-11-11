// Package exports turns a space's movements into Excel files (plan 11.1).
// Read-only: no tables of its own.
package exports

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/money"
	"github.com/LucianoR23/valuo_api/internal/xlsx"
)

// MaxDays is the longest period one file covers (a leap year).
const MaxDays = 366

// MaxRows caps one file. A year of a household is far below it; the cap
// keeps a runaway space from building a huge file in memory.
const MaxRows = 20000

// Querier is the slice of sqlc this package reads.
type Querier interface {
	ListExpensesForExport(ctx context.Context, arg sqlcgen.ListExpensesForExportParams) ([]sqlcgen.ListExpensesForExportRow, error)
	ListIncomesForExport(ctx context.Context, arg sqlcgen.ListIncomesForExportParams) ([]sqlcgen.ListIncomesForExportRow, error)
}

type Service struct {
	q Querier
}

func NewService(q Querier) *Service { return &Service{q: q} }

// Period is the inclusive date range of a file.
type Period struct {
	From, To time.Time
}

// Validate checks the range is in order and at most MaxDays long.
func (p Period) Validate() error {
	if p.To.Before(p.From) {
		return domain.NewValidationError("to", "no puede ser anterior a from")
	}
	if days := int(p.To.Sub(p.From).Hours()/24) + 1; days > MaxDays {
		return domain.NewValidationError("to", fmt.Sprintf("el período no puede pasar de %d días", MaxDays))
	}
	return nil
}

// Filename names the file after its period: gastos-2026-11.xlsx for a whole
// month, gastos-2026.xlsx for a whole year, else both dates.
func (p Period) Filename(prefix string) string {
	f, t := p.From, p.To
	lastOfMonth := t.AddDate(0, 0, 1).Day() == 1
	switch {
	case f.Day() == 1 && f.Month() == time.January && t.Month() == time.December && t.Day() == 31 && f.Year() == t.Year():
		return fmt.Sprintf("%s-%d.xlsx", prefix, f.Year())
	case f.Day() == 1 && lastOfMonth && f.Year() == t.Year() && f.Month() == t.Month():
		return fmt.Sprintf("%s-%s.xlsx", prefix, f.Format("2006-01"))
	default:
		return fmt.Sprintf("%s-%s-a-%s.xlsx", prefix, f.Format("2006-01-02"), t.Format("2006-01-02"))
	}
}

// ExpenseFilter mirrors the filters of GET /expenses.
type ExpenseFilter struct {
	Period
	CategoryID      *uuid.UUID
	PaymentMethodID *uuid.UUID
	// Kind: "", "fixed" or "variable".
	Kind string
	Q    string
}

var errTooMany = domain.NewValidationError("to", "son demasiados movimientos para un archivo: elegí un período más corto")

func date(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

// Expenses builds the confirmed expenses of the space as a sheet.
func (s *Service) Expenses(ctx context.Context, householdID, viewerID uuid.UUID, f ExpenseFilter) (xlsx.Sheet, error) {
	if err := f.Validate(); err != nil {
		return xlsx.Sheet{}, err
	}
	params := sqlcgen.ListExpensesForExportParams{
		ViewerID:        viewerID,
		HouseholdID:     householdID,
		FromDate:        date(f.From),
		ToDate:          date(f.To),
		CategoryID:      f.CategoryID,
		PaymentMethodID: f.PaymentMethodID,
		RowLimit:        MaxRows + 1,
	}
	switch f.Kind {
	case "fixed":
		params.Fixed = pgtype.Bool{Bool: true, Valid: true}
	case "variable":
		params.Fixed = pgtype.Bool{Bool: false, Valid: true}
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		esc := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(q)
		params.Q = pgtype.Text{String: esc, Valid: true}
	}
	rows, err := s.q.ListExpensesForExport(ctx, params)
	if err != nil {
		return xlsx.Sheet{}, fmt.Errorf("exports.Expenses: %w", err)
	}
	if len(rows) > MaxRows {
		return xlsx.Sheet{}, errTooMany
	}

	sheet := xlsx.Sheet{
		Name: "Gastos",
		Columns: []xlsx.Column{
			{Header: "Fecha", Width: 11},
			{Header: "Descripción", Width: 32},
			{Header: "Categoría", Width: 24},
			{Header: "Medio de pago", Width: 20},
			{Header: "Monto", Width: 14},
			{Header: "Moneda", Width: 8},
			{Header: "Cotización", Width: 11},
			{Header: "Monto en pesos", Width: 15},
			{Header: "Cuotas", Width: 8},
			{Header: "Tipo", Width: 10},
			{Header: "Compartido", Width: 11},
			{Header: "Cargó", Width: 14},
		},
		Rows: make([][]xlsx.Cell, 0, len(rows)),
	}
	for _, r := range rows {
		sheet.Rows = append(sheet.Rows, []xlsx.Cell{
			xlsx.Date(r.SpentAt.Time),
			xlsx.Text(r.Description),
			xlsx.Text(categoryLabel(r.CategoryName, r.ParentCategoryName)),
			xlsx.Text(r.PaymentMethodName),
			xlsx.Money(money.FromNumeric(r.Amount)),
			xlsx.Text(r.Currency),
			rateCell(r.RateUsed),
			xlsx.Money(money.FromNumeric(r.AmountBase)),
			xlsx.Int(int64(r.Installments)),
			xlsx.Text(pick(r.IsFixed, "Fijo", "Variable")),
			xlsx.Text(pick(r.IsShared, "Sí", "No")),
			xlsx.Text(r.CreatedByName),
		})
	}
	return sheet, nil
}

// Incomes builds the space's incomes as a sheet.
func (s *Service) Incomes(ctx context.Context, householdID, viewerID uuid.UUID, p Period) (xlsx.Sheet, error) {
	if err := p.Validate(); err != nil {
		return xlsx.Sheet{}, err
	}
	rows, err := s.q.ListIncomesForExport(ctx, sqlcgen.ListIncomesForExportParams{
		ViewerID:    viewerID,
		HouseholdID: householdID,
		FromDate:    date(p.From),
		ToDate:      date(p.To),
		RowLimit:    MaxRows + 1,
	})
	if err != nil {
		return xlsx.Sheet{}, fmt.Errorf("exports.Incomes: %w", err)
	}
	if len(rows) > MaxRows {
		return xlsx.Sheet{}, errTooMany
	}

	sheet := xlsx.Sheet{
		Name: "Ingresos",
		Columns: []xlsx.Column{
			{Header: "Fecha", Width: 11},
			{Header: "Descripción", Width: 32},
			{Header: "Tipo", Width: 12},
			{Header: "Medio", Width: 20},
			{Header: "Monto", Width: 14},
			{Header: "Moneda", Width: 8},
			{Header: "Cotización", Width: 11},
			{Header: "Monto en pesos", Width: 15},
			{Header: "Recibió", Width: 14},
		},
		Rows: make([][]xlsx.Cell, 0, len(rows)),
	}
	for _, r := range rows {
		sheet.Rows = append(sheet.Rows, []xlsx.Cell{
			xlsx.Date(r.ReceivedAt.Time),
			xlsx.Text(r.Description),
			xlsx.Text(incomeSourceLabel(r.Source)),
			xlsx.Text(r.PaymentMethodName),
			xlsx.Money(money.FromNumeric(r.Amount)),
			xlsx.Text(r.Currency),
			rateCell(r.RateUsed),
			xlsx.Money(money.FromNumeric(r.AmountBase)),
			xlsx.Text(r.ReceivedByName),
		})
	}
	return sheet, nil
}

// categoryLabel: "Servicios > Luz" for a subcategory.
func categoryLabel(name, parent pgtype.Text) string {
	switch {
	case !name.Valid:
		return "Sin categoría"
	case parent.Valid:
		return parent.String + " > " + name.String
	default:
		return name.String
	}
}

// rateCell is blank for movements in the base currency (no rate).
func rateCell(n pgtype.Numeric) xlsx.Cell {
	if !n.Valid {
		return xlsx.Empty()
	}
	d := money.FromNumeric(n)
	if d.Equal(decimal.Zero) {
		return xlsx.Empty()
	}
	return xlsx.Money(d)
}

func pick(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

// incomeSourceLabels match the app's labels (src/lib/labels.ts).
var incomeSourceLabels = map[string]string{
	"salary":     "Salario",
	"freelance":  "Freelance",
	"gift":       "Regalo",
	"investment": "Inversión",
	"refund":     "Reembolso",
	"other":      "Otro",
}

func incomeSourceLabel(s string) string {
	if l, ok := incomeSourceLabels[s]; ok {
		return l
	}
	return s
}
