package settlements_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/api_go_ahorra/internal/households"
	"github.com/LucianoR23/api_go_ahorra/internal/settlements"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/apitest"
)

// slowDebt reports a fixed debt minus the payments already stored, and
// waits after reading: without the lock, concurrent payments all read the
// debt before any of them inserts.
type slowDebt struct {
	pool *pgxpool.Pool
	debt decimal.Decimal
}

func (b slowDebt) PairNet(ctx context.Context, householdID, _, _ uuid.UUID) (decimal.Decimal, error) {
	var paid decimal.Decimal
	if err := b.pool.QueryRow(ctx,
		`SELECT coalesce(sum(amount_base), 0)::text FROM settlement_payments WHERE household_id = $1`,
		householdID).Scan(&paid); err != nil {
		return decimal.Zero, err
	}
	time.Sleep(300 * time.Millisecond)
	return b.debt.Sub(paid), nil
}

// Plan 0.8: the debt check and the insert run under a household lock.
// Before, two payments of the whole debt sent at once were both accepted.
func TestConcurrentPaymentsCannotExceedTheDebt(t *testing.T) {
	t.Parallel()
	s := apitest.New(t)
	h := newHouse(t, s)
	svc := settlements.NewService(settlements.NewRepository(s.Pool), households.NewRepository(s.Pool),
		slowDebt{pool: s.Pool, debt: decimal.NewFromInt(300)})

	in := settlements.CreateInput{
		HouseholdID: uuid.MustParse(h.id),
		FromUser:    uuid.MustParse(h.ana.ID),
		ToUser:      uuid.MustParse(h.owner.ID),
		Amount:      decimal.NewFromInt(300),
	}
	const n = 3
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Create(context.Background(), in)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	accepted := 0
	for err := range errs {
		if err == nil {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("%d payments of the whole debt accepted, want 1", accepted)
	}

	var list struct {
		TotalCount int `json:"totalCount"`
	}
	h.do(t, h.owner, http.MethodGet, "/settlements", nil).Status(t, http.StatusOK).JSON(t, &list)
	if list.TotalCount != 1 {
		t.Errorf("%d payments stored, want 1", list.TotalCount)
	}
}
