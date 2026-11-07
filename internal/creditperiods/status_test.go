package creditperiods

// Status takes "now" as a parameter, so its day boundary can be pinned with
// a fixed clock (item 0.21d).

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/paymethods"
	"github.com/LucianoR23/valuo_api/internal/testutil"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestStatusDayBoundary(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()

	var userID, pmID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, first_name, last_name)
		VALUES ('status@example.test', 'x', 'S', 'T') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO payment_methods (owner_user_id, name, kind, allows_installments)
		VALUES ($1, 'Visa', 'credit', true) RETURNING id`, userID).Scan(&pmID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `WITH cc AS (
			INSERT INTO credit_cards (payment_method_id, alias, default_closing_day, default_due_day)
			VALUES ($1, 'Visa', 25, 5) RETURNING id)
		INSERT INTO credit_card_periods (credit_card_id, period_ym, closing_date, due_date)
		SELECT id, '2026-11', '2026-11-26', '2026-12-07' FROM cc`, pmID); err != nil {
		t.Fatal(err)
	}

	svc := NewService(NewRepository(pool), paymethods.NewService(paymethods.NewRepository(pool)))
	ar := testutil.Location(t)
	at := func(day, hour int) time.Time { return time.Date(2026, 12, day, hour, 0, 0, 0, ar) }

	for _, tc := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{"day before the due date", at(6, 12), false},
		{"due date, morning", at(7, 9), false},
		{"due date, 23:00 in Buenos Aires (02:00 UTC next day)", at(7, 23), false},
		{"day after the due date", at(8, 9), true},
		{"day after, 00:30", time.Date(2026, 12, 8, 0, 30, 0, 0, ar), true},
	} {
		st, err := svc.Status(ctx, userID, pmID, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		if st.DueDatePassed != tc.want {
			t.Errorf("%s: dueDatePassed = %v, want %v", tc.name, st.DueDatePassed, tc.want)
		}
	}

	st, err := svc.Status(ctx, userID, pmID, at(7, 22))
	if err != nil {
		t.Fatal(err)
	}
	if st.DueDatePassed {
		t.Errorf("at 22:00 on the due date (Buenos Aires) dueDatePassed = true")
	}
}
