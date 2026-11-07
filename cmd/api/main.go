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

	"github.com/LucianoR23/valuo_api/internal/app"
	"github.com/LucianoR23/valuo_api/internal/config"
	"github.com/LucianoR23/valuo_api/internal/db"
	"github.com/LucianoR23/valuo_api/internal/dbmigrate"
	"github.com/LucianoR23/valuo_api/internal/httpx"
	"github.com/LucianoR23/valuo_api/internal/monitoring"
	"github.com/LucianoR23/valuo_api/migrations"
)

func main() {
	logHandler := newLogHandler()
	logger := slog.New(logHandler)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("no se pudo cargar config", "error", err)
		os.Exit(1)
	}
	logger.Info("config cargada", "env", cfg.Env, "port", cfg.Port)

	// ---------- error monitoring (plan 0.12) ----------
	// Every ERROR log line becomes a scrubbed GlitchTip event. exit flushes
	// the queue first: os.Exit skips deferred calls.
	flush, err := monitoring.Init(monitoring.Options{DSN: cfg.GlitchTipDSN, Environment: cfg.GlitchTipEnvironment})
	if err != nil {
		logger.Error("no se pudo iniciar el monitoreo de errores", "error", err)
		os.Exit(1)
	}
	defer flush()
	exit := func(code int) {
		flush()
		os.Exit(code)
	}
	if cfg.GlitchTipDSN != "" {
		logger = slog.New(monitoring.NewHandler(logHandler, nil))
		logger.Info("monitoreo de errores activo", "environment", cfg.GlitchTipEnvironment)
	}

	// ---------- migrations (opt-in) ----------
	// Run before opening the runtime pool, with the schema-owner role. Any
	// failure (including a dirty DB) aborts startup so the deploy fails and
	// the previous container keeps serving.
	if cfg.MigrateOnStart {
		res, err := dbmigrate.Up(cfg.MigrateDatabaseURL, migrations.FS, logger)
		if err != nil {
			logger.Error("migrations failed", "error", err, "from_version", res.From, "at_version", res.To)
			exit(1)
		}
		logger.Info("migrations applied", "from_version", res.From, "to_version", res.To, "changed", res.Changed)
	}

	// ---------- conexión a DB ----------
	// Usamos un context con timeout solo para el arranque. Para el server
	// corriendo, cada request trae su propio ctx (Go lo provee).
	bootCtx, bootCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer bootCancel()

	pool, err := db.NewPool(bootCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("no se pudo conectar a postgres", "error", err)
		exit(1)
	}
	defer pool.Close()
	logger.Info("postgres conectado")

	// ---------- app (wiring, router, background jobs) ----------
	application, err := app.New(bootCtx, cfg, pool, logger, app.Options{})
	if err != nil {
		logger.Error("no se pudo construir la app", "error", err)
		exit(1)
	}
	stopBackground := application.Start(context.Background())
	defer stopBackground()
	r := application.Router()

	// Banner de startup (tipo Fiber) — solo en dev para no ensuciar logs prod.
	if cfg.Env != "prod" {
		httpx.PrintStartupBanner(os.Stdout, cfg.BrandName, cfg.Env, ":"+cfg.Port, r)
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
		exit(1)
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

func newLogHandler() slog.Handler {
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
	return slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
}
