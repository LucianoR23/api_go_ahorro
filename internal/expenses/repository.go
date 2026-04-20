// Package expenses: core del dominio — alta/consulta/edición/borrado de
// gastos y sus cuotas y shares. Toda la escritura es transaccional
// (expense + installments + shares se arman juntos o nada).
package expenses

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// CreateBundle representa todo lo que un service.Create necesita persistir
// atómicamente: el expense + sus installments + los shares de cada uno.
type CreateBundle struct {
	Expense      domain.Expense
	Installments []InstallmentWithShares
}

// InstallmentWithShares: input a persistir. ID se asigna en la DB.
type InstallmentWithShares struct {
	Installment domain.ExpenseInstallment
	Shares      []domain.InstallmentShare
}

// CreateTx inserta expense + installments + shares en una sola tx.
// El service arma el bundle ya convertido/validado; acá solo escribimos.
func (r *Repository) CreateTx(ctx context.Context, b CreateBundle) (domain.ExpenseDetail, error) {
	var detail domain.ExpenseDetail

	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		qTx := r.q.WithTx(tx)

		amount, err := numericFromFloat(b.Expense.Amount, 2)
		if err != nil {
			return fmt.Errorf("amount inválido: %w", err)
		}
		amountBase, err := numericFromFloat(b.Expense.AmountBase, 2)
		if err != nil {
			return fmt.Errorf("amountBase inválido: %w", err)
		}
		rateUsed := pgtype.Numeric{}
		if b.Expense.RateUsed != nil {
			rateUsed, err = numericFromFloat(*b.Expense.RateUsed, 4)
			if err != nil {
				return fmt.Errorf("rateUsed inválido: %w", err)
			}
		}
		rateAt := pgtype.Timestamptz{}
		if b.Expense.RateAt != nil {
			rateAt = pgtype.Timestamptz{Time: *b.Expense.RateAt, Valid: true}
		}

		eRow, err := qTx.CreateExpense(ctx, sqlcgen.CreateExpenseParams{
			HouseholdID:        b.Expense.HouseholdID,
			CreatedBy:          b.Expense.CreatedBy,
			CategoryID:         b.Expense.CategoryID,
			PaymentMethodID:    b.Expense.PaymentMethodID,
			Amount:             amount,
			Currency:           b.Expense.Currency,
			AmountBase:         amountBase,
			BaseCurrency:       b.Expense.BaseCurrency,
			RateUsed:           rateUsed,
			RateAt:             rateAt,
			Description:        b.Expense.Description,
			SpentAt:            pgtype.Date{Time: b.Expense.SpentAt, Valid: true},
			Installments:       int32(b.Expense.Installments),
			IsShared:           b.Expense.IsShared,
			RecurringExpenseID: b.Expense.RecurringExpenseID,
		})
		if err != nil {
			return fmt.Errorf("insert expense: %w", err)
		}

		detail.Expense = expenseToDomain(eRow)

		for _, iw := range b.Installments {
			instAmount, err := numericFromFloat(iw.Installment.InstallmentAmount, 2)
			if err != nil {
				return fmt.Errorf("installment amount: %w", err)
			}
			instAmountBase, err := numericFromFloat(iw.Installment.InstallmentAmountBase, 2)
			if err != nil {
				return fmt.Errorf("installment amount_base: %w", err)
			}
			dueDate := pgtype.Date{}
			if iw.Installment.DueDate != nil {
				dueDate = pgtype.Date{Time: *iw.Installment.DueDate, Valid: true}
			}
			paidAt := pgtype.Timestamptz{}
			if iw.Installment.PaidAt != nil {
				paidAt = pgtype.Timestamptz{Time: *iw.Installment.PaidAt, Valid: true}
			}

			iRow, err := qTx.CreateInstallment(ctx, sqlcgen.CreateInstallmentParams{
				ExpenseID:             eRow.ID,
				InstallmentNumber:     int32(iw.Installment.InstallmentNumber),
				InstallmentAmount:     instAmount,
				InstallmentAmountBase: instAmountBase,
				BillingDate:           pgtype.Date{Time: iw.Installment.BillingDate, Valid: true},
				DueDate:               dueDate,
				IsPaid:                iw.Installment.IsPaid,
				PaidAt:                paidAt,
			})
			if err != nil {
				return fmt.Errorf("insert installment: %w", err)
			}

			installmentDomain := installmentToDomain(iRow)
			sharesDomain := make([]domain.InstallmentShare, 0, len(iw.Shares))
			for _, sh := range iw.Shares {
				owed, err := numericFromFloat(sh.AmountBaseOwed, 2)
				if err != nil {
					return fmt.Errorf("share amount: %w", err)
				}
				if err := qTx.CreateInstallmentShare(ctx, sqlcgen.CreateInstallmentShareParams{
					InstallmentID:  iRow.ID,
					UserID:         sh.UserID,
					AmountBaseOwed: owed,
				}); err != nil {
					return fmt.Errorf("insert share: %w", err)
				}
				sharesDomain = append(sharesDomain, domain.InstallmentShare{
					InstallmentID:  iRow.ID,
					UserID:         sh.UserID,
					AmountBaseOwed: sh.AmountBaseOwed,
				})
			}

			detail.Installments = append(detail.Installments, domain.InstallmentWithShares{
				Installment: installmentDomain,
				Shares:      sharesDomain,
			})
		}

		return nil
	})
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	return detail, nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.Expense, error) {
	row, err := r.q.GetExpenseByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Expense{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Expense{}, fmt.Errorf("expenses.GetByID: %w", err)
	}
	return expenseToDomain(row), nil
}

