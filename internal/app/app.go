// Package app wires the API: repositories, services, handlers, the HTTP router
// and the background jobs. cmd/api owns process concerns (config, migrations,
// pool, server, signals); tests build the same App with fakes for the external
// services through Options.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httplog/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/api_go_ahorra/internal/auth"
	"github.com/LucianoR23/api_go_ahorra/internal/auth/google"
	"github.com/LucianoR23/api_go_ahorra/internal/balances"
	"github.com/LucianoR23/api_go_ahorra/internal/categories"
	"github.com/LucianoR23/api_go_ahorra/internal/config"
	"github.com/LucianoR23/api_go_ahorra/internal/creditperiods"
	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/email"
	"github.com/LucianoR23/api_go_ahorra/internal/expenses"
	"github.com/LucianoR23/api_go_ahorra/internal/fxrates"
	"github.com/LucianoR23/api_go_ahorra/internal/goals"
	"github.com/LucianoR23/api_go_ahorra/internal/households"
	"github.com/LucianoR23/api_go_ahorra/internal/httpx"
	"github.com/LucianoR23/api_go_ahorra/internal/incomes"
	"github.com/LucianoR23/api_go_ahorra/internal/insights"
	"github.com/LucianoR23/api_go_ahorra/internal/paymethods"
	"github.com/LucianoR23/api_go_ahorra/internal/push"
	"github.com/LucianoR23/api_go_ahorra/internal/recurringexpenses"
	"github.com/LucianoR23/api_go_ahorra/internal/reports"
	"github.com/LucianoR23/api_go_ahorra/internal/settlements"
	"github.com/LucianoR23/api_go_ahorra/internal/splitrules"
	"github.com/LucianoR23/api_go_ahorra/internal/support"
	"github.com/LucianoR23/api_go_ahorra/internal/users"
)

// EmailSender sends transactional mail (verification, password reset, invites).
type EmailSender interface {
	Configured() bool
	Send(ctx context.Context, to []string, subject, html string) error
}

// GoogleVerifier validates Google ID tokens for /auth/google.
type GoogleVerifier interface {
	Verify(ctx context.Context, idToken string) (*google.Claims, error)
}

// Options replaces the API's external dependencies. Zero values mean the real
// ones, built from Config, exactly as production runs.
type Options struct {
	// NewEmailSender builds a sender for a "From" address. Default: Resend
	// with cfg.ResendAPIKey (no key = not configured, links are logged).
	NewEmailSender func(from string) EmailSender
	// GoogleVerifier defaults to Google's JWKS with cfg.GoogleOAuthClientID.
	GoogleVerifier GoogleVerifier
	// FXHTTPClient is used to fetch exchange rates. Default: 10s timeout.
	FXHTTPClient *http.Client
	// AuthRateLimiter limits the auth endpoints. Default: in-memory per IP.
	AuthRateLimiter auth.RateLimiter
}

// App is the wired API.
type App struct {
	router   *chi.Mux
	starters []func(context.Context) func()
	realtime func(context.Context) func() // insights LISTEN/NOTIFY listener (also in starters)
	jobs     Jobs
}

// Jobs are the units of work behind the background workers, callable
// directly for a given date without starting the schedulers (the test
// harness uses them to run a day's generation deterministically).
type Jobs struct {
	// RecurringExpenses creates the expenses (or drafts) due on date.
	RecurringExpenses func(ctx context.Context, date time.Time) (created, failed int, err error)
	// RecurringIncomes creates the incomes due on date.
	RecurringIncomes func(ctx context.Context, date time.Time) (created int, err error)
	// Insights generates the daily insights of every household at "at".
	Insights func(ctx context.Context, at time.Time) (generated, failed int, err error)
	// MonthlyReports emails the previous month's summary as of "at".
	MonthlyReports func(ctx context.Context, at time.Time)
}

// Jobs returns the background jobs' units of work.
func (a *App) Jobs() Jobs { return a.jobs }

