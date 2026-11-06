package config

import (
	"strings"
	"testing"
)

// setRequired sets the env vars Load always needs, so each test only varies
// what it is about. The package dir has no .env, so nothing else leaks in.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://app:pw@localhost:5432/x")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
}

func TestLoadMigrateOnStart(t *testing.T) {
	tests := []struct {
		name       string
		onStart    string
		migrateURL string
		wantOn     bool
		wantErr    string
	}{
		{name: "unset defaults to off", onStart: "", wantOn: false},
		{name: "off does not need a migrate URL", onStart: "false", wantOn: false},
		{name: "on with URL", onStart: "true", migrateURL: "postgres://mig:pw@localhost:5432/x", wantOn: true},
		{name: "on without URL fails", onStart: "true", wantErr: "MIGRATE_DATABASE_URL"},
		{name: "on with blank URL fails", onStart: "1", migrateURL: "   ", wantErr: "MIGRATE_DATABASE_URL"},
		{name: "garbage value fails", onStart: "yes please", wantErr: "MIGRATE_ON_START"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("MIGRATE_ON_START", tt.onStart)
			t.Setenv("MIGRATE_DATABASE_URL", tt.migrateURL)

			cfg, err := Load()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Load() error = %v, want it to mention %s", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}
			if cfg.MigrateOnStart != tt.wantOn {
				t.Errorf("MigrateOnStart = %v, want %v", cfg.MigrateOnStart, tt.wantOn)
			}
		})
	}
}

func TestLoadMigrateDoesNotFallBackToDatabaseURL(t *testing.T) {
	setRequired(t)
	t.Setenv("MIGRATE_ON_START", "false")
	t.Setenv("MIGRATE_DATABASE_URL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.MigrateDatabaseURL != "" {
		t.Errorf("MigrateDatabaseURL = %q, want empty (no fallback to DATABASE_URL)", cfg.MigrateDatabaseURL)
	}
}

func TestLoadBrand(t *testing.T) {
	t.Run("defaults to today's name, from-names follow it", func(t *testing.T) {
		setRequired(t)
		t.Setenv("BRAND_NAME", "")
		t.Setenv("REPORT_FROM_EMAIL", "")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BrandName != "Ahorro" || cfg.ReportFromEmail != "Ahorro <onboarding@resend.dev>" {
			t.Errorf("brand %q, report from %q", cfg.BrandName, cfg.ReportFromEmail)
		}
	})

	t.Run("BRAND_NAME drives the defaults; explicit from-names win", func(t *testing.T) {
		setRequired(t)
		t.Setenv("BRAND_NAME", "Valuo")
		t.Setenv("INVITE_FROM_EMAIL", "")
		t.Setenv("REPORT_FROM_EMAIL", "Resúmenes <reports@valuo.ar>")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.BrandName != "Valuo" || cfg.InviteFromEmail != "Valuo <onboarding@resend.dev>" || cfg.ReportFromEmail != "Resúmenes <reports@valuo.ar>" {
			t.Errorf("cfg = %q / %q / %q", cfg.BrandName, cfg.InviteFromEmail, cfg.ReportFromEmail)
		}
	})
}

func TestLoadTimezone(t *testing.T) {
	t.Run("defaults to Buenos Aires", func(t *testing.T) {
		setRequired(t)
		t.Setenv("APP_TIMEZONE", "")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.AppTimezone != "America/Argentina/Buenos_Aires" || cfg.Location == nil || cfg.Location.String() != cfg.AppTimezone {
			t.Errorf("timezone %q, location %v", cfg.AppTimezone, cfg.Location)
		}
	})

	t.Run("another valid zone", func(t *testing.T) {
		setRequired(t)
		t.Setenv("APP_TIMEZONE", "America/Montevideo")
		cfg, err := Load()
		if err != nil || cfg.Location.String() != "America/Montevideo" {
			t.Errorf("cfg = %v, err = %v", cfg, err)
		}
	})

	t.Run("an invalid zone stops startup", func(t *testing.T) {
		setRequired(t)
		t.Setenv("APP_TIMEZONE", "Argentina/Narnia")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "APP_TIMEZONE") {
			t.Errorf("err = %v", err)
		}
	})
}
