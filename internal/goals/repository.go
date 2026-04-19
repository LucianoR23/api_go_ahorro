// Package goals: metas presupuestarias (category_limit / total_limit / savings).
// El cálculo de progreso lee expense_installments y incomes — no almacena
// snapshots: siempre vivo contra la DB.
package goals

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db"
	sqlcgen "github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

type CreateParams struct {
	HouseholdID  uuid.UUID
	Scope        string
	UserID       *uuid.UUID
	CategoryID   *uuid.UUID
	GoalType     string
	TargetAmount float64
	Currency     string
	Period       string
	IsActive     bool
}

func (r *Repository) Create(ctx context.Context, p CreateParams) (domain.BudgetGoal, error) {
	target, err := db.NumericFromFloat(p.TargetAmount, 2)
	if err != nil {
		return domain.BudgetGoal{}, err
	}
	row, err := r.q.CreateBudgetGoal(ctx, sqlcgen.CreateBudgetGoalParams{
		HouseholdID:  p.HouseholdID,
		Scope:        p.Scope,
		UserID:       p.UserID,
		CategoryID:   p.CategoryID,
		GoalType:     p.GoalType,
		TargetAmount: target,
		Currency:     p.Currency,
		Period:       p.Period,
		IsActive:     p.IsActive,
	})
	if err != nil {
		return domain.BudgetGoal{}, fmt.Errorf("goals.Create: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.BudgetGoal, error) {
	row, err := r.q.GetBudgetGoalByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BudgetGoal{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.BudgetGoal{}, fmt.Errorf("goals.GetByID: %w", err)
	}
	return toDomain(row), nil
}

type ListFilters struct {
	Scope      *string
	UserID     *uuid.UUID
	OnlyActive *bool
}

func (r *Repository) ListByHousehold(ctx context.Context, householdID uuid.UUID, f ListFilters) ([]domain.BudgetGoal, error) {
	rows, err := r.q.ListBudgetGoalsByHousehold(ctx, sqlcgen.ListBudgetGoalsByHouseholdParams{
		HouseholdID: householdID,
		Scope:       textPtr(f.Scope),
		UserID:      f.UserID,
		OnlyActive:  boolPtr(f.OnlyActive),
	})
	if err != nil {
		return nil, fmt.Errorf("goals.List: %w", err)
	}
	out := make([]domain.BudgetGoal, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

type UpdateParams struct {
	CategoryID   *uuid.UUID
	TargetAmount float64
	Currency     string
	Period       string
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, p UpdateParams) (domain.BudgetGoal, error) {
	target, err := db.NumericFromFloat(p.TargetAmount, 2)
	if err != nil {
		return domain.BudgetGoal{}, err
	}
	row, err := r.q.UpdateBudgetGoal(ctx, sqlcgen.UpdateBudgetGoalParams{
		ID:           id,
		CategoryID:   p.CategoryID,
		TargetAmount: target,
		Currency:     p.Currency,
		Period:       p.Period,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.BudgetGoal{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.BudgetGoal{}, fmt.Errorf("goals.Update: %w", err)
	}
	return toDomain(row), nil
}

func (r *Repository) SetActive(ctx context.Context, id uuid.UUID, active bool) error {
	if err := r.q.SetBudgetGoalActive(ctx, sqlcgen.SetBudgetGoalActiveParams{ID: id, IsActive: active}); err != nil {
		return fmt.Errorf("goals.SetActive: %w", err)
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.q.DeleteBudgetGoal(ctx, id); err != nil {
		return fmt.Errorf("goals.Delete: %w", err)
	}
	return nil
}

// ===================== progress queries =====================

func (r *Repository) SumHouseholdInstallments(ctx context.Context, householdID uuid.UUID, categoryID *uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumInstallmentsForHouseholdGoal(ctx, sqlcgen.SumInstallmentsForHouseholdGoalParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
		CategoryID:  categoryID,
	})
	if err != nil {
		return 0, fmt.Errorf("goals.SumHouseholdInstallments: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

func (r *Repository) SumUserInstallments(ctx context.Context, householdID, userID uuid.UUID, categoryID *uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumInstallmentsForUserGoal(ctx, sqlcgen.SumInstallmentsForUserGoalParams{
		HouseholdID: householdID,
		UserID:      userID,
		Column3:     pgtype.Date{Time: from, Valid: true},
		Column4:     pgtype.Date{Time: to, Valid: true},
		CategoryID:  categoryID,
	})
	if err != nil {
		return 0, fmt.Errorf("goals.SumUserInstallments: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

func (r *Repository) SumHouseholdIncomes(ctx context.Context, householdID uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumIncomesByHouseholdInRange(ctx, sqlcgen.SumIncomesByHouseholdInRangeParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("goals.SumHouseholdIncomes: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

func (r *Repository) SumUserIncomes(ctx context.Context, householdID, userID uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumIncomesByUserInRange(ctx, sqlcgen.SumIncomesByUserInRangeParams{
		HouseholdID: householdID,
		ReceivedBy:  userID,
		Column3:     pgtype.Date{Time: from, Valid: true},
		Column4:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("goals.SumUserIncomes: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

// ===================== mappers =====================

func toDomain(row sqlcgen.BudgetGoal) domain.BudgetGoal {
	return domain.BudgetGoal{
		ID:           row.ID,
		HouseholdID:  row.HouseholdID,
		Scope:        row.Scope,
		UserID:       row.UserID,
		CategoryID:   row.CategoryID,
		GoalType:     row.GoalType,
		TargetAmount: db.FloatFromNumeric(row.TargetAmount),
		Currency:     row.Currency,
		Period:       row.Period,
		IsActive:     row.IsActive,
		CreatedAt:    row.CreatedAt.Time,
	}
}

func textPtr(p *string) pgtype.Text {
	if p == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *p, Valid: true}
}

func boolPtr(p *bool) pgtype.Bool {
	if p == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *p, Valid: true}
}
