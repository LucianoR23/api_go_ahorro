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
	"github.com/LucianoR23/api_go_ahorra/internal/balances"
	"github.com/LucianoR23/api_go_ahorra/internal/categories"
	"github.com/LucianoR23/api_go_ahorra/internal/config"
	"github.com/LucianoR23/api_go_ahorra/internal/creditperiods"
	"github.com/LucianoR23/api_go_ahorra/internal/db"
	"github.com/LucianoR23/api_go_ahorra/internal/expenses"
	"github.com/LucianoR23/api_go_ahorra/internal/fxrates"
	"github.com/LucianoR23/api_go_ahorra/internal/households"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
	"github.com/LucianoR23/api_go_ahorra/internal/incomes"
	"github.com/LucianoR23/api_go_ahorra/internal/paymethods"
	"github.com/LucianoR23/api_go_ahorra/internal/recurringexpenses"
	"github.com/LucianoR23/api_go_ahorra/internal/settlements"
	"github.com/LucianoR23/api_go_ahorra/internal/splitrules"
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

	// paymethods: repo primero, service después. El service de auth lo
	// necesita para crear "Efectivo" automático al registrar un user.
	paymethodsRepo := paymethods.NewRepository(pool)
	paymethodsSvc := paymethods.NewService(paymethodsRepo)

	authSvc := auth.NewService(userRepo, tokenIssuer, paymethodsSvc, logger)
	authMW := auth.NewMiddleware(tokenIssuer, logger)
	authHandler := auth.NewHandler(authSvc, authMW, logger, cfg.Env == "prod")

	// categories: repo se construye antes que households porque households.Service
	// lo recibe como categoriesSeeder (bootstrap de las 7 categorías default
	// al crear un hogar, dentro de la misma tx).
	categoriesRepo := categories.NewRepository(pool)
	categoriesSvc := categories.NewService(categoriesRepo)

	// splitrules: reglas de peso por miembro. Se inyecta en households.Service
	// para seedear weight=1.0 al owner en Create y al invitado en AddMember,
	// dentro de la misma tx que la membresía (atómico).
	splitRulesRepo := splitrules.NewRepository(pool)
	householdsRepo := households.NewRepository(pool)
	splitRulesSvc := splitrules.NewService(splitRulesRepo, householdsRepo)
	householdsSvc := households.NewService(householdsRepo, userRepo, categoriesRepo, splitRulesSvc)
	householdsMW := households.NewMiddleware(householdsRepo, logger)
	householdsHandler := households.NewHandler(householdsSvc, authMW, logger)
	splitRulesHandler := splitrules.NewHandler(splitRulesSvc, authMW, householdsMW, logger)

	paymethodsHandler := paymethods.NewHandler(paymethodsSvc, authMW, logger)
	categoriesHandler := categories.NewHandler(categoriesSvc, authMW, householdsMW, logger)

	// creditperiods: montado bajo /payment-methods/{id}/credit-card/periods/*.
	// Reusa paymethodsSvc para validar ownership y resolver credit_card_id.
	creditPeriodsRepo := creditperiods.NewRepository(pool)
	creditPeriodsSvc := creditperiods.NewService(creditPeriodsRepo, paymethodsSvc)
	creditPeriodsHandler := creditperiods.NewHandler(creditPeriodsSvc, authMW, logger)

	// fxrates: tasas ARS/USD/EUR. Hidratamos caché desde DB al arrancar y
	// levantamos un worker que refresca cada 15min (bluelytics).
	fxRepo := fxrates.NewRepository(pool)
	fxFetcher := fxrates.NewFetcher(&http.Client{Timeout: 10 * time.Second})
	fxSvc := fxrates.NewService(fxRepo, fxFetcher, logger)
	if err := fxSvc.Hydrate(bootCtx); err != nil {
		// No es fatal: si DB está vacía (primer arranque) el worker poblará.
		logger.Warn("fxrates hydrate inicial falló", "error", err)
	}
	fxHandler := fxrates.NewHandler(fxSvc, authMW, logger)
	fxWorker := fxrates.NewWorker(fxSvc, 15*time.Minute, logger)
	stopFxWorker := fxWorker.Start(context.Background())
	defer stopFxWorker()

	// expenses: núcleo del producto. Depende de casi todo lo anterior:
	// households (base_currency + miembros para shares), paymethods (ownership
	// + credit_card defaults), creditperiods (overrides mensuales), fxrates
	// (conversión a base currency).
	expensesRepo := expenses.NewRepository(pool)
	expensesSvc := expenses.NewService(expensesRepo, householdsRepo, paymethodsSvc, creditPeriodsRepo, fxSvc, splitRulesSvc)
	expensesHandler := expenses.NewHandler(expensesSvc, authMW, householdsMW, logger)

	// balances: cálculo on-demand de deudas (shares billed - settlements).
	// No tiene tablas propias: lee de expenses y settlements.
	balancesRepo := balances.NewRepository(pool)
	balancesSvc := balances.NewService(balancesRepo)
	balancesHandler := balances.NewHandler(balancesSvc, authMW, householdsMW, logger)

	// settlements: pagos entre miembros. Valida amount <= deuda_actual usando
	// balancesSvc.PairNet. No toca payment_methods (la plata se movió afuera).
	settlementsRepo := settlements.NewRepository(pool)
	settlementsSvc := settlements.NewService(settlementsRepo, householdsRepo, balancesSvc)
	settlementsHandler := settlements.NewHandler(settlementsSvc, authMW, householdsMW, logger)

	// incomes: ingresos cobrados + plantillas recurrentes. No tiene shares
	// (la plata entra y ya), pero sí FX (congela rate al recibir igual que
	// expenses). El worker de recurring_incomes se agrega en CP8.4.
	incomesRepo := incomes.NewRepository(pool)
	incomesSvc := incomes.NewService(incomesRepo, householdsRepo, fxSvc)
	incomesHandler := incomes.NewHandler(incomesSvc, authMW, householdsMW, logger)
	// Worker diario 00:30 local — genera ingresos recurrentes.
	incomesWorker := incomes.NewWorker(incomesSvc, 0, 30, logger)
	stopIncomesWorker := incomesWorker.Start(context.Background())
	defer stopIncomesWorker()

	// recurring_expenses: plantillas de gastos fijos. El generator delega
	// en expensesSvc.Create para heredar toda la lógica (cuotas, shares,
	// FX, credit_card_periods). Worker 00:30 también.
	recurringExpensesRepo := recurringexpenses.NewRepository(pool)
	recurringExpensesSvc := recurringexpenses.NewService(recurringExpensesRepo, householdsRepo, expensesSvc, logger)
	recurringExpensesHandler := recurringexpenses.NewHandler(recurringExpensesSvc, authMW, householdsMW, logger)
	recurringExpensesWorker := recurringexpenses.NewWorker(recurringExpensesSvc, 0, 30, logger)
	stopRecurringExpensesWorker := recurringExpensesWorker.Start(context.Background())
	defer stopRecurringExpensesWorker()

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

	// Payment methods / banks / credit cards (auth requerido).
	paymethodsHandler.Mount(r)

	// Credit card periods (auth requerido, ownership validado en service).
	creditPeriodsHandler.Mount(r)

	// Expenses (auth + household member requerido).
	expensesHandler.Mount(r)

	// Categories (auth + household member requerido).
	categoriesHandler.Mount(r)

	// Exchange rates (auth requerido).
	fxHandler.Mount(r)

	// Balances (auth + household member requerido).
	balancesHandler.Mount(r)

	// Settlements (auth + household member requerido).
	settlementsHandler.Mount(r)

	// Split rules (auth + household member; Update valida owner en service).
	splitRulesHandler.Mount(r)

	// Incomes + recurring-incomes + /totals/income (auth + household member).
	incomesHandler.Mount(r)

	// Recurring expenses (auth + household member).
	recurringExpensesHandler.Mount(r)

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
