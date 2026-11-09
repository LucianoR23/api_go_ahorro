// Package statements: credit card statements (plan 4.1). A statement is the
// installments of a card billed in a closing month plus manual adjustments.
// Totals are computed on every read; the tables keep only what the user
// enters (minimum payment, payment, adjustments).
package statements

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/money"
)

type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// periodSum: one currency of one closing month.
type periodSum struct {
	PeriodYM string
	Currency string
	Total    decimal.Decimal
	Count    int
}

func date(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

func (r *Repository) PeriodOverrides(ctx context.Context, ccID uuid.UUID, fromYM, toYM string) (map[string]domain.CreditCardPeriod, error) {
	rows, err := r.q.ListCreditCardPeriodsInRange(ctx, sqlcgen.ListCreditCardPeriodsInRangeParams{
		CreditCardID: ccID, FromYm: fromYM, ToYm: toYM,
	})
	if err != nil {
		return nil, fmt.Errorf("statements.PeriodOverrides: %w", err)
	}
	out := make(map[string]domain.CreditCardPeriod, len(rows))
	for _, p := range rows {
		out[p.PeriodYm] = domain.CreditCardPeriod{
			CreditCardID: p.CreditCardID, PeriodYM: p.PeriodYm,
			ClosingDate: p.ClosingDate.Time, DueDate: p.DueDate.Time,
		}
	}
	return out, nil
}

func (r *Repository) InstallmentSums(ctx context.Context, pmID uuid.UUID, from, to time.Time) ([]periodSum, error) {
	rows, err := r.q.SumCardInstallmentsByPeriod(ctx, sqlcgen.SumCardInstallmentsByPeriodParams{
		PaymentMethodID: pmID, FromDate: date(from), ToDate: date(to),
	})
	if err != nil {
		return nil, fmt.Errorf("statements.InstallmentSums: %w", err)
	}
	out := make([]periodSum, len(rows))
	for i, row := range rows {
		out[i] = periodSum{PeriodYM: row.PeriodYm, Currency: row.Currency, Total: money.FromNumeric(row.Total), Count: int(row.InstallmentCount)}
	}
	return out, nil
}

func (r *Repository) AdjustmentSums(ctx context.Context, ccID uuid.UUID, fromYM, toYM string) ([]periodSum, error) {
	rows, err := r.q.SumCardAdjustmentsByPeriod(ctx, sqlcgen.SumCardAdjustmentsByPeriodParams{
		CreditCardID: ccID, FromYm: fromYM, ToYm: toYM,
	})
	if err != nil {
		return nil, fmt.Errorf("statements.AdjustmentSums: %w", err)
	}
	out := make([]periodSum, len(rows))
	for i, row := range rows {
		out[i] = periodSum{PeriodYM: row.PeriodYm, Currency: row.Currency, Total: money.FromNumeric(row.Total)}
	}
	return out, nil
}

func (r *Repository) Records(ctx context.Context, ccID uuid.UUID, fromYM, toYM string) (map[string]domain.StatementRecord, error) {
	rows, err := r.q.ListCardStatementRecords(ctx, sqlcgen.ListCardStatementRecordsParams{
		CreditCardID: ccID, FromYm: fromYM, ToYm: toYM,
	})
	if err != nil {
		return nil, fmt.Errorf("statements.Records: %w", err)
	}
	out := make(map[string]domain.StatementRecord, len(rows))
	for _, row := range rows {
		rec := domain.StatementRecord{}
		if row.MinimumPayment.Valid {
			m := money.FromNumeric(row.MinimumPayment)
			rec.MinimumPayment = &m
		}
		if row.PaidAt.Valid {
			at, amt, cur := row.PaidAt.Time, money.FromNumeric(row.PaidAmount), row.PaidCurrency.String
			rec.PaidAt, rec.PaidAmount, rec.PaidCurrency = &at, &amt, &cur
		}
		out[row.PeriodYm] = rec
	}
	return out, nil
}

func (r *Repository) Installments(ctx context.Context, pmID uuid.UUID, from, to time.Time) ([]domain.StatementInstallment, error) {
	rows, err := r.q.ListCardStatementInstallments(ctx, sqlcgen.ListCardStatementInstallmentsParams{
		PaymentMethodID: pmID, FromDate: date(from), ToDate: date(to),
	})
	if err != nil {
		return nil, fmt.Errorf("statements.Installments: %w", err)
	}
	out := make([]domain.StatementInstallment, len(rows))
	for i, row := range rows {
		out[i] = domain.StatementInstallment{
			InstallmentID: row.InstallmentID, ExpenseID: row.ExpenseID, HouseholdID: row.HouseholdID,
			CategoryID: row.CategoryID, Description: row.Description, SpentAt: row.SpentAt.Time,
			InstallmentNumber: int(row.InstallmentNumber), TotalInstallments: int(row.TotalInstallments),
			Amount: money.FromNumeric(row.InstallmentAmount), Currency: row.Currency, IsPaid: row.IsPaid,
		}
	}
	return out, nil
}

func (r *Repository) Adjustments(ctx context.Context, ccID uuid.UUID, ym string) ([]domain.StatementAdjustment, error) {
	rows, err := r.q.ListCardStatementAdjustments(ctx, sqlcgen.ListCardStatementAdjustmentsParams{CreditCardID: ccID, PeriodYm: ym})
	if err != nil {
		return nil, fmt.Errorf("statements.Adjustments: %w", err)
	}
	out := make([]domain.StatementAdjustment, len(rows))
	for i, row := range rows {
		out[i] = toAdjustment(row)
	}
	return out, nil
}

func toAdjustment(row sqlcgen.CreditCardStatementAdjustment) domain.StatementAdjustment {
	return domain.StatementAdjustment{
		ID: row.ID, Kind: domain.AdjustmentKind(row.Kind), Description: row.Description,
		Amount: money.FromNumeric(row.Amount), Currency: row.Currency, CreatedAt: row.CreatedAt.Time,
	}
}

func (r *Repository) SetMinimum(ctx context.Context, ccID uuid.UUID, ym string, minimum *decimal.Decimal) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if err := q.EnsureCardStatement(ctx, sqlcgen.EnsureCardStatementParams{CreditCardID: ccID, PeriodYm: ym}); err != nil {
			return fmt.Errorf("statements.SetMinimum ensure: %w", err)
		}
		n := pgtype.Numeric{}
		if minimum != nil {
			n = money.ToNumeric(*minimum, money.AmountScale)
		}
		if err := q.SetCardStatementMinimum(ctx, sqlcgen.SetCardStatementMinimumParams{CreditCardID: ccID, PeriodYm: ym, MinimumPayment: n}); err != nil {
			return fmt.Errorf("statements.SetMinimum: %w", err)
		}
		return nil
	})
}

