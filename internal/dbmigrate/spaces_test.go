package dbmigrate_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
	"github.com/LucianoR23/valuo_api/migrations"
)

// Migration 000032 (plan 1.2): the database itself keeps "Mi plata" private
// and every user in at most one active shared space.

func newUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, first_name, last_name)
		VALUES (gen_random_uuid()::text || '@example.test', 'U', 'Test') RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("user: %v", err)
	}
	return id
}

// newSpace creates a household of kind with owner as its only member.
func newSpace(t *testing.T, pool *pgxpool.Pool, owner, kind string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		WITH h AS (
		    INSERT INTO households (name, created_by, kind) VALUES ('H', $1, $2) RETURNING id
		)
		INSERT INTO household_members (household_id, user_id, role)
		SELECT id, $1, 'owner' FROM h RETURNING household_id::text`, owner, kind).Scan(&id); err != nil {
		t.Fatalf("space %s: %v", kind, err)
	}
	return id
}

func join(ctx context.Context, q interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, household, user string) error {
	_, err := q.Exec(ctx, `INSERT INTO household_members (household_id, user_id, role) VALUES ($1, $2, 'member')`, household, user)
	return err
}

func wantConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != constraint {
		t.Fatalf("err = %v, want a violation of %s", err, constraint)
	}
}

func TestSpacesSchema(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()

	t.Run("existing households are shared", func(t *testing.T) {
		var kind string
		id := newSpace(t, pool, newUser(t, pool), "shared")
		if err := pool.QueryRow(ctx, `SELECT kind FROM households WHERE id = $1`, id).Scan(&kind); err != nil || kind != "shared" {
			t.Fatalf("kind = %q, %v", kind, err)
		}
		var def string
		if err := pool.QueryRow(ctx, `
			SELECT column_default FROM information_schema.columns
			WHERE table_name = 'households' AND column_name = 'kind'`).Scan(&def); err != nil || def != "'shared'::text" {
			t.Errorf("kind default = %q, %v", def, err)
		}
	})

	t.Run("Mi plata only admits its owner", func(t *testing.T) {
		owner, other := newUser(t, pool), newUser(t, pool)
		personal := newSpace(t, pool, owner, "personal")
		wantConstraint(t, join(ctx, pool, personal, other), "household_members_personal_single_member")
	})

	t.Run("one Mi plata per user", func(t *testing.T) {
		u := newUser(t, pool)
		newSpace(t, pool, u, "personal")
		_, err := pool.Exec(ctx, `INSERT INTO households (name, created_by, kind) VALUES ('Otra', $1, 'personal')`, u)
		wantConstraint(t, err, "households_one_personal_per_user")
	})

	t.Run("one active hogar per user; Mi plata and deleted ones don't count", func(t *testing.T) {
		u := newUser(t, pool)
		newSpace(t, pool, u, "personal")
		first := newSpace(t, pool, newUser(t, pool), "shared")
		second := newSpace(t, pool, newUser(t, pool), "shared")
		if err := join(ctx, pool, first, u); err != nil {
			t.Fatalf("first hogar: %v", err)
		}
		wantConstraint(t, join(ctx, pool, second, u), "household_members_one_shared_per_user")

		if _, err := pool.Exec(ctx, `UPDATE households SET deleted_at = now() WHERE id = $1`, first); err != nil {
			t.Fatal(err)
		}
		if err := join(ctx, pool, second, u); err != nil {
			t.Fatalf("join after the first was deleted: %v", err)
		}

		// Restoring the first would leave u in two hogares.
		_, err := pool.Exec(ctx, `UPDATE households SET deleted_at = NULL WHERE id = $1`, first)
		wantConstraint(t, err, "household_members_one_shared_per_user")
	})

	t.Run("kind never changes", func(t *testing.T) {
		id := newSpace(t, pool, newUser(t, pool), "shared")
		_, err := pool.Exec(ctx, `UPDATE households SET kind = 'personal' WHERE id = $1`, id)
		wantConstraint(t, err, "households_kind_immutable")
	})

	t.Run("two simultaneous joins: only one wins", func(t *testing.T) {
		u := newUser(t, pool)
		a := newSpace(t, pool, newUser(t, pool), "shared")
		b := newSpace(t, pool, newUser(t, pool), "shared")

		tx1, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx1.Rollback(ctx) }()
		if err := join(ctx, tx1, a, u); err != nil {
			t.Fatalf("tx1 join: %v", err)
		}

		second := make(chan error, 1)
		go func() {
			second <- pgx.BeginFunc(ctx, pool, func(tx2 pgx.Tx) error { return join(ctx, tx2, b, u) })
		}()

		// tx2 must be waiting on the user's row lock before tx1 commits.
		deadline := time.Now().Add(10 * time.Second)
		for {
			var waiting bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (SELECT 1 FROM pg_stat_activity
				               WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'INSERT INTO household_members%')`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the second join never waited for the first")
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err := tx1.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		wantConstraint(t, <-second, "household_members_one_shared_per_user")
	})
}

// TestHouseholdKindDown: 000032's down migration removes the column, index
// and triggers, and up applies again.
func TestHouseholdKindDown(t *testing.T) {
	t.Parallel()
	dbURL := testdb.NewEmptyURL(t)
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
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.Close() })
	if err := m.Migrate(32); err != nil {
		t.Fatalf("up to 32: %v", err)
	}
	if err := m.Migrate(31); err != nil {
		t.Fatalf("down to 31: %v", err)
	}
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	var left int
	if err := conn.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM information_schema.columns WHERE table_name = 'households' AND column_name = 'kind')
		     + (SELECT count(*) FROM pg_trigger WHERE tgname IN ('households_enforce_spaces', 'household_members_enforce_spaces'))
		     + (SELECT count(*) FROM pg_proc WHERE proname IN ('households_enforce_spaces', 'household_members_enforce_spaces'))`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d objects of 000032 left after down", left)
	}
	if err := m.Migrate(32); err != nil {
		t.Fatalf("up again: %v", err)
	}
}
