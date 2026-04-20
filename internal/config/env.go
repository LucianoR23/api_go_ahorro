package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Config concentra todas las variables de entorno tipadas que la app necesita.
// Se construye una sola vez en arranque desde Load().
type Config struct {
	Port             string
	Env              string
	LogLevel         string
	DatabaseURL      string
	JWTSecret        string
	JWTRefreshSecret string
	AllowedOrigins   []string

	// Resend: opcional. Si no hay API key, el worker de reports no manda
	// emails (loguea warning y sigue). Útil para dev sin configurar nada.
	ResendAPIKey    string
	ReportFromEmail string // e.g. "Ahorra <reports@ahorra.app>"

	// VAPID: opcional. Si no hay keys, los endpoints de push siguen
	// funcionando pero Service.NotifyUsers hace no-op. Útil para dev.
	// Generar una sola vez con `go run ./cmd/vapidgen` y guardar como
	// secret en Coolify.
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string // "mailto:admin@ahorra.app"
}

// Load lee el archivo .env (si existe) al ambiente del proceso y construye
// un Config validado. Las env vars reales del SO tienen prioridad sobre .env
// — en producción .env no existe y las variables las inyecta Coolify.
func Load() (*Config, error) {
	// En dev existe .env; en prod no. Silenciamos el error si no está.
	if err := loadDotEnv(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("config: cargar .env: %w", err)
	}

	cfg := &Config{
		Port:             getOrDefault("PORT", "8080"),
		Env:              getOrDefault("ENV", "dev"),
		LogLevel:         getOrDefault("LOG_LEVEL", "info"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		JWTSecret:        os.Getenv("JWT_SECRET"),
		JWTRefreshSecret: os.Getenv("JWT_REFRESH_SECRET"),
		AllowedOrigins:   splitAndTrim(getOrDefault("ALLOWED_ORIGINS", "http://localhost:3000"), ","),
		ResendAPIKey:     os.Getenv("RESEND_API_KEY"),
		ReportFromEmail:  getOrDefault("REPORT_FROM_EMAIL", "Ahorra <onboarding@resend.dev>"),
		VAPIDPublicKey:   os.Getenv("VAPID_PUBLIC_KEY"),
		VAPIDPrivateKey:  os.Getenv("VAPID_PRIVATE_KEY"),
		VAPIDSubject:     getOrDefault("VAPID_SUBJECT", "mailto:admin@ahorra.app"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	required := map[string]string{
		"DATABASE_URL":       c.DatabaseURL,
		"JWT_SECRET":         c.JWTSecret,
		"JWT_REFRESH_SECRET": c.JWTRefreshSecret,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("config: %s es obligatorio", name)
		}
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("config: JWT_SECRET debe tener al menos 32 caracteres")
	}
	if len(c.JWTRefreshSecret) < 32 {
		return fmt.Errorf("config: JWT_REFRESH_SECRET debe tener al menos 32 caracteres")
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
	f, err := os.Open(path)
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