// payment: what "Marcar como pagado" records; nil clears it.
type payment struct {
	Amount   decimal.Decimal
	Currency string
	At       time.Time
}

// SetPayment records or clears a statement's payment and marks its
// installments paid or unpaid, in one transaction.
func (r *Repository) SetPayment(ctx context.Context, cc domain.CreditCard, ym string, from, to time.Time, p *payment) error {
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if err := q.EnsureCardStatement(ctx, sqlcgen.EnsureCardStatementParams{CreditCardID: cc.ID, PeriodYm: ym}); err != nil {
			return fmt.Errorf("statements.SetPayment ensure: %w", err)
		}
		params := sqlcgen.SetCardStatementPaymentParams{CreditCardID: cc.ID, PeriodYm: ym}
		if p != nil {
			params.PaidAt = pgtype.Timestamptz{Time: p.At, Valid: true}
			params.PaidAmount = money.ToNumeric(p.Amount, money.AmountScale)
			params.PaidCurrency = pgtype.Text{String: p.Currency, Valid: true}
		}
		if err := q.SetCardStatementPayment(ctx, params); err != nil {
			return fmt.Errorf("statements.SetPayment: %w", err)
		}
		if _, err := q.SetCardStatementInstallmentsPaid(ctx, sqlcgen.SetCardStatementInstallmentsPaidParams{
			IsPaid: p != nil, PaymentMethodID: cc.PaymentMethodID, FromDate: date(from), ToDate: date(to),
		}); err != nil {
			return fmt.Errorf("statements.SetPayment installments: %w", err)
		}
		return nil
	})
}