// Router returns the HTTP handler with every route and middleware mounted.
func (a *App) Router() chi.Router { return a.router }

// Start launches the background jobs (FX refresh, recurring generators,
// insights listener and worker, monthly reports) in the order production
// always used, and returns a function that stops them in reverse order.
func (a *App) Start(ctx context.Context) (stop func()) {
	stops := make([]func(), 0, len(a.starters))
	for _, start := range a.starters {
		stops = append(stops, start(ctx))
	}
	return func() {
		for i := len(stops) - 1; i >= 0; i-- {
			stops[i]()
		}
	}
}

// StartRealtime launches only the insights listener that feeds the SSE
// stream, without the workers (which run once on start). For tests; never
// call it together with Start, which already includes the listener.
func (a *App) StartRealtime(ctx context.Context) (stop func()) { return a.realtime(ctx) }

// New builds the App. ctx bounds the startup work (FX cache hydration).
func New(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger, opts Options) (*App, error) {
	if opts.NewEmailSender == nil {
		opts.NewEmailSender = func(from string) EmailSender { return email.NewResendSender(cfg.ResendAPIKey, from) }
	}
	if opts.GoogleVerifier == nil {
		opts.GoogleVerifier = google.NewVerifier(cfg.GoogleOAuthClientID)
	}
	if opts.AuthRateLimiter == nil {
		opts.AuthRateLimiter = auth.NewInMemoryRateLimiter()
	}
	if opts.FXHTTPClient == nil {
		opts.FXHTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	a := &App{}

	userRepo := users.NewRepository(pool)

	tokenIssuer, err := auth.NewTokenIssuer(cfg.JWTSecret, cfg.JWTRefreshSecret)
	if err != nil {
		return nil, fmt.Errorf("app: token issuer: %w", err)
	}

	// paymethods: repo primero, service después. El service de auth lo
	// necesita para crear "Efectivo" automático al registrar un user.
	paymethodsRepo := paymethods.NewRepository(pool)
	paymethodsSvc := paymethods.NewService(paymethodsRepo)

	authSvc := auth.NewService(userRepo, tokenIssuer, paymethodsSvc, logger)
	authMW := auth.NewMiddleware(tokenIssuer, logger)
	// Cablear el lookup de is_superadmin para el middleware RequireSuperadmin.
	// Sin esto, el middleware rechaza todo /admin/* (fail-closed).
	authMW.SetSuperadminChecker(userRepo)
	authHandler := auth.NewHandler(authSvc, authMW, logger, cfg.Env == "prod")

	// Password reset: mismo patrón que invites — token random (32 bytes),
	// guardamos solo el hash, TTL 1h, Resend opcional (en dev se loguea el URL).
	passwordResetRepo := auth.NewPasswordResetRepository(pool)
	passwordResetSender := opts.NewEmailSender(cfg.PasswordResetFromEmail)
	passwordResetSvc := auth.NewPasswordResetService(passwordResetRepo, userRepo, passwordResetSender, logger, cfg.AppBaseURL, cfg.BrandName)
	authHandler.SetPasswordResetService(passwordResetSvc)

	// Rate limiting: in-memory (httprate). Solo se aplica si NO estamos en
	// modo test. En prod es obligatorio por seguridad; en dev puede ser
	// molesto al iterar — pero lo dejamos siempre encendido para detectar
	// problemas antes de llegar a prod.
	authHandler.SetRateLimiter(opts.AuthRateLimiter)

	// Email verification: token por mail al registrarse sin invite. Si el
	// sender no está configurado, el service loguea el URL (útil en dev).
	verificationRepo := auth.NewEmailVerificationRepository(pool)
	verificationSender := opts.NewEmailSender(cfg.VerificationFromEmail)
	verificationSvc := auth.NewEmailVerificationService(verificationRepo, userRepo, verificationSender, logger, cfg.AppBaseURL, cfg.BrandName)
	authHandler.SetEmailVerificationService(verificationSvc)
	authSvc.SetEmailVerifier(verificationSvc)

	// Google OAuth: verifier valida ID tokens contra el JWKS de Google;
	// identityRepo persiste (provider, subject) ↔ user_id. Si
	// GOOGLE_OAUTH_CLIENT_ID no está configurado el endpoint /auth/google
	// existe pero falla con 500 al ser llamado (clientID vacío en Verify).
	authSvc.SetGoogleVerifier(opts.GoogleVerifier)
	authSvc.SetIdentityRepository(users.NewIdentityRepository(pool))

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
	householdsAdminHandler := households.NewAdminHandler(householdsSvc, authMW, logger)

	// Invites: owner invita por email. Genera token, persiste hash y manda
	// el link por Resend (usando INVITE_FROM_EMAIL + APP_BASE_URL). Si el
	// sender no está configurado, el endpoint igual devuelve el acceptURL
	// en el response para que el owner lo comparta a mano (útil en dev).
	inviteSender := opts.NewEmailSender(cfg.InviteFromEmail)
	invitesRepo := households.NewInvitesRepository(pool)
	invitesSvc := households.NewInvitesService(invitesRepo, householdsRepo, userRepo, splitRulesSvc, inviteSender, logger, cfg.AppBaseURL, cfg.BrandName)
	invitesHandler := households.NewInvitesHandler(invitesSvc, authMW, logger)
	// Permite que /auth/register consuma el inviteToken del body.
	authSvc.SetInviteAccepter(invitesSvc)

	// Push: repo + service + handler. Si las VAPID keys no están, el service
	// acepta suscripciones pero no envía nada (no-op). El notifier se cablea
	// a expensesSvc / settlementsSvc / householdsSvc más abajo.
	pushRepo := push.NewRepository(pool)
	pushSvc := push.NewService(pushRepo, push.Config{
		PublicKey:  cfg.VAPIDPublicKey,
		PrivateKey: cfg.VAPIDPrivateKey,
		Subject:    cfg.VAPIDSubject,
	}, logger)
	pushHandler := push.NewHandler(pushSvc, authMW, logger)

	// AccountService: baja de cuenta (soft delete). Requiere pushRepo para
	// limpiar subs del user al borrarlo. Se cablea al authHandler vía setter.
	accountSvc := auth.NewAccountService(userRepo, householdsRepo, pushRepo, logger)
	authHandler.SetAccountService(accountSvc)

	pushAdapter := pushNotifierAdapter{svc: pushSvc}
	householdsSvc.SetNotifier(pushAdapter)
	invitesSvc.SetNotifier(pushAdapter)
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
	fxFetcher := fxrates.NewFetcher(opts.FXHTTPClient)
	fxSvc := fxrates.NewService(fxRepo, fxFetcher, logger)
	if err := fxSvc.Hydrate(ctx); err != nil {
		// No es fatal: si DB está vacía (primer arranque) el worker poblará.
		logger.Warn("fxrates hydrate inicial falló", "error", err)
	}
	fxHandler := fxrates.NewHandler(fxSvc, authMW, logger)
	fxWorker := fxrates.NewWorker(fxSvc, 15*time.Minute, logger)
	a.starters = append(a.starters, fxWorker.Start)

	// expenses: núcleo del producto. Depende de casi todo lo anterior:
	// households (base_currency + miembros para shares), paymethods (ownership
	// + credit_card defaults), creditperiods (overrides mensuales), fxrates
	// (conversión a base currency).
	expensesRepo := expenses.NewRepository(pool)
	expensesSvc := expenses.NewService(expensesRepo, householdsRepo, paymethodsSvc, creditPeriodsRepo, fxSvc, splitRulesSvc)
	expensesSvc.SetNotifier(pushAdapter)
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
	settlementsSvc.SetNotifier(pushAdapter, userRepo)
	settlementsHandler := settlements.NewHandler(settlementsSvc, authMW, householdsMW, logger)

	// incomes: ingresos cobrados + plantillas recurrentes. No tiene shares
	// (la plata entra y ya), pero sí FX (congela rate al recibir igual que
	// expenses). El worker de recurring_incomes se agrega en CP8.4.
	incomesRepo := incomes.NewRepository(pool)
	incomesSvc := incomes.NewService(incomesRepo, householdsRepo, fxSvc, logger)
	incomesHandler := incomes.NewHandler(incomesSvc, authMW, householdsMW, logger)
	// Worker diario 00:30 local — genera ingresos recurrentes.
	incomesWorker := incomes.NewWorker(incomesSvc, 0, 30, logger)
	a.starters = append(a.starters, incomesWorker.Start)

	// recurring_expenses: plantillas de gastos fijos. El generator delega
	// en expensesSvc.Create para heredar toda la lógica (cuotas, shares,
	// FX, credit_card_periods). Worker 00:30 también.
	recurringExpensesRepo := recurringexpenses.NewRepository(pool)
	recurringExpensesSvc := recurringexpenses.NewService(recurringExpensesRepo, householdsRepo, expensesSvc, logger)
	// Wiring cruzado: expenses.ConfirmDraft necesita leer/actualizar la
	// recurrente (threshold + last_amount); recurringExpenses.Stats necesita
	// leer el histórico de expenses. Post-construcción para evitar ciclos.
	expensesSvc.SetRecurringReader(recurringExpensesRepo)
	recurringExpensesSvc.SetExpensesReader(expensesRepo)
	recurringExpensesHandler := recurringexpenses.NewHandler(recurringExpensesSvc, authMW, householdsMW, logger)
	recurringExpensesWorker := recurringexpenses.NewWorker(recurringExpensesSvc, 0, 30, logger)
	a.starters = append(a.starters, recurringExpensesWorker.Start)

	// goals: metas presupuestarias (category_limit / total_limit / savings),
	// scope household o user. El progreso se calcula vivo contra expense_installments
	// e incomes — no cachea nada.
	goalsRepo := goals.NewRepository(pool)
	goalsSvc := goals.NewService(goalsRepo, householdsRepo, fxSvc)
	goalsHandler := goals.NewHandler(goalsSvc, authMW, householdsMW, logger)

	// insights: genera daily_summary, alerts (goals >=80%) y weekly_review
	// (domingos). Worker 01:00 local. Idempotente via UNIQUE parciales.
	// El adapter traduce goalsSvc.ProgressList a la interface que insights usa.
	insightsRepo := insights.NewRepository(pool)
	goalsAdapter := insights.GoalsAdapter(func(ctx context.Context, hhID uuid.UUID, onlyActive *bool, at time.Time) ([]domain.BudgetGoalProgress, error) {
		return goalsSvc.ProgressList(ctx, hhID, goals.ListFilters{OnlyActive: onlyActive}, at)
	})
	insightsSvc := insights.NewService(insightsRepo, householdsRepo, goalsAdapter, logger)
	// pg_notify hook → realtime fan-out a SSE clients vía Hub+Listener.
	insightsSvc.SetNotifier(func(ctx context.Context, ev insights.Event) error {
		return insights.Notify(ctx, pool, ev)
	})
	// Deps para genCreditPeriodReminders: iteramos miembros del hogar y, por
	// cada uno, sus tarjetas activas. Latest period via creditperiods.Repository.
	creditCardsAdapter := insights.CreditCardsAdapter(func(ctx context.Context, hhID uuid.UUID) ([]insights.CreditCardForReminder, error) {
		members, err := householdsRepo.ListMembers(ctx, hhID)
		if err != nil {
			return nil, err
		}
		var out []insights.CreditCardForReminder
		for _, m := range members {
			cards, err := paymethodsSvc.ListCreditCards(ctx, m.User.ID)
			if err != nil {
				return nil, err
			}
			for _, c := range cards {
				if !c.PaymentMethod.IsActive || c.CreditCard == nil {
					continue
				}
				out = append(out, insights.CreditCardForReminder{
					PaymentMethodID: c.PaymentMethod.ID,
					OwnerUserID:     c.PaymentMethod.OwnerUserID,
					CreditCardID:    c.CreditCard.ID,
					Alias:           c.CreditCard.Alias,
				})
			}
		}
		return out, nil
	})
	periodLatestAdapter := insights.PeriodLatestAdapter(func(ctx context.Context, ccID uuid.UUID) (domain.CreditCardPeriod, error) {
		return creditPeriodsRepo.GetLatest(ctx, ccID)
	})
	insightsSvc.SetCreditPeriodDeps(creditCardsAdapter, periodLatestAdapter)
	insightsSvc.SetPushNotifier(pushAdapter)
	insightsSvc.SetInstallmentsDueDep(insights.InstallmentsDueAdapter(listInstallmentsDueOn(pool)))
	insightsHub := insights.NewHub(logger)
	insightsListener := insights.NewListener(pool, insightsHub, logger)
	a.starters = append(a.starters, insightsListener.Start)
	a.realtime = insightsListener.Start
	insightsHandler := insights.NewHandler(insightsSvc, authMW, householdsMW, logger)
	insightsSSE := insights.NewSSEHandler(insightsHub, insightsRepo, tokenIssuer,
		func(ctx context.Context, hh, u uuid.UUID) (bool, error) {
			return householdsRepo.IsMember(ctx, hh, u)
		})
	insightsWorker := insights.NewWorker(insightsSvc, 1, 0, logger)
	a.starters = append(a.starters, insightsWorker.Start)

	// Cablear el insight creator en los services que disparan eventos.
	insightsAdapter := insightsCreatorAdapter{svc: insightsSvc, logger: logger}
	expensesSvc.SetInsightCreator(insightsAdapter)
	invitesSvc.SetInsightCreator(insightsAdapter)
	settlementsSvc.SetInsightCreator(insightsAdapter)

	// reports: agregaciones read-only (monthly + trends + ai-export).
	// No tiene worker por ahora — el email mensual se agrega cuando
	// configuremos RESEND_API_KEY.
	a.jobs = Jobs{
		RecurringExpenses: recurringExpensesSvc.GenerateDue,
		RecurringIncomes:  incomesSvc.GenerateDue,
		Insights:          insightsSvc.GenerateAll,
	}

	reportsRepo := reports.NewRepository(pool)
	reportsSvc := reports.NewService(reportsRepo, householdsRepo, logger)
	reportsHandler := reports.NewHandler(reportsSvc, authMW, householdsMW, logger)

	// Resend sender + worker mensual. Si RESEND_API_KEY no está, el worker
	// arranca igual pero no envía — útil para dev sin credenciales.
	reportsSender := opts.NewEmailSender(cfg.ReportFromEmail)
	reportsWorker := reports.NewWorker(reportsSvc, householdsRepo, reportsSender, cfg.BrandName, 8, 0, logger)
	a.starters = append(a.starters, reportsWorker.Start)
	a.jobs.MonthlyReports = reportsWorker.RunOnce

	// support: proxy al servicio centralizado Lemy Support. Stateless, sin
	// repo ni migración — solo usa userRepo para resolver el email del
	// reporter (el JWT solo trae el userID). Si SOPORTE_API_KEY no está, el
	// módulo queda deshabilitado (endpoints /support/* responden 503).
	supportSvc := support.NewService(support.Config{
		BaseURL: cfg.SupportAPIURL,
		APIKey:  cfg.SupportAPIKey,
	}, userRepo, logger)
	supportHandler := support.NewHandler(supportSvc, authMW, logger)

	// ---------- router ----------
	r := chi.NewRouter()

	// Middlewares globales. El orden importa: primero request-id (todos
	// los logs siguientes lo heredan), después recovery (atrapa panics),
	// después logger. El timeout de handlers (30s) NO va acá: se aplica por
	// grupo más abajo para poder excluir el upload de soporte (ver router).
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	// CORS: permite al frontend llamar al API desde otro origen. Los orígenes
	// válidos vienen de la env var ALLOWED_ORIGINS (separada por coma).
	// AllowCredentials=true es obligatorio para que el refresh token cookie
	// (HttpOnly) viaje en las requests del frontend.
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Household-ID", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

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

	// Health endpoints fuera de /auth — no requieren autenticación. Quedan
	// fuera del grupo con timeout: son instantáneos (probes).
	r.Get("/health/live", httpx.LiveHandler)
	r.Get("/health/ready", httpx.ReadyHandler(pool))

	// El timeout de procesamiento de handlers (30s) se aplica como grupo, no
	// global, para poder EXCLUIR el upload de soporte (POST /support/tickets):
	// los videos (≤20MB) tardan más que 30s en conexiones lentas y necesitan
	// deadlines extendidos. El resto de las rutas conserva el corte de 30s.
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(30 * time.Second))

		// Auth endpoints.
		authHandler.Mount(r)

		// Households (todas las rutas requieren auth — el mount lo aplica).
		householdsHandler.Mount(r)

		// Admin households (/admin/households/*): requiere auth + is_superadmin.
		// Endpoints: listar soft-deleted, restaurar, purgar (DELETE físico con CASCADE).
		householdsAdminHandler.Mount(r)

		// Household invites: /invites/{token} es pública (preview pre-login).
		// Create/List/Revoke/Accept aplican auth dentro del mount.
		invitesHandler.Mount(r)

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

		// Goals (auth + household member).
		goalsHandler.Mount(r)

		// Insights (auth + household member).
		insightsHandler.Mount(r)

		// SSE stream para realtime de insights. Auth via ?access_token= (no
		// header — EventSource del browser no soporta headers custom). Filtra
		// por household + user dentro del Hub.
		insightsSSE.Mount(r)

		// Reports (auth + household member).
		reportsHandler.Mount(r)

		// Push subscriptions + VAPID public key (public key sin auth;
		// subscribe/unsubscribe requieren auth).
		pushHandler.Mount(r)
	})

	// Support (/support/*): proxy a Lemy Support. Requiere auth (el mount lo
	// aplica). No usa X-Household-ID — el soporte es por usuario. Se monta
	// FUERA del grupo con timeout: el handler de upload extiende sus propios
	// deadlines (Read/Write) con ResponseController; las rutas de lectura/
	// mensajes aplican el timeout de 30s dentro de su propio Mount.
	supportHandler.Mount(r)

	a.router = r
	return a, nil
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