// GetDetail: expense + installments + shares. 3 queries, agrupamos en Go.
func (r *Repository) GetDetail(ctx context.Context, id uuid.UUID) (domain.ExpenseDetail, error) {
	e, err := r.GetByID(ctx, id)
	if err != nil {
		return domain.ExpenseDetail{}, err
	}
	installmentRows, err := r.q.ListInstallmentsByExpense(ctx, id)
	if err != nil {
		return domain.ExpenseDetail{}, fmt.Errorf("expenses.GetDetail.installments: %w", err)
	}
	shareRows, err := r.q.ListSharesByExpense(ctx, id)
	if err != nil {
		return domain.ExpenseDetail{}, fmt.Errorf("expenses.GetDetail.shares: %w", err)
	}

	// Agrupamos shares por installment_id.
	byInst := make(map[uuid.UUID][]domain.InstallmentShare, len(installmentRows))
	for _, s := range shareRows {
		byInst[s.InstallmentID] = append(byInst[s.InstallmentID], domain.InstallmentShare{
			InstallmentID:  s.InstallmentID,
			UserID:         s.UserID,
			AmountBaseOwed: floatFromNumeric(s.AmountBaseOwed),
		})
	}

	detail := domain.ExpenseDetail{Expense: e}
	for _, ir := range installmentRows {
		detail.Installments = append(detail.Installments, domain.InstallmentWithShares{
			Installment: installmentToDomain(ir),
			Shares:      byInst[ir.ID],
		})
	}
	return detail, nil
}

// ListFilter: filtros opcionales para listados paginados.
type ListFilter struct {
	CategoryID      *uuid.UUID
	PaymentMethodID *uuid.UUID
	FromDate        *time.Time
	ToDate          *time.Time
	Limit           int32
	Offset          int32
}

