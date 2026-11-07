package dbmigrate_test

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
	"github.com/LucianoR23/api_go_ahorra/migrations"
)

// TestForeignKeysAreIndexed: every foreign key has an index that starts with
// its columns (plan 0.14). Postgres doesn't create one for the referencing
// side; without it, deleting a parent row scans the whole child table.
func TestForeignKeysAreIndexed(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	rows, err := pool.Query(context.Background(), `
		SELECT c.conrelid::regclass::text, c.conname
		FROM pg_constraint c
		WHERE c.contype = 'f'
		  AND c.connamespace = 'public'::regnamespace
		  AND NOT EXISTS (
		      SELECT 1 FROM pg_index i
		      WHERE i.indrelid = c.conrelid
		        AND (i.indkey::int2[])[0:cardinality(c.conkey) - 1] @> c.conkey
		        AND (i.indkey::int2[])[0:cardinality(c.conkey) - 1] <@ c.conkey
		  )
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	missing, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
		var table, name string
		err := r.Scan(&table, &name)
		return table + "." + name, err
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(missing) > 0 {
		t.Errorf("foreign keys without an index:\n  %s", strings.Join(missing, "\n  "))
	}
}

// TestInstallmentHouseholdBackfill: migration 000030 copies each expense's
// household to its installments, and the composite foreign key then rejects
// an installment whose household differs from its expense's.
func TestInstallmentHouseholdBackfill(t *testing.T) {
	t.Parallel()
	dbURL := testdb.NewEmptyURL(t)
	ctx := context.Background()

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	driverURL, err := url.Parse(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	driverURL.Scheme = "pgx5"
	m, err := migrate.NewWithSourceInstance("iofs", src, driverURL.String())
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	if err := m.Migrate(29); err != nil {
		t.Fatalf("migrate to 29: %v", err)
	}

	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })

	// Two households, one expense with two installments in each.
	var hh [2]string
	for i := range hh {
		err := conn.QueryRow(ctx, `
			WITH u AS (
			    INSERT INTO users (email, first_name, last_name)
			    VALUES ('u' || $1 || '@example.test', 'U', 'Test') RETURNING id
			), h AS (
			    INSERT INTO households (name, created_by) SELECT 'H', id FROM u RETURNING id, created_by
			), pm AS (
			    INSERT INTO payment_methods (owner_user_id, name, kind, allows_installments)
			    SELECT id, 'Visa', 'credit', true FROM u RETURNING id
			), e AS (
			    INSERT INTO expenses (household_id, created_by, payment_method_id, amount, amount_base,
			                          base_currency, description, installments)
			    SELECT h.id, h.created_by, pm.id, 200, 200, 'ARS', 'Silla', 2 FROM h, pm RETURNING id, household_id
			), i AS (
			    INSERT INTO expense_installments (expense_id, installment_number, installment_amount,
			                                      installment_amount_base, billing_date)
			    SELECT e.id, n, 100, 100, DATE '2026-03-01' + n FROM e, generate_series(1, 2) n
			)
			SELECT household_id::text FROM e`, strconv.Itoa(i)).Scan(&hh[i])
		if err != nil {
			t.Fatalf("seed household %d: %v", i, err)
		}
	}

	if err := m.Migrate(30); err != nil {
		t.Fatalf("migrate to 30: %v", err)
	}

	var wrong, total int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE i.household_id IS DISTINCT FROM e.household_id), count(*)
		FROM expense_installments i JOIN expenses e ON e.id = i.expense_id`).Scan(&wrong, &total); err != nil {
		t.Fatalf("check backfill: %v", err)
	}
	if total != 4 || wrong != 0 {
		t.Fatalf("backfill: %d of %d installments with the wrong household", wrong, total)
	}

	// An installment of household 0's expense claiming household 1.
	_, err = conn.Exec(ctx, `
		INSERT INTO expense_installments (expense_id, household_id, installment_number, installment_amount,
		                                  installment_amount_base, billing_date)
		SELECT id, $1, 3, 100, 100, DATE '2026-03-10' FROM expenses WHERE household_id = $2`, hh[1], hh[0])
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("installment with another household's id: err = %v, want a foreign key violation", err)
	}

	// Deleting an expense still deletes its installments.
	if _, err := conn.Exec(ctx, `DELETE FROM expenses WHERE household_id = $1`, hh[0]); err != nil {
		t.Fatalf("delete expense: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM expense_installments`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("installments after deleting one expense = %d, want 2", total)
	}

	// The down migration restores the previous schema.
	if err := m.Migrate(29); err != nil {
		t.Fatalf("migrate down to 29: %v", err)
	}
}