// insightsCreatorAdapter adapta insights.Service al shape que expensesSvc,
// invitesSvc y settlementsSvc esperan (sus interfaces locales). Mismo
// patrón que pushNotifierAdapter — los services no importan insights para
// no acoplar el grafo de deps. Errores: log + swallow (los insights son
// secundarios, no debemos abortar el flujo principal por uno).
type insightsCreatorAdapter struct {
	svc    *insights.Service
	logger *slog.Logger
}

func (a insightsCreatorAdapter) CreateSharedExpenseInsight(ctx context.Context, householdID, expenseID, recipientID uuid.UUID, title, body string) {
	a.create(ctx, insights.EventInput{
		HouseholdID: householdID,
		UserID:      &recipientID,
		InsightType: domain.InsightTypeSharedExpense,
		Title:       title,
		Body:        body,
		Severity:    domain.InsightSeverityInfo,
		RefID:       expenseID,
		Metadata: map[string]any{
			"expenseId": expenseID.String(),
			"url":       "/expenses/" + expenseID.String(),
		},
	})
}

func (a insightsCreatorAdapter) CreateRecurringSpikeInsight(ctx context.Context, householdID, expenseID, recipientID uuid.UUID, title, body string) {
	a.create(ctx, insights.EventInput{
		HouseholdID: householdID,
		UserID:      &recipientID,
		InsightType: domain.InsightTypeRecurringSpike,
		Title:       title,
		Body:        body,
		Severity:    domain.InsightSeverityWarning,
		RefID:       expenseID,
		Metadata: map[string]any{
			"expenseId": expenseID.String(),
			"url":       "/expenses/" + expenseID.String(),
		},
	})
}

