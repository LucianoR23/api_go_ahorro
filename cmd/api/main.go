package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/config"
	"github.com/LucianoR23/api_go_ahorra/internal/db"
)

func main() {
	logger := newLogger()

	cfg, err := config.Load()
	if err != nil {
		logger.Error("no se pudo cargar config", "error", err)
		os.Exit(1)
	}
	logger.Info("config cargada", "env", cfg.Env, "port", cfg.Port)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("no se pudo conectar a postgres", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	logger.Info("postgres conectado")

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		logger.Error("SELECT 1 falló", "error", err)
		os.Exit(1)
	}
	logger.Info("SELECT 1 ok", "result", one)
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
