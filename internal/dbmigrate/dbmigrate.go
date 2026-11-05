// Package dbmigrate applies the embedded golang-migrate migrations on startup.
//
// It is meant to run with a dedicated role that owns the schema (DDL), while
// the API's runtime pool uses a DML-only role. Concurrent instances are safe:
// the pgx/v5 driver holds a Postgres advisory lock while migrating, so a second
// instance waits (up to LockTimeout) and then finds nothing to do.
package dbmigrate

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the "pgx5" scheme
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// LockTimeout bounds how long an instance waits for another one that is
// already migrating. Generous on purpose: a slow migration must not make the
// second instance crash-loop.
const LockTimeout = 2 * time.Minute

// Result reports the schema version before and after Up.
type Result struct {
	From    uint
	To      uint
	Changed bool
}

// Up applies every pending migration in src to the database at dbURL.
// A dirty database (a previous migration failed halfway) is an error: it
// needs a human to inspect it and run `migrate force`, never an automatic fix.
// Errors never contain the password from dbURL.
func Up(dbURL string, src fs.FS, logger *slog.Logger) (Result, error) {
	driverURL, err := toPgx5URL(dbURL)
	if err != nil {
		return Result{}, err
	}

	source, err := iofs.New(src, ".")
	if err != nil {
		return Result{}, fmt.Errorf("dbmigrate: open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", source, driverURL)
	if err != nil {
		return Result{}, redact(fmt.Errorf("dbmigrate: connect: %w", err), dbURL)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil || dbErr != nil {
			logger.Warn("dbmigrate: close", "source_error", srcErr, "db_error", redact(dbErr, dbURL))
		}
	}()
	m.LockTimeout = LockTimeout

	// Read before taking the lock, so it is only a hint for the log: another
	// instance may be migrating right now, and then this read can even see the
	// transient dirty flag of its in-flight migration. The dirty check that
	// counts is the one m.Up() does under the advisory lock (ErrDirty below).
	from, _, err := version(m)
	if err != nil {
		return Result{}, redact(err, dbURL)
	}

	upErr := m.Up()
	to, _, verErr := version(m)

	var dirtyErr migrate.ErrDirty
	switch {
	case errors.As(upErr, &dirtyErr):
		return Result{From: from, To: to}, dirtyError(uint(dirtyErr.Version))
	case errors.Is(upErr, migrate.ErrNoChange):
		// Up ran under the advisory lock and found nothing pending; if another
		// instance migrated while we waited, `from` is stale.
		if verErr != nil {
			return Result{}, redact(verErr, dbURL)
		}
		return Result{From: to, To: to}, nil
	case upErr != nil:
		return Result{From: from, To: to}, redact(fmt.Errorf("dbmigrate: up: %w", upErr), dbURL)
	case verErr != nil:
		return Result{}, redact(verErr, dbURL)
	}
	return Result{From: from, To: to, Changed: to != from}, nil
}

func dirtyError(v uint) error {
	return fmt.Errorf("dbmigrate: database is dirty at version %d; fix it by hand and run `migrate force`", v)
}

// version returns the current schema version; 0 means no migration applied.
func version(m *migrate.Migrate) (uint, bool, error) {
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("dbmigrate: read version: %w", err)
	}
	return v, dirty, nil
}

// toPgx5URL rewrites a standard postgres:// URL to the scheme golang-migrate's
// pgx/v5 driver registers, keeping everything else (host, sslmode, ...) intact.
func toPgx5URL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// url.Parse errors echo the input, which contains the password.
		return "", errors.New("dbmigrate: invalid database URL")
	}
	switch u.Scheme {
	case "postgres", "postgresql":
		u.Scheme = "pgx5"
	case "pgx5":
	default:
		return "", fmt.Errorf("dbmigrate: unsupported database URL scheme %q", u.Scheme)
	}
	return u.String(), nil
}

// redact strips the password of dbURL from err's message, in case a driver
// error echoes the connection string.
func redact(err error, dbURL string) error {
	if err == nil {
		return nil
	}
	u, parseErr := url.Parse(dbURL)
	if parseErr != nil || u.User == nil {
		return err
	}
	pw, ok := u.User.Password()
	if !ok || pw == "" || !strings.Contains(err.Error(), pw) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), pw, "xxxxx"))
}