func (a insightsCreatorAdapter) CreateInviteInsight(ctx context.Context, householdID, inviteID, recipientID uuid.UUID, title, body string) {
	a.create(ctx, insights.EventInput{
		HouseholdID: householdID,
		UserID:      &recipientID,
		InsightType: domain.InsightTypeInvite,
		Title:       title,
		Body:        body,
		Severity:    domain.InsightSeverityInfo,
		RefID:       inviteID,
		Metadata: map[string]any{
			"inviteId": inviteID.String(),
			"url":      "/households/" + householdID.String(),
		},
	})
}

func (a insightsCreatorAdapter) CreateSettlementInsight(ctx context.Context, householdID, settlementID, recipientID uuid.UUID, title, body string) {
	a.create(ctx, insights.EventInput{
		HouseholdID: householdID,
		UserID:      &recipientID,
		InsightType: domain.InsightTypeSettlement,
		Title:       title,
		Body:        body,
		Severity:    domain.InsightSeverityInfo,
		RefID:       settlementID,
		Metadata: map[string]any{
			"settlementId": settlementID.String(),
			"url":          "/balances",
		},
	})
}

func (a insightsCreatorAdapter) create(ctx context.Context, in insights.EventInput) {
	if _, _, err := a.svc.CreateEvent(ctx, in); err != nil {
		a.logger.WarnContext(ctx, "insight event create falló",
			"type", in.InsightType, "ref", in.RefID.String(), "error", err)
	}
}