type adjustmentFields struct {
	Kind        domain.AdjustmentKind
	Description string
	Amount      decimal.Decimal
	Currency    string
}

func (r *Repository) CreateAdjustment(ctx context.Context, ccID uuid.UUID, ym string, f adjustmentFields) (domain.StatementAdjustment, error) {
	var out domain.StatementAdjustment
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if err := q.EnsureCardStatement(ctx, sqlcgen.EnsureCardStatementParams{CreditCardID: ccID, PeriodYm: ym}); err != nil {
			return fmt.Errorf("statements.CreateAdjustment ensure: %w", err)
		}
		row, err := q.CreateCardStatementAdjustment(ctx, sqlcgen.CreateCardStatementAdjustmentParams{
			CreditCardID: ccID, PeriodYm: ym, Kind: string(f.Kind), Description: f.Description,
			Amount: money.ToNumeric(f.Amount, money.AmountScale), Currency: f.Currency,
		})
		if err != nil {
			return fmt.Errorf("statements.CreateAdjustment: %w", err)
		}
		out = toAdjustment(row)
		return nil
	})
	return out, err
}

func (r *Repository) UpdateAdjustment(ctx context.Context, ccID uuid.UUID, ym string, id uuid.UUID, f adjustmentFields) (domain.StatementAdjustment, error) {
	row, err := r.q.UpdateCardStatementAdjustment(ctx, sqlcgen.UpdateCardStatementAdjustmentParams{
		ID: id, CreditCardID: ccID, PeriodYm: ym, Kind: string(f.Kind), Description: f.Description,
		Amount: money.ToNumeric(f.Amount, money.AmountScale), Currency: f.Currency,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.StatementAdjustment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.StatementAdjustment{}, fmt.Errorf("statements.UpdateAdjustment: %w", err)
	}
	return toAdjustment(row), nil
}

func (r *Repository) DeleteAdjustment(ctx context.Context, ccID uuid.UUID, ym string, id uuid.UUID) error {
	n, err := r.q.DeleteCardStatementAdjustment(ctx, sqlcgen.DeleteCardStatementAdjustmentParams{ID: id, CreditCardID: ccID, PeriodYm: ym})
	if err != nil {
		return fmt.Errorf("statements.DeleteAdjustment: %w", err)
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *Repository) InstallmentPlans(ctx context.Context, pmID uuid.UUID, from time.Time) ([]domain.InstallmentPlan, error) {
	rows, err := r.q.ListCardInstallmentPlans(ctx, sqlcgen.ListCardInstallmentPlansParams{PaymentMethodID: pmID, FromDate: date(from)})
	if err != nil {
		return nil, fmt.Errorf("statements.InstallmentPlans: %w", err)
	}
	out := make([]domain.InstallmentPlan, len(rows))
	for i, row := range rows {
		out[i] = domain.InstallmentPlan{
			ExpenseID: row.ExpenseID, HouseholdID: row.HouseholdID, CategoryID: row.CategoryID,
			Description: row.Description, Currency: row.Currency, TotalInstallments: int(row.TotalInstallments),
			NextInstallmentNumber: int(row.NextInstallmentNumber), NextInstallmentAmount: money.FromNumeric(row.NextInstallmentAmount),
			LastPeriodYM: domain.PeriodYMFromDate(row.LastBillingDate.Time),
		}
	}
	return out, nil
}
