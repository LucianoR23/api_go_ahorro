package dbmigrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

// Migration 000036 (plan 4.3): card reminders are once per card and day.

func TestCardReminderInsightsMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbURL := testdb.NewEmptyURL(t)
	m := migrator(t, dbURL)
	if err := m.Migrate(36); err != nil {
		t.Fatalf("up to 36: %v", err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	user := newUser(t, pool)
	space := newSpace(t, pool, user, "personal")
	insert := func(date string) int64 {
		t.Helper()
		tag, err := pool.Exec(ctx, `
			INSERT INTO daily_insights (household_id, user_id, insight_date, insight_type, title, body, severity, ref_id)
			VALUES ($1, $2, $3, 'card_due_soon', 't', 'b', 'warning', $1)
			ON CONFLICT DO NOTHING`, space, user, date)
		if err != nil {
			t.Fatal(err)
		}
		return tag.RowsAffected()
	}

	if insert("2026-11-20") != 1 || insert("2026-11-20") != 0 {
		t.Error("the same card reminder twice on one day")
	}
	if insert("2026-12-20") != 1 {
		t.Error("a card reminder next month was dropped")
	}

	t.Run("down and up again", func(t *testing.T) {
		if err := m.Migrate(35); err != nil {
			t.Fatalf("down to 35: %v", err)
		}
		if err := m.Migrate(36); err != nil {
			t.Fatalf("up to 36: %v", err)
		}
		if insert("2026-11-20") != 1 {
			t.Error("reminders were not cleared on the way down")
		}
	})
}
