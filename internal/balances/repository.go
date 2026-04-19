// Package balances: repository que lee la matriz de deudas y los settlements
// agregados. Todo es on-demand (no hay tabla materializada): cada request
// recomputa sumando shares con billing_date <= CURRENT_DATE y restando
// settlements ya registrados.
package balances

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/db"
	sqlcgen "github.com/LucianoR23/api_go_ahorra/internal/db/sqlc"
)

type Repository struct {
	pool *pgxpool.Pool
	q    *sqlcgen.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool, q: sqlcgen.New(pool)}
}

// MatrixRow: una celda cruda de la matriz, todavía en "direcciones separadas"
// (debtor → creditor). El service la consolida en pares netos.
type MatrixRow struct {
	Debtor     uuid.UUID
	Creditor   uuid.UUID
	BilledOwed float64
}

// SettlementRow: total pagado por from → to en el hogar. Tambien en
// direcciones separadas; el service las combina al netear.
type SettlementRow struct {
	From      uuid.UUID
	To        uuid.UUID
	PaidTotal float64
}

// HouseholdMatrix devuelve las dos fuentes que alimentan el cálculo de
// balances: shares billed (deuda generada) y settlements (deuda pagada).
// El service hace la resta y normaliza a pares from→to con Amount > 0.
func (r *Repository) HouseholdMatrix(ctx context.Context, householdID uuid.UUID) ([]MatrixRow, []SettlementRow, error) {
	rawMatrix, err := r.q.BalanceMatrixByHousehold(ctx, householdID)
	if err != nil {
		return nil, nil, fmt.Errorf("balances.HouseholdMatrix/shares: %w", err)
	}
	matrix := make([]MatrixRow, len(rawMatrix))
	for i, row := range rawMatrix {
		matrix[i] = MatrixRow{
			Debtor:     row.DebtorID,
			Creditor:   row.CreditorID,
			BilledOwed: db.FloatFromNumeric(row.BilledOwed),
		}
	}

	rawSettle, err := r.q.SettlementsByHouseholdAggregated(ctx, householdID)
	if err != nil {
		return nil, nil, fmt.Errorf("balances.HouseholdMatrix/settlements: %w", err)
	}
	settle := make([]SettlementRow, len(rawSettle))
	for i, row := range rawSettle {
		settle[i] = SettlementRow{
			From:      row.FromUser,
			To:        row.ToUser,
			PaidTotal: db.FloatFromNumeric(row.PaidTotal),
		}
	}
	return matrix, settle, nil
}

// PairBalance devuelve los 4 valores crudos necesarios para calcular el
// balance entre dos miembros (owed en cada dirección + settled en cada
// dirección). El service los combina con la fórmula del plan.
type PairRaw struct {
	OwedByFrom float64
	OwedByTo   float64
	SettledFwd float64
	SettledBwd float64
}

// PairBalance pide la fila agregada entre dos usuarios. sqlc espera los
// params nombrados; internamente matchean con $2/$3 de la query.
func (r *Repository) PairBalance(ctx context.Context, householdID, from, to uuid.UUID) (PairRaw, error) {
	row, err := r.q.BalanceOwedBetween(ctx, sqlcgen.BalanceOwedBetweenParams{
		HouseholdID: householdID,
		FromUser:    from,
		ToUser:      to,
	})
	if err != nil {
		return PairRaw{}, fmt.Errorf("balances.PairBalance: %w", err)
	}
	return PairRaw{
		OwedByFrom: db.FloatFromNumeric(row.OwedByFrom),
		OwedByTo:   db.FloatFromNumeric(row.OwedByTo),
		SettledFwd: db.FloatFromNumeric(row.SettledFwd),
		SettledBwd: db.FloatFromNumeric(row.SettledBwd),
	}, nil
}