// pushNotifierAdapter adapta push.Service al shape que expensesSvc,
// settlementsSvc y householdsSvc esperan (sus interfaces locales). Los
// services no importan push directamente para mantener el grafo de deps
// en una sola dirección (main → push).
type pushNotifierAdapter struct {
	svc *push.Service
}

func (a pushNotifierAdapter) NotifyUsers(ctx context.Context, userIDs []uuid.UUID, title, body, url, tag string) {
	a.svc.NotifyUsers(ctx, userIDs, push.Payload{
		Title: title,
		Body:  body,
		URL:   url,
		Tag:   tag,
	})
}

// listInstallmentsDueOn devuelve un closure que el worker de insights usa
// para encontrar cuotas con due_date = X. Query directa contra el pool
// (no necesita sqlc): aprovecha el índice parcial
// idx_installments_due_unpaid ON (due_date) WHERE is_paid = false.
func listInstallmentsDueOn(pool *pgxpool.Pool) func(ctx context.Context, due time.Time) ([]insights.InstallmentDueForReminder, error) {
	return func(ctx context.Context, due time.Time) ([]insights.InstallmentDueForReminder, error) {
		const q = `
			SELECT
				ei.expense_id,
				e.household_id,
				e.created_by,
				e.description,
				ei.installment_number,
				e.installments,
				ei.installment_amount_base::float8,
				e.base_currency,
				ei.due_date
			FROM expense_installments ei
			JOIN expenses e ON e.id = ei.expense_id
			WHERE ei.is_paid = false
			  AND ei.due_date = $1
		`
		rows, err := pool.Query(ctx, q, pgtype.Date{Time: due, Valid: true})
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var out []insights.InstallmentDueForReminder
		for rows.Next() {
			var r insights.InstallmentDueForReminder
			var dueDate pgtype.Date
			if err := rows.Scan(
				&r.ExpenseID,
				&r.HouseholdID,
				&r.CreatedBy,
				&r.Description,
				&r.InstallmentNumber,
				&r.TotalInstallments,
				&r.AmountBase,
				&r.BaseCurrency,
				&dueDate,
			); err != nil {
				return nil, err
			}
			if dueDate.Valid {
				r.DueDate = dueDate.Time
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}
}
