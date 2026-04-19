// Package insights: genera y expone insights deterministas diarios. Apunta
// a que el usuario abra la app y vea "ayer gastaste X", "este mes te cobran Y",
// "tu goal de comida está al 85%". Sin ML — reglas simples sobre agregados.
package insights

import (
	"context"
	"encoding/json"
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

// ===================== insights CRUD =====================

type CreateParams struct {
	HouseholdID uuid.UUID
	UserID      *uuid.UUID
	InsightDate time.Time
	InsightType string
	Title       string
	Body        string
	Severity    string
	Metadata    map[string]any
}

// Create: devuelve (insight, created). created=false cuando ON CONFLICT
// DO NOTHING se activó (ya existía el mismo tipo para ese día).
func (r *Repository) Create(ctx context.Context, p CreateParams) (domain.DailyInsight, bool, error) {
	meta := []byte("{}")
	if p.Metadata != nil {
		b, err := json.Marshal(p.Metadata)
		if err != nil {
			return domain.DailyInsight{}, false, fmt.Errorf("insights.Create metadata: %w", err)
		}
		meta = b
	}
	row, err := r.q.CreateDailyInsight(ctx, sqlcgen.CreateDailyInsightParams{
		HouseholdID: p.HouseholdID,
		UserID:      p.UserID,
		InsightDate: pgtype.Date{Time: p.InsightDate, Valid: true},
		InsightType: p.InsightType,
		Title:       p.Title,
		Body:        p.Body,
		Severity:    p.Severity,
		Metadata:    meta,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DailyInsight{}, false, nil
	}
	if err != nil {
		return domain.DailyInsight{}, false, fmt.Errorf("insights.Create: %w", err)
	}
	return toDomain(row), true, nil
}

func (r *Repository) GetByID(ctx context.Context, id uuid.UUID) (domain.DailyInsight, error) {
	row, err := r.q.GetDailyInsightByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.DailyInsight{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.DailyInsight{}, fmt.Errorf("insights.GetByID: %w", err)
	}
	return toDomain(row), nil
}

type ListFilters struct {
	UserID      *uuid.UUID
	OnlyUnread  *bool
	From        *time.Time
	To          *time.Time
	InsightType *string
	Limit       int32
	Offset      int32
}

func (r *Repository) ListByHousehold(ctx context.Context, householdID uuid.UUID, f ListFilters) ([]domain.DailyInsight, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	rows, err := r.q.ListDailyInsightsByHousehold(ctx, sqlcgen.ListDailyInsightsByHouseholdParams{
		HouseholdID: householdID,
		Limit:       f.Limit,
		Offset:      f.Offset,
		UserID:      f.UserID,
		OnlyUnread:  boolPtr(f.OnlyUnread),
		FromDate:    datePtr(f.From),
		ToDate:      datePtr(f.To),
		InsightType: textPtr(f.InsightType),
	})
	if err != nil {
		return nil, fmt.Errorf("insights.List: %w", err)
	}
	out := make([]domain.DailyInsight, len(rows))
	for i, row := range rows {
		out[i] = toDomain(row)
	}
	return out, nil
}

func (r *Repository) CountUnread(ctx context.Context, householdID uuid.UUID, userID *uuid.UUID) (int64, error) {
	n, err := r.q.CountUnreadInsightsByHousehold(ctx, sqlcgen.CountUnreadInsightsByHouseholdParams{
		HouseholdID: householdID,
		UserID:      userID,
	})
	if err != nil {
		return 0, fmt.Errorf("insights.CountUnread: %w", err)
	}
	return n, nil
}

func (r *Repository) MarkRead(ctx context.Context, id uuid.UUID) error {
	if err := r.q.MarkDailyInsightRead(ctx, id); err != nil {
		return fmt.Errorf("insights.MarkRead: %w", err)
	}
	return nil
}

func (r *Repository) MarkAllRead(ctx context.Context, householdID uuid.UUID, userID *uuid.UUID) error {
	if err := r.q.MarkAllInsightsReadByHousehold(ctx, sqlcgen.MarkAllInsightsReadByHouseholdParams{
		HouseholdID: householdID,
		UserID:      userID,
	}); err != nil {
		return fmt.Errorf("insights.MarkAllRead: %w", err)
	}
	return nil
}

func (r *Repository) Delete(ctx context.Context, id uuid.UUID) error {
	if err := r.q.DeleteDailyInsight(ctx, id); err != nil {
		return fmt.Errorf("insights.Delete: %w", err)
	}
	return nil
}

// ===================== agregaciones =====================

func (r *Repository) SumSpentAt(ctx context.Context, householdID uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumExpensesSpentAtRange(ctx, sqlcgen.SumExpensesSpentAtRangeParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("insights.SumSpentAt: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

type SpentCounts struct {
	Total              int64
	DistinctCategories int64
}

func (r *Repository) CountSpentAt(ctx context.Context, householdID uuid.UUID, from, to time.Time) (SpentCounts, error) {
	row, err := r.q.CountExpensesSpentAtRange(ctx, sqlcgen.CountExpensesSpentAtRangeParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return SpentCounts{}, fmt.Errorf("insights.CountSpentAt: %w", err)
	}
	return SpentCounts{Total: row.TotalCount, DistinctCategories: row.DistinctCategories}, nil
}

func (r *Repository) SumDue(ctx context.Context, householdID uuid.UUID, from, to time.Time) (float64, error) {
	n, err := r.q.SumInstallmentsDueInRange(ctx, sqlcgen.SumInstallmentsDueInRangeParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return 0, fmt.Errorf("insights.SumDue: %w", err)
	}
	return db.FloatFromNumeric(n), nil
}

type TopCategory struct {
	CategoryID *uuid.UUID
	Total      float64
	Found      bool
}

func (r *Repository) TopCategory(ctx context.Context, householdID uuid.UUID, from, to time.Time) (TopCategory, error) {
	row, err := r.q.TopCategorySpentAtRange(ctx, sqlcgen.TopCategorySpentAtRangeParams{
		HouseholdID: householdID,
		Column2:     pgtype.Date{Time: from, Valid: true},
		Column3:     pgtype.Date{Time: to, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return TopCategory{Found: false}, nil
	}
	if err != nil {
		return TopCategory{}, fmt.Errorf("insights.TopCategory: %w", err)
	}
	return TopCategory{CategoryID: row.CategoryID, Total: db.FloatFromNumeric(row.TotalBase), Found: true}, nil
}

// ===================== mappers =====================

func toDomain(row sqlcgen.DailyInsight) domain.DailyInsight {
	return domain.DailyInsight{
		ID:          row.ID,
		HouseholdID: row.HouseholdID,
		UserID:      row.UserID,
		InsightDate: row.InsightDate.Time,
		InsightType: row.InsightType,
		Title:       row.Title,
		Body:        row.Body,
		Severity:    row.Severity,
		IsRead:      row.IsRead,
		Metadata:    row.Metadata,
		CreatedAt:   row.CreatedAt.Time,
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

func datePtr(p *time.Time) pgtype.Date {
	if p == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *p, Valid: true}
}
