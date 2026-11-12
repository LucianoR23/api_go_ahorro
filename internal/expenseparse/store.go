package expenseparse

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
)

// PGStore reads payment methods from Postgres.
type PGStore struct {
	q *sqlcgen.Queries
}

// NewStore builds the Postgres store.
func NewStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{q: sqlcgen.New(pool)}
}

func (s *PGStore) Methods(ctx context.Context, userID uuid.UUID) ([]Method, error) {
	rows, err := s.q.ListPaymentMatchNames(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("expenseparse: methods: %w", err)
	}
	out := make([]Method, len(rows))
	for i, r := range rows {
		out[i] = Method{ID: r.ID, Name: r.Name, Kind: r.Kind, Bank: r.BankName, Alias: r.CardAlias, Network: r.CardNetwork}
	}
	return out, nil
}
