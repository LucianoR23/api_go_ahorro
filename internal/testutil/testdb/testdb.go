// Package testdb gives each test its own migrated PostgreSQL database.
//
// One server per test binary: a testcontainers Postgres 18 (same image as dev
// and staging), or the server in TEST_DATABASE_URL when set (a superuser URL,
// e.g. CI's service container or the local docker compose DB). The embedded
// migrations are applied once to a template database; every New(t) clones it
// with CREATE DATABASE ... TEMPLATE, which takes milliseconds, so tests never
// share state and can run in parallel.
//
// Packages that use it should declare
//
//	func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }
//
// so the container is removed when the package's tests finish.
//
// Without a database the tests skip locally, but fail when CI or
// TESTDB_REQUIRED is set (scripts/check.sh sets it, so a local check never
// passes without having run them).
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/LucianoR23/valuo_api/internal/dbmigrate"
	"github.com/LucianoR23/valuo_api/migrations"
)

// Image must match docker-compose.yml and the staging database.
const Image = "postgres:18.6-alpine3.24"

var (
	once      sync.Once
	setupErr  error
	adminURL  string // superuser URL to the server's "postgres" database
	template  string // migrated template database name
	container testcontainers.Container
)

// Main runs the package's tests and then removes the container, if one was
// started. Use it from TestMain.
func Main(m *testing.M) int {
	code := m.Run()
	if container != nil {
		if err := testcontainers.TerminateContainer(container); err != nil {
			fmt.Fprintln(os.Stderr, "testdb: terminate container:", err)
		}
	}
	return code
}

// New returns a pool connected to a fresh database with every migration
// applied. The database is dropped when the test ends.
func New(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dbURL := NewURL(t)
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("testdb: open pool: %v", err)
	}
	t.Cleanup(pool.Close) // runs before the drop registered by NewURL
	return pool
}

// NewURL is New without the pool: it returns the URL of a fresh, migrated
// database (for code that opens its own connections).
func NewURL(t testing.TB) string {
	t.Helper()
	ensureServer(t)

	name := "t_" + randomHex(8)
	ctx := context.Background()
	if err := adminExec(ctx, fmt.Sprintf(`CREATE DATABASE %s TEMPLATE %s`, name, template)); err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}
	t.Cleanup(func() {
		if err := adminExec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name)); err != nil {
			t.Logf("testdb: drop %s: %v", name, err)
		}
	})
	return withDatabase(adminURL, name)
}

// NewEmptyURL returns the URL of a fresh database WITHOUT migrations, for
// tests of the migration runner itself.
func NewEmptyURL(t testing.TB) string {
	t.Helper()
	ensureServer(t)

	name := "e_" + randomHex(8)
	if err := adminExec(context.Background(), `CREATE DATABASE `+name); err != nil {
		t.Fatalf("testdb: create empty database: %v", err)
	}
	t.Cleanup(func() {
		_ = adminExec(context.Background(), fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, name))
	})
	return withDatabase(adminURL, name)
}

func ensureServer(t testing.TB) {
	t.Helper()
	once.Do(func() { setupErr = setup() })
	if setupErr != nil {
		if os.Getenv("CI") == "" && os.Getenv("TESTDB_REQUIRED") == "" {
			t.Skipf("testdb: no database available (start Docker or set TEST_DATABASE_URL): %v", setupErr)
		}
		t.Fatalf("testdb: %v", setupErr)
	}
}

func setup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if u := os.Getenv("TEST_DATABASE_URL"); u != "" {
		adminURL = u
	} else {
		ctr, err := tcpostgres.Run(ctx, Image,
			tcpostgres.WithDatabase("postgres"),
			tcpostgres.WithUsername("postgres"),
			tcpostgres.WithPassword("testdb_only_pw"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			return fmt.Errorf("start postgres container: %w", err)
		}
		container = ctr
		u, err := ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			return fmt.Errorf("container connection string: %w", err)
		}
		adminURL = u
	}

	// One template per test binary: packages run in parallel processes and may
	// share a TEST_DATABASE_URL server.
	template = "tpl_" + randomHex(8)
	if err := adminExec(ctx, `CREATE DATABASE `+template); err != nil {
		return fmt.Errorf("create template: %w", err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := dbmigrate.Up(withDatabase(adminURL, template), migrations.FS, quiet); err != nil {
		return fmt.Errorf("migrate template: %w", err)
	}
	return nil
}

func adminExec(ctx context.Context, sql string) error {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	_, err = conn.Exec(ctx, sql)
	return err
}

func withDatabase(raw, name string) string {
	u, err := url.Parse(raw)
	if err != nil {
		panic(fmt.Sprintf("testdb: bad admin URL: %v", err)) // our own constant/CI value
	}
	u.Path = "/" + name
	return u.String()
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
