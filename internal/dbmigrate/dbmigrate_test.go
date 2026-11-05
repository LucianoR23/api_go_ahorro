package dbmigrate

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

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
