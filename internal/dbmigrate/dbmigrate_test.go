package dbmigrate

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/LucianoR23/api_go_ahorra/migrations"
)

func TestToPgx5URL(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "postgres://u:p@db:5432/x?sslmode=disable", want: "pgx5://u:p@db:5432/x?sslmode=disable"},
		{in: "postgresql://u:p@db/x", want: "pgx5://u:p@db/x"},
		{in: "pgx5://u:p@db/x", want: "pgx5://u:p@db/x"},
		{in: "mysql://u:p@db/x", wantErr: true},
		{in: "postgres://u:se cret@db:bad port/x", wantErr: true},
	}
	for _, tt := range tests {
		got, err := toPgx5URL(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("toPgx5URL(%q) = %q, want error", tt.in, got)
			}
			if err != nil && strings.Contains(err.Error(), "se cret") {
				t.Errorf("toPgx5URL error leaks the password: %v", err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("toPgx5URL(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestRedact(t *testing.T) {
	const dbURL = "postgres://valuo_migrate:s3cretpw@db:5432/x"

	err := redact(errors.New("dial pgx5://valuo_migrate:s3cretpw@db failed"), dbURL)
	if strings.Contains(err.Error(), "s3cretpw") {
		t.Fatalf("password not redacted: %v", err)
	}

	plain := errors.New("connection refused")
	if got := redact(plain, dbURL); got != plain {
		t.Errorf("redact changed an error without the password: %v", got)
	}
	if redact(nil, dbURL) != nil {
		t.Error("redact(nil) must be nil")
	}
}

func TestEmbeddedMigrationsArePaired(t *testing.T) {
	ups, _ := fs.Glob(migrations.FS, "*.up.sql")
	downs, _ := fs.Glob(migrations.FS, "*.down.sql")
	if len(ups) == 0 {
		t.Fatal("no migrations embedded")
	}
	if len(ups) != len(downs) {
		t.Fatalf("%d up files vs %d down files", len(ups), len(downs))
	}
	for _, up := range ups {
		down := strings.TrimSuffix(up, ".up.sql") + ".down.sql"
		if _, err := fs.Stat(migrations.FS, down); err != nil {
			t.Errorf("%s has no matching %s", up, down)
		}
	}
}

// TestUpIntegration runs against a real, EMPTY Postgres database. It is skipped
// unless DBMIGRATE_TEST_DATABASE_URL is set (testcontainers replaces this in 0.11).
func TestUpIntegration(t *testing.T) {
	dbURL := os.Getenv("DBMIGRATE_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("DBMIGRATE_TEST_DATABASE_URL not set")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ups, _ := fs.Glob(migrations.FS, "*.up.sql")
	latest := uint(len(ups))

	// Two instances booting at once: the advisory lock serializes them and
	// exactly one reports a change.
	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() { results[i], errs[i] = Up(dbURL, migrations.FS, logger) })
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
	again, err := Up(dbURL, migrations.FS, logger)
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

	if _, err := Up(dbURL, migrations.FS, logger); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("Up on a dirty DB: err = %v, want a dirty error", err)
	}
}
