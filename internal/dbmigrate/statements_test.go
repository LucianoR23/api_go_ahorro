package dbmigrate_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

// Migration 000035 (plan 4.1): credit card statements and adjustments.

func newCard(t *testing.T, pool *pgxpool.Pool, owner string) string {
	t.Helper()
	var card string
	if err := pool.QueryRow(context.Background(), `
		WITH pm AS (
			INSERT INTO payment_methods (owner_user_id, name, kind, allows_installments)
			VALUES ($1, 'Visa', 'credit', true) RETURNING id)
		INSERT INTO credit_cards (payment_method_id, alias, default_closing_day, default_due_day)
		SELECT id, 'Visa', 24, 3 FROM pm RETURNING id::text`, owner).Scan(&card); err != nil {
		t.Fatalf("card: %v", err)
	}
	return card
}

func TestCardStatementsMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbURL := testdb.NewEmptyURL(t)
	m := migrator(t, dbURL)
	if err := m.Migrate(35); err != nil {
		t.Fatalf("up to 35: %v", err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	card := newCard(t, pool, newUser(t, pool))
	if _, err := pool.Exec(ctx, `INSERT INTO credit_card_statements (credit_card_id, period_ym) VALUES ($1, '2026-11')`, card); err != nil {
		t.Fatal(err)
	}

	t.Run("a payment has its date, amount and currency or none of them", func(t *testing.T) {
		_, err := pool.Exec(ctx, `UPDATE credit_card_statements SET paid_at = now() WHERE credit_card_id = $1`, card)
		wantConstraint(t, err, "credit_card_statements_payment_complete")
		if _, err := pool.Exec(ctx, `UPDATE credit_card_statements SET paid_at = now(), paid_amount = 10, paid_currency = 'ARS'
			WHERE credit_card_id = $1`, card); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("bad periods and adjustments are rejected", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `INSERT INTO credit_card_statements (credit_card_id, period_ym) VALUES ($1, '2026-13')`, card); err == nil {
			t.Error("month 13 accepted")
		}
		_, err := pool.Exec(ctx, `INSERT INTO credit_card_statement_adjustments (credit_card_id, period_ym, kind, description, amount)
			VALUES ($1, '2026-10', 'fee', 'x', 1)`, card)
		wantConstraint(t, err, "credit_card_statement_adjustments_statement_fk")
		if _, err := pool.Exec(ctx, `INSERT INTO credit_card_statement_adjustments (credit_card_id, period_ym, kind, description, amount)
			VALUES ($1, '2026-11', 'bonus', 'x', 1)`, card); err == nil {
			t.Error("unknown kind accepted")
		}
	})

	t.Run("deleting the card deletes its statements and adjustments", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `INSERT INTO credit_card_statement_adjustments (credit_card_id, period_ym, kind, description, amount)
			VALUES ($1, '2026-11', 'interest', 'Intereses', 50)`, card); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM credit_cards WHERE id = $1`, card); err != nil {
			t.Fatal(err)
		}
		var left int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM credit_card_statements) + (SELECT count(*) FROM credit_card_statement_adjustments)`).
			Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Errorf("%d rows left", left)
		}
	})

	t.Run("down and up again", func(t *testing.T) {
		if err := m.Migrate(34); err != nil {
			t.Fatalf("down to 34: %v", err)
		}
		if err := m.Migrate(35); err != nil {
			t.Fatalf("up again: %v", err)
		}
	})
}
