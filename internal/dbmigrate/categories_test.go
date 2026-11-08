package dbmigrate_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
	"github.com/LucianoR23/valuo_api/migrations"
)

// Migration 000034 (plan 1.4): subcategories one level deep inside a
// household, and icons as Lucide keys instead of emojis.

func migrator(t *testing.T, dbURL string) *migrate.Migrate {
	t.Helper()
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
	return m
}

func newCategory(t *testing.T, pool *pgxpool.Pool, household, name, icon string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO categories (household_id, name, icon) VALUES ($1, $2, $3) RETURNING id::text`,
		household, name, icon).Scan(&id); err != nil {
		t.Fatalf("category %s: %v", name, err)
	}
	return id
}

func icon(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var got string
	if err := pool.QueryRow(context.Background(), `SELECT icon FROM categories WHERE id = $1`, id).Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCategoryParentMigration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbURL := testdb.NewEmptyURL(t)
	m := migrator(t, dbURL)
	if err := m.Migrate(33); err != nil {
		t.Fatalf("up to 33: %v", err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	owner := newUser(t, pool)
	h := newSpace(t, pool, owner, "personal")
	stored := map[string]string{
		"🍕":             "utensils",
		"✈️":            "plane", // with the variation selector
		"✈":             "plane", // without it
		"🦄":             "tag",   // an emoji the map doesn't know
		"shopping-cart": "shopping-cart",
		"Not A Key":     "tag",
	}
	ids := map[string]string{}
	for value := range stored {
		ids[value] = newCategory(t, pool, h, "c "+value, value)
	}

	if err := m.Migrate(34); err != nil {
		t.Fatalf("up to 34: %v", err)
	}

	t.Run("emojis become icon keys", func(t *testing.T) {
		for value, want := range stored {
			if got := icon(t, pool, ids[value]); got != want {
				t.Errorf("%q became %q, want %q", value, got, want)
			}
		}
		if _, err := pool.Exec(ctx, `UPDATE categories SET icon = '🍕' WHERE id = $1`, ids["🍕"]); err == nil {
			t.Error("an emoji icon was accepted")
		} else {
			wantConstraint(t, err, "categories_icon_key_check")
		}
		var def string
		if err := pool.QueryRow(ctx, `INSERT INTO categories (household_id, name) VALUES ($1, 'default') RETURNING icon`, h).Scan(&def); err != nil {
			t.Fatal(err)
		}
		if def != "tag" {
			t.Errorf("default icon = %q, want tag", def)
		}
	})

	t.Run("one level, same household", func(t *testing.T) {
		parent := newCategory(t, pool, h, "Servicios", "lightbulb")
		child := newCategory(t, pool, h, "Luz", "zap")
		if _, err := pool.Exec(ctx, `UPDATE categories SET parent_id = $1 WHERE id = $2`, parent, child); err != nil {
			t.Fatalf("set parent: %v", err)
		}

		grandchild := newCategory(t, pool, h, "Luz de la oficina", "zap")
		_, err := pool.Exec(ctx, `UPDATE categories SET parent_id = $1 WHERE id = $2`, child, grandchild)
		wantConstraint(t, err, "categories_one_level")
		other := newCategory(t, pool, h, "Otra", "tag")
		_, err = pool.Exec(ctx, `UPDATE categories SET parent_id = $1 WHERE id = $2`, other, parent)
		wantConstraint(t, err, "categories_one_level")
		_, err = pool.Exec(ctx, `UPDATE categories SET parent_id = id WHERE id = $1`, other)
		wantConstraint(t, err, "categories_parent_not_self")

		elsewhere := newSpace(t, pool, newUser(t, pool), "personal")
		theirs := newCategory(t, pool, elsewhere, "Ajena", "tag")
		_, err = pool.Exec(ctx, `UPDATE categories SET parent_id = $1 WHERE id = $2`, theirs, other)
		wantConstraint(t, err, "categories_parent_fk")

		// Deleting the parent keeps the child, now top-level.
		if _, err := pool.Exec(ctx, `DELETE FROM categories WHERE id = $1`, parent); err != nil {
			t.Fatal(err)
		}
		var parentID *string
		var household string
		if err := pool.QueryRow(ctx, `SELECT parent_id::text, household_id::text FROM categories WHERE id = $1`, child).
			Scan(&parentID, &household); err != nil {
			t.Fatalf("child after deleting its parent: %v", err)
		}
		if parentID != nil || household != h {
			t.Errorf("child parent = %v, household = %s", parentID, household)
		}
	})

	t.Run("down and up again", func(t *testing.T) {
		if err := m.Migrate(33); err != nil {
			t.Fatalf("down to 33: %v", err)
		}
		var left int
		if err := pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM information_schema.columns WHERE table_name = 'categories' AND column_name = 'parent_id')
			     + (SELECT count(*) FROM pg_trigger WHERE tgname = 'categories_one_level')
			     + (SELECT count(*) FROM pg_proc WHERE proname = 'categories_one_level')
			     + (SELECT count(*) FROM pg_constraint WHERE conname IN ('categories_household_id_id_key', 'categories_icon_key_check'))`).
			Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left != 0 {
			t.Errorf("%d objects of 000034 left after down", left)
		}
		if got := icon(t, pool, ids["🍕"]); got != "🍕" {
			t.Errorf("down icon = %q, want 🍕", got)
		}
		if err := m.Migrate(34); err != nil {
			t.Fatalf("up again: %v", err)
		}
	})
}