func (r *Repository) List(ctx context.Context, householdID uuid.UUID, f ListFilter) ([]domain.Expense, int64, error) {
	from := pgtype.Date{}
	if f.FromDate != nil {
		from = pgtype.Date{Time: *f.FromDate, Valid: true}
	}
	to := pgtype.Date{}
	if f.ToDate != nil {
		to = pgtype.Date{Time: *f.ToDate, Valid: true}
	}

	rows, err := r.q.ListExpensesByHousehold(ctx, sqlcgen.ListExpensesByHouseholdParams{
		HouseholdID:     householdID,
		Limit:           f.Limit,
		Offset:          f.Offset,
		CategoryID:      f.CategoryID,
		PaymentMethodID: f.PaymentMethodID,
		FromDate:        from,
		ToDate:          to,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("expenses.List: %w", err)
	}
	total, err := r.q.CountExpensesByHousehold(ctx, sqlcgen.CountExpensesByHouseholdParams{
		HouseholdID:     householdID,
		CategoryID:      f.CategoryID,
		PaymentMethodID: f.PaymentMethodID,
		FromDate:        from,
		ToDate:          to,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("expenses.Count: %w", err)
	}
	out := make([]domain.Expense, len(rows))
	for i, row := range rows {
		out[i] = expenseToDomain(row)
	}
	return out, total, nil
}

// UpdateMeta actualiza solo description/spent_at/category_id.
// Editar amount/currency/installments obligaría a recomputar todo
// (shares, periods, conversion) y es mejor borrar y recrear.
func (r *Repository) UpdateMeta(ctx context.Context, id uuid.UUID, description string, spentAt time.Time, categoryID *uuid.UUID) (domain.Expense, error) {
	row, err := r.q.UpdateExpense(ctx, sqlcgen.UpdateExpenseParams{
		ID:          id,
		Description: description,
		SpentAt:     pgtype.Date{Time: spentAt, Valid: true},
		CategoryID:  categoryID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Expense{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Expense{}, fmt.Errorf("expenses.UpdateMeta: %w", err)
	}
	return expenseToDomain(row), nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.q.DeleteExpense(ctx, id); err != nil {
		return fmt.Errorf("expenses.Delete: %w", err)
	}
	return nil
}

// GetInstallmentByNumber: usado en el PATCH de installments (CP6.5).
func (r *Repository) GetInstallmentByNumber(ctx context.Context, expenseID uuid.UUID, n int) (domain.ExpenseInstallment, error) {
	row, err := r.q.GetInstallmentByExpenseAndNumber(ctx, sqlcgen.GetInstallmentByExpenseAndNumberParams{
		ExpenseID:         expenseID,
		InstallmentNumber: int32(n),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExpenseInstallment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ExpenseInstallment{}, fmt.Errorf("expenses.GetInstallment: %w", err)
	}
	return installmentToDomain(row), nil
}

func (r *Repository) UpdateInstallmentDates(ctx context.Context, id uuid.UUID, billing time.Time, due *time.Time) (domain.ExpenseInstallment, error) {
	dueDate := pgtype.Date{}
	if due != nil {
		dueDate = pgtype.Date{Time: *due, Valid: true}
	}
	row, err := r.q.UpdateInstallmentDates(ctx, sqlcgen.UpdateInstallmentDatesParams{
		ID:          id,
		BillingDate: pgtype.Date{Time: billing, Valid: true},
		DueDate:     dueDate,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExpenseInstallment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ExpenseInstallment{}, fmt.Errorf("expenses.UpdateInstallmentDates: %w", err)
	}
	return installmentToDomain(row), nil
}

func (r *Repository) SetInstallmentPaid(ctx context.Context, id uuid.UUID, paid bool) (domain.ExpenseInstallment, error) {
	row, err := r.q.SetInstallmentPaid(ctx, sqlcgen.SetInstallmentPaidParams{
		ID:     id,
		IsPaid: paid,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ExpenseInstallment{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.ExpenseInstallment{}, fmt.Errorf("expenses.SetInstallmentPaid: %w", err)
	}
	return installmentToDomain(row), nil
}

// ===================== mappers =====================

func expenseToDomain(e sqlcgen.Expense) domain.Expense {
	var rateUsed *float64
	if e.RateUsed.Valid {
		v := floatFromNumeric(e.RateUsed)
		rateUsed = &v
	}
	var rateAt *time.Time
	if e.RateAt.Valid {
		t := e.RateAt.Time
		rateAt = &t
	}
	return domain.Expense{
		ID:              e.ID,
		HouseholdID:     e.HouseholdID,
		CreatedBy:       e.CreatedBy,
		CategoryID:      e.CategoryID,
		PaymentMethodID: e.PaymentMethodID,
		Amount:          floatFromNumeric(e.Amount),
		Currency:        e.Currency,
		AmountBase:      floatFromNumeric(e.AmountBase),
		BaseCurrency:    e.BaseCurrency,
		RateUsed:        rateUsed,
		RateAt:          rateAt,
		Description:     e.Description,
		SpentAt:         e.SpentAt.Time,
		Installments:    int(e.Installments),
		IsShared:        e.IsShared,
		RecurringExpenseID: e.RecurringExpenseID,
		CreatedAt:       e.CreatedAt.Time,
		UpdatedAt:       e.UpdatedAt.Time,
	}
}

func installmentToDomain(i sqlcgen.ExpenseInstallment) domain.ExpenseInstallment {
	var due *time.Time
	if i.DueDate.Valid {
		t := i.DueDate.Time
		due = &t
	}
	var paidAt *time.Time
	if i.PaidAt.Valid {
		t := i.PaidAt.Time
		paidAt = &t
	}
	return domain.ExpenseInstallment{
		ID:                    i.ID,
		ExpenseID:             i.ExpenseID,
		InstallmentNumber:     int(i.InstallmentNumber),
		InstallmentAmount:     floatFromNumeric(i.InstallmentAmount),
		InstallmentAmountBase: floatFromNumeric(i.InstallmentAmountBase),
		BillingDate:           i.BillingDate.Time,
		DueDate:               due,
		IsPaid:                i.IsPaid,
		PaidAt:                paidAt,
		CreatedAt:             i.CreatedAt.Time,
		UpdatedAt:             i.UpdatedAt.Time,
	}
}
