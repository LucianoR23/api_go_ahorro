package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/dates"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
)

// Config concentra todas las variables de entorno tipadas que la app necesita.
// Se construye una sola vez en arranque desde Load().
type Config struct {
	Port        string
	Env         string
	LogLevel    string
	DatabaseURL string

	// Migrations on startup (MIGRATE_ON_START, default false). When on, the
	// API applies pending migrations with MigrateDatabaseURL (a schema-owner
	// role) before opening the runtime pool. There is deliberately no
	// fallback to DatabaseURL: the runtime role must not be able to run DDL.
	MigrateOnStart     bool
	MigrateDatabaseURL string

	// JWTSecret signs access tokens. Refresh tokens are server-side sessions
	// since plan 0.5 (JWT_REFRESH_SECRET is gone; ignored if still set).
	JWTSecret      string
	AllowedOrigins []string

	// Resend: opcional. Si no hay API key, el worker de reports no manda
	// emails (loguea warning y sigue). Útil para dev sin configurar nada.
	ResendAPIKey string
	// BrandName is the product name shown to users (emails, banner). Plan
	// 0.15b: one source; 1.1 switches the value to Valuo.
	BrandName              string
	ReportFromEmail        string // e.g. "<Brand> <reports@example.com>"
	InviteFromEmail        string
	PasswordResetFromEmail string
	VerificationFromEmail  string

	// TrustedProxies (TRUSTED_PROXIES, CIDRs, comma separated): the reverse
	// proxy's network (Traefik); only connections from it may set
	// X-Forwarded-For. Empty: trust nobody (plan 0.6).
	TrustedProxies []string
	// CloudflareRanges (CLOUDFLARE_RANGES): edges whose CF-Connecting-IP is
	// believed; defaults to Cloudflare's published list.
	CloudflareRanges []string

	// AppTimezone (APP_TIMEZONE): the IANA zone that decides "today", the
	// current month and the hour the daily jobs run (plan 0.2). Location is
	// the loaded zone.
	AppTimezone string
	Location    *time.Location

	// AppBaseURL: URL pública del frontend (ej: https://app.ahorra.app).
	// Se usa para armar los links de invitación que van en los mails.
	// En dev cae a http://localhost:3005.
	AppBaseURL string

	// Google OAuth: opcional. Si no está, /auth/google falla con 500 al
	// llamarse (el verifier rechaza por clientID vacío). El resto del API
	// arranca normal — útil para dev sin haber configurado Google Cloud.
	GoogleOAuthClientID string

	// VAPID: opcional. Si no hay keys, los endpoints de push siguen
	// funcionando pero Service.NotifyUsers hace no-op. Útil para dev.
	// Generar una sola vez con `go run ./cmd/vapidgen` y guardar como
	// secret en Coolify.
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string // "mailto:admin@ahorra.app"
	// PushExtraHosts (PUSH_EXTRA_HOSTS): push endpoint hosts accepted
	// besides the known push services. Tests only; leave empty.
	PushExtraHosts []string

	// Lemy Support: proxy al servicio centralizado de soporte. La API key se
	// saca del panel (https://soporte.lemydev.com) y se muestra una sola vez.
	// Server-side only — nunca exponer al front. Sin SupportAPIKey el módulo
	// queda deshabilitado (endpoints /support/* responden 503).
	SupportAPIURL string // dev: http://localhost:8081 — prod: https://api-soporte.lemydev.com
	SupportAPIKey string
}

