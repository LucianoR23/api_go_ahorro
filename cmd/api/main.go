package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v2"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/config"
	"github.com/LucianoR23/api_go_ahorra/internal/db"
	"github.com/LucianoR23/api_go_ahorra/internal/households"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
	"github.com/LucianoR23/api_go_ahorra/internal/users"
)

func main() {
	logger := newLogger()

	cfg, err := config.Load()
	if err != nil {
		logger.Error("no se pudo cargar config", "error", err)
		os.Exit(1)
	}
	logger.Info("config cargada", "env", cfg.Env, "port", cfg.Port)

	// ---------- conexión a DB ----------
	// Usamos un context con timeout solo para el arranque. Para el server
	// corriendo, cada request trae su propio ctx (Go lo provee).
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer bootCancel()

	pool, err := db.NewPool(bootCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("no se pudo conectar a postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	logger.Info("postgres conectado")

	// ---------- wiring de dependencias (repos, services, handlers) ----------
	// Todo se instancia en main: es el composition root. Cualquier cambio
	// de implementación (ej: repo mockeado en tests) se hace acá.
	userRepo := users.NewRepository(pool)

	tokenIssuer, err := auth.NewTokenIssuer(cfg.JWTSecret, cfg.JWTRefreshSecret)
	if err != nil {
		logger.Error("no se pudo crear token issuer", "error", err)
		os.Exit(1)
	}

	authSvc := auth.NewService(userRepo, tokenIssuer)
	authMW := auth.NewMiddleware(tokenIssuer, logger)
	authHandler := auth.NewHandler(authSvc, authMW, logger, cfg.Env == "prod")

	householdsRepo := households.NewRepository(pool)
	householdsSvc := households.NewService(householdsRepo, userRepo)
	householdsHandler := households.NewHandler(householdsSvc, authMW, logger)

	// ---------- router ----------
	r := chi.NewRouter()

	// Middlewares globales. El orden importa: primero request-id (todos
	// los logs siguientes lo heredan), después recovery (atrapa panics),
	// después logger. El timeout va al final para aplicar a los handlers.
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// Logger de requests:
	//   dev  → DevRequestLogger con colores ANSI por status (verde/amarillo/rojo)
	//   prod → httplog con JSON estructurado (parsable por Loki/Coolify)
	if cfg.Env == "prod" {
		reqLogger := httplog.NewLogger("ahorra-api", httplog.Options{
			JSON:             true,
			LogLevel:         parseLogLevel(cfg.LogLevel),
			Concise:          true,
			MessageFieldName: "msg",
			TimeFieldFormat:  time.RFC3339,
			Tags:             map[string]string{"env": cfg.Env},
		})
		r.Use(httplog.RequestLogger(reqLogger))
	} else {
		r.Use(httpx.DevRequestLogger)
	}

	r.Use(middleware.Timeout(30 * time.Second))

	// Health endpoints fuera de /auth — no requieren autenticación.
	r.Get("/health/live", httpx.LiveHandler)
	r.Get("/health/ready", httpx.ReadyHandler(pool))

	// Auth endpoints.
	authHandler.Mount(r)

	// Households (todas las rutas requieren auth — el mount lo aplica).
	householdsHandler.Mount(r)

	// Banner de startup (tipo Fiber) — solo en dev para no ensuciar logs prod.
	if cfg.Env != "prod" {
		httpx.PrintStartupBanner(os.Stdout, cfg.Env, ":"+cfg.Port, r)
	}

	// ---------- server con graceful shutdown ----------
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Arrancamos el server en una goroutine para poder escuchar señales
	// en el main. Si falla el ListenAndServe (puerto ocupado, etc.),
	// lo mandamos por un canal para salir.
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server escuchando", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Escuchamos SIGINT/SIGTERM. Ctrl-C en dev manda SIGINT; Docker/Coolify
	// manda SIGTERM al parar el container. Ambos inician shutdown limpio.
	stopSig := make(chan os.Signal, 1)
	signal.Notify(stopSig, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		logger.Error("server cayó", "error", err)
		os.Exit(1)
	case sig := <-stopSig:
		logger.Info("señal recibida, iniciando shutdown", "signal", sig.String())
	}

	// 15s para que los requests en vuelo terminen. Si algún handler tarda
	// más (ej: un reporte pesado), lo corta.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown con errores", "error", err)
	}
	logger.Info("server detenido")
}

func parseLogLevel(lvl string) slog.Level {
	switch lvl {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func newLogger() *slog.Logger {
	level := slog.LevelInfo
	if lvl := os.Getenv("LOG_LEVEL"); lvl != "" {
		switch lvl {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
