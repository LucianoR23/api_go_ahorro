package dbmigrate_test

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/LucianoR23/valuo_api/internal/dbmigrate"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
	"github.com/LucianoR23/valuo_api/migrations"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// TestUpIntegration runs the embedded migrations against a real, empty
// Postgres 18 database (testdb: testcontainers or TEST_DATABASE_URL).
func TestUpIntegration(t *testing.T) {
	dbURL := testdb.NewEmptyURL(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ups, _ := fs.Glob(migrations.FS, "*.up.sql")
	latest := uint(len(ups))

	// Two instances booting at once: the advisory lock serializes them and
	// exactly one reports a change.
	var wg sync.WaitGroup
	results := make([]dbmigrate.Result, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() { results[i], errs[i] = dbmigrate.Up(dbURL, migrations.FS, logger) })
	}
	wg.Wait()
	changed := 0
	for i := range 2 {
		if errs[i] != nil {
			t.Fatalf("concurrent Up #%d: %v", i, errs[i])
		}
		if results[i].To != latest {
			t.Fatalf("concurrent Up #%d reached %d, want %d", i, results[i].To, latest)
		}
		if results[i].Changed {
			changed++
		}
	}
	if changed != 1 {
		t.Fatalf("want exactly one instance to apply migrations, got %d (%+v)", changed, results)
	}

	// Re-running is a no-op.
	again, err := dbmigrate.Up(dbURL, migrations.FS, logger)
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if again.Changed || again.From != latest || again.To != latest {
		t.Fatalf("second Up should be a no-op, got %+v", again)
	}

	// A dirty database must stop startup, never be auto-fixed.
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "UPDATE schema_migrations SET dirty = true"); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(ctx, "UPDATE schema_migrations SET dirty = false") })

	if _, err := dbmigrate.Up(dbURL, migrations.FS, logger); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("Up on a dirty DB: err = %v, want a dirty error", err)
	}
}
