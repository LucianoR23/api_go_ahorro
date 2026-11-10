package cpi

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/money"
)

// Service stores the index and serves it to reports.
type Service struct {
	q       *sqlcgen.Queries
	fetcher *Fetcher
	logger  *slog.Logger
}

func NewService(pool *pgxpool.Pool, fetcher *Fetcher, logger *slog.Logger) *Service {
	return &Service{q: sqlcgen.New(pool), fetcher: fetcher, logger: logger}
}

// Refresh downloads the series and stores the new or revised months. It is
// the daily cpi_refresh job: most days nothing changes.
func (s *Service) Refresh(ctx context.Context) error {
	points, err := s.fetcher.Fetch(ctx)
	if points == nil {
		return fmt.Errorf("cpi.Refresh: %w", err)
	}
	if err != nil {
		s.logger.WarnContext(ctx, "cpi refresh: INDEC failed, used datos.gob.ar", "error", err)
	}
	p := sqlcgen.UpsertCPIParams{
		Months: make([]pgtype.Date, len(points)),
		Vals:   make([]string, len(points)),
	}
	for i, pt := range points {
		p.Months[i] = pgtype.Date{Time: pt.Month, Valid: true}
		p.Vals[i] = pt.Value.String()
	}
	n, err := s.q.UpsertCPI(ctx, p)
	if err != nil {
		return fmt.Errorf("cpi.Refresh: store: %w", err)
	}
	s.logger.InfoContext(ctx, "cpi refresh: ok", "months", len(points), "changed", n,
		"through", NewSeries(points).Through().Format("2006-01"))
	return nil
}

// Series is the stored index.
func (s *Service) Series(ctx context.Context) (Series, error) {
	rows, err := s.q.ListCPI(ctx)
	if err != nil {
		return Series{}, fmt.Errorf("cpi.Series: %w", err)
	}
	points := make([]Point, len(rows))
	for i, r := range rows {
		points[i] = Point{Month: MonthOf(r.Month.Time), Value: money.FromNumeric(r.Value)}
	}
	return NewSeries(points), nil
}
