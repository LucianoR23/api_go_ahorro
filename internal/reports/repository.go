// Package reports: agregaciones para el resumen mensual, trends y export a IA.
// Todo read-only — no tiene tablas propias, consume de expenses/installments/incomes.
package reports

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db"
	sqlcgen "github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
)

type Repository struct {
	q *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{q: sqlcgen.New(pool)}
}

// SumSpentAt: total gastado en el rango (por spent_at).
func (r *Repository) SumSpentAt(ctx context.Context, householdID uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumExpensesSpentAtForReport(ctx, sqlcgen.SumExpensesSpentAtForReportParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("reports.SumSpentAt: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

// SumBilled: total resumido en tarjetas en el rango (por billing_date).
func (r *Repository) SumBilled(ctx context.Context, householdID uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumInstallmentsBilledForReport(ctx, sqlcgen.SumInstallmentsBilledForReportParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("reports.SumBilled: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

// SumDue: total a pagar en el rango (COALESCE due/billing).
func (r *Repository) SumDue(ctx context.Context, householdID uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumInstallmentsDueForReport(ctx, sqlcgen.SumInstallmentsDueForReportParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("reports.SumDue: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

// CategoryBreakdown: total por categoría en el rango.
type CategoryRow struct {
	CategoryID   *uuid.UUID
	CategoryName string // "" si NULL
	Total        float64
	TxCount      int64
}

func (r *Repository) CategoryBreakdown(ctx context.Context, householdID uuid.UUID, from, to time.Time) ([]CategoryRow, error) {
	rows, err := r.q.SumExpensesByCategoryInRange(ctx, sqlcgen.SumExpensesByCategoryInRangeParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("reports.CategoryBreakdown: %w", err)
	}
	out := make([]CategoryRow, len(rows))
	for i, rr := range rows {
		out[i] = CategoryRow{
			CategoryID:   rr.CategoryID,
			CategoryName: rr.CategoryName,
			Total:        db.FloatFromNumeric(rr.TotalBase),
			TxCount:      rr.TxCount,
		}
	}
	return out, nil
}

// FixedVariable: split entre gastos recurrentes (fijos) y manuales (variables).
type FixedVariableSplit struct {
	FixedTotal    float64
	VariableTotal float64
	FixedCount    int64
	VariableCount int64
}

func (r *Repository) FixedVariable(ctx context.Context, householdID uuid.UUID, from, to time.Time) (FixedVariableSplit, error) {
	row, err := r.q.SumExpensesFixedVariableInRange(ctx, sqlcgen.SumExpensesFixedVariableInRangeParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return FixedVariableSplit{}, fmt.Errorf("reports.FixedVariable: %w", err)
	}
	return FixedVariableSplit{
		FixedTotal:    db.FloatFromNumeric(row.FixedTotal),
		VariableTotal: db.FloatFromNumeric(row.VariableTotal),
		FixedCount:    row.FixedCount,
		VariableCount: row.VariableCount,
	}, nil
}

// MonthRow: una fila de serie mensual.
type MonthRow struct {
	Month   time.Time
	Total   float64
	TxCount int64
}

func (r *Repository) SpentByMonth(ctx context.Context, householdID uuid.UUID, from, to time.Time) ([]MonthRow, error) {
	rows, err := r.q.SumExpensesSpentAtByMonth(ctx, sqlcgen.SumExpensesSpentAtByMonthParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("reports.SpentByMonth: %w", err)
	}
	out := make([]MonthRow, len(rows))
	for i, rr := range rows {
		out[i] = MonthRow{Month: rr.Month.Time, Total: db.FloatFromNumeric(rr.TotalBase), TxCount: rr.TxCount}
	}
	return out, nil
}

func (r *Repository) DueByMonth(ctx context.Context, householdID uuid.UUID, from, to time.Time) ([]MonthRow, error) {
	rows, err := r.q.SumInstallmentsDueByMonth(ctx, sqlcgen.SumInstallmentsDueByMonthParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("reports.DueByMonth: %w", err)
	}
	out := make([]MonthRow, len(rows))
	for i, rr := range rows {
		out[i] = MonthRow{Month: rr.Month.Time, Total: db.FloatFromNumeric(rr.TotalBase)}
	}
	return out, nil
}

func (r *Repository) IncomesByMonth(ctx context.Context, householdID uuid.UUID, from, to time.Time) ([]MonthRow, error) {
	rows, err := r.q.SumIncomesReceivedByMonth(ctx, sqlcgen.SumIncomesReceivedByMonthParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("reports.IncomesByMonth: %w", err)
	}
	out := make([]MonthRow, len(rows))
	for i, rr := range rows {
		out[i] = MonthRow{Month: rr.Month.Time, Total: db.FloatFromNumeric(rr.TotalBase)}
	}
	return out, nil
}