// Load lee el archivo .env (si existe) al ambiente del proceso y construye
// un Config validado. Las env vars reales del SO tienen prioridad sobre .env
// — en producción .env no existe y las variables las inyecta Coolify.
func Load() (*Config, error) {
	// En dev existe .env; en prod no. Silenciamos el error si no está.
	if err := loadDotEnv(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("config: cargar .env: %w", err)
	}

	migrateOnStart, err := parseBool("MIGRATE_ON_START", false)
	if err != nil {
		return nil, err
	}

	brand := getOrDefault("BRAND_NAME", "Ahorro")

	cfg := &Config{
		Port:                   getOrDefault("PORT", "8080"),
		Env:                    getOrDefault("ENV", "dev"),
		LogLevel:               getOrDefault("LOG_LEVEL", "info"),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		MigrateOnStart:         migrateOnStart,
		MigrateDatabaseURL:     os.Getenv("MIGRATE_DATABASE_URL"),
		JWTSecret:              os.Getenv("JWT_SECRET"),
		AllowedOrigins:         splitAndTrim(getOrDefault("ALLOWED_ORIGINS", "http://localhost:3005"), ","),
		ResendAPIKey:           os.Getenv("RESEND_API_KEY"),
		BrandName:              brand,
		ReportFromEmail:        getOrDefault("REPORT_FROM_EMAIL", brand+" <onboarding@resend.dev>"),
		InviteFromEmail:        getOrDefault("INVITE_FROM_EMAIL", brand+" <onboarding@resend.dev>"),
		PasswordResetFromEmail: getOrDefault("PASSWORD_RESET_FROM_EMAIL", brand+" <onboarding@resend.dev>"),
		VerificationFromEmail:  getOrDefault("VERIFICATION_FROM_EMAIL", brand+" <onboarding@resend.dev>"),
		AppBaseURL:             getOrDefault("APP_BASE_URL", "http://localhost:3005"),
		AppTimezone:            getOrDefault("APP_TIMEZONE", dates.DefaultTimezone),
		TrustedProxies:         splitAndTrim(os.Getenv("TRUSTED_PROXIES"), ","),
		CloudflareRanges:       splitAndTrim(getOrDefault("CLOUDFLARE_RANGES", strings.Join(httpx.CloudflareRanges, ",")), ","),
		GoogleOAuthClientID:    os.Getenv("GOOGLE_OAUTH_CLIENT_ID"),
		VAPIDPublicKey:         os.Getenv("VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey:        os.Getenv("VAPID_PRIVATE_KEY"),
		VAPIDSubject:           getOrDefault("VAPID_SUBJECT", "mailto:admin@ahorra.app"),
		PushExtraHosts:         splitAndTrim(os.Getenv("PUSH_EXTRA_HOSTS"), ","),
		SupportAPIURL:          getOrDefault("SOPORTE_API_URL", "http://localhost:8081"),
		SupportAPIKey:          os.Getenv("SOPORTE_API_KEY"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	loc, err := time.LoadLocation(cfg.AppTimezone)
	if err != nil {
		return nil, fmt.Errorf("config: APP_TIMEZONE %q no es una zona horaria válida: %w", cfg.AppTimezone, err)
	}
	cfg.Location = loc
	if _, err := httpx.ParsePrefixes(cfg.TrustedProxies); err != nil {
		return nil, fmt.Errorf("config: TRUSTED_PROXIES: %w", err)
	}
	if _, err := httpx.ParsePrefixes(cfg.CloudflareRanges); err != nil {
		return nil, fmt.Errorf("config: CLOUDFLARE_RANGES: %w", err)
	}
	return cfg, nil
}

func (c *Config) validate() error {
	required := map[string]string{
		"DATABASE_URL": c.DatabaseURL,
		"JWT_SECRET":   c.JWTSecret,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("config: %s es obligatorio", name)
		}
	}
	if c.MigrateOnStart && strings.TrimSpace(c.MigrateDatabaseURL) == "" {
		return fmt.Errorf("config: MIGRATE_DATABASE_URL es obligatorio con MIGRATE_ON_START=true")
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("config: JWT_SECRET debe tener al menos 32 caracteres")
	}
	return nil
}

// loadDotEnv parsea un archivo .env simple y setea las variables al ambiente.
// Formato soportado:
//   - Líneas "KEY=value"
//   - Comentarios que empiezan con #
//   - Líneas en blanco
//   - Comillas opcionales alrededor del valor: KEY="value" o KEY='value'
//
// No interpreta escapes ni interpolación (${VAR}) — mantenemos el parser
// mínimo a propósito.
//
// Si la variable ya está en el ambiente del SO, NO la sobrescribe. Así en
// dev podés overridear un valor del .env exportando la variable antes.
func loadDotEnv(path string) error {
	f, err := os.Open(path) //nolint:gosec // G304: path is the constant ".env" from Load, never user input
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return fmt.Errorf("%s:%d: línea inválida (falta '='): %q", path, lineNum, line)
		}

		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		value = stripQuotes(value)

		if _, exists := os.LookupEnv(key); exists {
			continue // respetar env vars del SO
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("%s:%d: set %s: %w", path, lineNum, key, err)
		}
	}
	return scanner.Err()
}

func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func getOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseBool reads a boolean env var (strconv.ParseBool syntax). Empty means
// def; anything unparsable is an error instead of silently falling back.
func parseBool(key string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("config: %s debe ser true o false, no %q", key, v)
	}
	return b, nil
}

func splitAndTrim(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
