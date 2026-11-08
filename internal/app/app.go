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
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httplog/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LucianoR23/valuo_api/internal/audit"
	"github.com/LucianoR23/valuo_api/internal/auth"
	"github.com/LucianoR23/valuo_api/internal/auth/google"
	"github.com/LucianoR23/valuo_api/internal/balances"
	"github.com/LucianoR23/valuo_api/internal/categories"
	"github.com/LucianoR23/valuo_api/internal/config"
	"github.com/LucianoR23/valuo_api/internal/creditperiods"
	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/email"
	"github.com/LucianoR23/valuo_api/internal/expenses"
	"github.com/LucianoR23/valuo_api/internal/fxrates"
	"github.com/LucianoR23/valuo_api/internal/goals"
	"github.com/LucianoR23/valuo_api/internal/households"
	"github.com/LucianoR23/valuo_api/internal/httpx"
	"github.com/LucianoR23/valuo_api/internal/incomes"
	"github.com/LucianoR23/valuo_api/internal/insights"
	"github.com/LucianoR23/valuo_api/internal/jobs"
	"github.com/LucianoR23/valuo_api/internal/ownership"
	"github.com/LucianoR23/valuo_api/internal/paymethods"
	"github.com/LucianoR23/valuo_api/internal/push"
	"github.com/LucianoR23/valuo_api/internal/recurringexpenses"
	"github.com/LucianoR23/valuo_api/internal/reports"
	"github.com/LucianoR23/valuo_api/internal/settlements"
	"github.com/LucianoR23/valuo_api/internal/splitrules"
	"github.com/LucianoR23/valuo_api/internal/support"
	"github.com/LucianoR23/valuo_api/internal/users"
	"github.com/LucianoR23/valuo_api/internal/waitlist"
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
	// TurnstileVerifier checks waitlist captcha tokens. Default: Cloudflare
	// with cfg.TurnstileSecretKey (none: skipped in dev, refused in prod).
	TurnstileVerifier waitlist.TurnstileVerifier
	// WaitlistLimiter limits POST /waitlist. Default: 5 per 10 min per IP.
	WaitlistLimiter func(http.Handler) http.Handler
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
	MonthlyReports func(ctx context.Context, at time.Time) error
}

// Jobs returns the background jobs' units of work.
func (a *App) Jobs() Jobs { return a.jobs }

// Router returns the HTTP handler with every route and middleware mounted.
func (a *App) Router() chi.Router { return a.router }

// Start launches the insights listener and the River job runner (FX
// refresh, recurring generators, insights, monthly reports, retention
// cleanups; plan 0.8), and returns a function that stops them in reverse
// order.
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
	dates.SetLocation(cfg.Location) // nil keeps the default (Buenos Aires)
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

	tokenIssuer, err := auth.NewTokenIssuer(cfg.JWTSecret)
	if err != nil {
		return nil, fmt.Errorf("app: token issuer: %w", err)
	}

	// paymethods: repo primero, service después. El service de auth lo
	// necesita para crear "Efectivo" automático al registrar un user.
	paymethodsRepo := paymethods.NewRepository(pool)
	paymethodsSvc := paymethods.NewService(paymethodsRepo)

	sessions := auth.NewSessionStore(pool)
	authSvc := auth.NewService(pool, userRepo, tokenIssuer, sessions, paymethodsSvc, logger)
	authMW := auth.NewMiddleware(tokenIssuer, logger)
	// Cablear el lookup de is_superadmin para el middleware RequireSuperadmin.
	// Sin esto, el middleware rechaza todo /admin/* (fail-closed).
	authMW.SetSuperadminChecker(userRepo)
	authMW.SetUserLookup(userRepo)
	authHandler := auth.NewHandler(authSvc, authMW, logger, cfg.Env == "prod")

	// Password reset: mismo patrón que invites — token random (32 bytes),
	// guardamos solo el hash, TTL 1h, Resend opcional (en dev se loguea el URL).
	passwordResetRepo := auth.NewPasswordResetRepository(pool)
	passwordResetSender := opts.NewEmailSender(cfg.PasswordResetFromEmail)
	passwordResetSvc := auth.NewPasswordResetService(passwordResetRepo, userRepo, passwordResetSender, logger, cfg.AppBaseURL, cfg.BrandName)
	passwordResetSvc.SetSessions(sessions)
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
	authSvc.SetPersonalSpaces(householdsSvc)
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

	// Security audit log (plan 0.17): sign-ins, password/email, sessions,
	// household membership and superadmin actions; new-device e-mail; daily
	// retention (IP 7 days, events 12 months).
	auditRec := audit.NewRecorder(pool, logger)
	auditRec.SetNewDeviceAlerts(audit.NewNewDeviceAlerts(verificationSender, userRepo, cfg.BrandName, cfg.AppBaseURL, logger))
	authHandler.SetAudit(auditRec)
	authHandler.SetCookieOrigins(cfg.AllowedOrigins)
	householdsHandler.SetAudit(auditRec)
	householdsAdminHandler.SetAudit(auditRec)
	invitesHandler.SetAudit(auditRec)
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
		ExtraHosts: cfg.PushExtraHosts,
	}, logger)
	pushHandler := push.NewHandler(pushSvc, authMW, logger)

	// AccountService: account deletion (soft delete), one transaction in
	// userRepo. Wired into authHandler through a setter.
	accountSvc := auth.NewAccountService(userRepo, logger)
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

	// fxrates: tasas ARS/USD/EUR. Hidratamos caché desde DB al arrancar; el
	// job fx_refresh refresca cada 15min (bluelytics).
	fxRepo := fxrates.NewRepository(pool)
	fxFetcher := fxrates.NewFetcher(opts.FXHTTPClient)
	fxSvc := fxrates.NewService(fxRepo, fxFetcher, logger)
	if err := fxSvc.Hydrate(ctx); err != nil {
		// No es fatal: si DB está vacía (primer arranque) el worker poblará.
		logger.Warn("fxrates hydrate inicial falló", "error", err)
	}
	fxHandler := fxrates.NewHandler(fxSvc, authMW, logger)

	// expenses: núcleo del producto. Depende de casi todo lo anterior:
	// households (base_currency + miembros para shares), paymethods (ownership
	// + credit_card defaults), creditperiods (overrides mensuales), fxrates
	// (conversión a base currency).
	expensesRepo := expenses.NewRepository(pool)
	ownChecker := ownership.NewChecker(pool) // plan 0.6: client ids belong where they claim
	expensesSvc := expenses.NewService(expensesRepo, householdsRepo, paymethodsSvc, creditPeriodsRepo, fxSvc, splitRulesSvc)
	expensesSvc.SetOwnership(ownChecker)
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
	// expenses).
	incomesRepo := incomes.NewRepository(pool)
	incomesSvc := incomes.NewService(incomesRepo, householdsRepo, fxSvc, logger)
	incomesSvc.SetOwnership(ownChecker)
	incomesHandler := incomes.NewHandler(incomesSvc, authMW, householdsMW, logger)

	// recurring_expenses: plantillas de gastos fijos. El generator delega
	// en expensesSvc.Create para heredar toda la lógica (cuotas, shares,
	// FX, credit_card_periods).
	recurringExpensesRepo := recurringexpenses.NewRepository(pool)
	recurringExpensesSvc := recurringexpenses.NewService(recurringExpensesRepo, householdsRepo, expensesSvc, logger)
	recurringExpensesSvc.SetOwnership(ownChecker)
	// Wiring cruzado: expenses.ConfirmDraft necesita leer/actualizar la
	// recurrente (threshold + last_amount); recurringExpenses.Stats necesita
	// leer el histórico de expenses. Post-construcción para evitar ciclos.
	expensesSvc.SetRecurringReader(recurringExpensesRepo)
	recurringExpensesSvc.SetExpensesReader(expensesRepo)
	recurringExpensesHandler := recurringexpenses.NewHandler(recurringExpensesSvc, authMW, householdsMW, logger)

	// goals: metas presupuestarias (category_limit / total_limit / savings),
	// scope household o user. El progreso se calcula vivo contra expense_installments
	// e incomes — no cachea nada.
	goalsRepo := goals.NewRepository(pool)
	goalsSvc := goals.NewService(goalsRepo, householdsRepo, fxSvc)
	goalsSvc.SetOwnership(ownChecker)
	goalsHandler := goals.NewHandler(goalsSvc, authMW, householdsMW, logger)

	// insights: genera daily_summary, alerts (goals >=80%) y weekly_review
	// (domingos). Job diario 01:00 local. Idempotente via UNIQUE parciales.
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
	// Deps para genCreditPeriodReminders: one query for the household's
	// active cards with their latest period (plan 0.14).
	creditCardsAdapter := insights.CreditCardsAdapter(func(ctx context.Context, hhID uuid.UUID) ([]insights.CreditCardForReminder, error) {
		cards, err := creditPeriodsRepo.ListActiveCardsWithLatestPeriodByHousehold(ctx, hhID)
		if err != nil {
			return nil, err
		}
		out := make([]insights.CreditCardForReminder, len(cards))
		for i, c := range cards {
			out[i] = insights.CreditCardForReminder{
				PaymentMethodID:   c.PaymentMethodID,
				OwnerUserID:       c.OwnerUserID,
				CreditCardID:      c.CreditCardID,
				Alias:             c.Alias,
				LatestPeriodYM:    c.Latest.PeriodYM,
				LatestClosingDate: c.Latest.ClosingDate,
			}
		}
		return out, nil
	})
	insightsSvc.SetCreditPeriodDeps(creditCardsAdapter)
	insightsSvc.SetPushNotifier(pushAdapter)
	insightsSvc.SetInstallmentsDueDep(insights.InstallmentsDueAdapter(listInstallmentsDueOn(pool)))
	insightsHub := insights.NewHub(logger)
	insightsListener := insights.NewListener(pool, insightsHub, logger)
	a.starters = append(a.starters, insightsListener.Start)
	a.realtime = insightsListener.Start
	streamTickets := insights.NewTicketStore()
	insightsHandler := insights.NewHandler(insightsSvc, streamTickets, authMW, householdsMW, logger)
	insightsHandler.AllowArbitraryDate(cfg.Env == "dev")
	insightsSSE := insights.NewSSEHandler(insightsHub, insightsRepo, streamTickets,
		func(ctx context.Context, hh, u uuid.UUID) (bool, error) {
			return householdsRepo.IsMember(ctx, hh, u)
		})

	// Cablear el insight creator en los services que disparan eventos.
	insightsAdapter := insightsCreatorAdapter{svc: insightsSvc, logger: logger}
	expensesSvc.SetInsightCreator(insightsAdapter)
	invitesSvc.SetInsightCreator(insightsAdapter)
	settlementsSvc.SetInsightCreator(insightsAdapter)

	// reports: agregaciones read-only (monthly + trends + ai-export).
	a.jobs = Jobs{
		RecurringExpenses: recurringExpensesSvc.GenerateDue,
		RecurringIncomes:  incomesSvc.GenerateDue,
		Insights:          insightsSvc.GenerateAll,
	}

	reportsRepo := reports.NewRepository(pool)
	reportsSvc := reports.NewService(reportsRepo, householdsRepo, logger)
	reportsHandler := reports.NewHandler(reportsSvc, authMW, householdsMW, logger)

	// Resend sender for the monthly email. Without RESEND_API_KEY the job
	// runs and sends nothing (dev without credentials).
	reportsSender := opts.NewEmailSender(cfg.ReportFromEmail)
	reportsMailer := reports.NewMailer(reportsSvc, householdsRepo, reportsSender, cfg.BrandName, logger)
	a.jobs.MonthlyReports = reportsMailer.SendMonthly

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
	// Client IP without trusting forgeable headers (plan 0.6): X-Forwarded-For
	// only from TRUSTED_PROXIES (Traefik), CF-Connecting-IP only from
	// Cloudflare edges. Validated in config.Load.
	trustedProxies, _ := httpx.ParsePrefixes(cfg.TrustedProxies)
	cloudflareEdges, _ := httpx.ParsePrefixes(cfg.CloudflareRanges)
	r.Use(httpx.RealIP(trustedProxies, cloudflareEdges))
	r.Use(httpx.Recoverer(logger)) // logs through slog, so panics reach monitoring (plan 0.12)
	r.Use(httpx.SecurityHeaders)   // plan 0.16

	// CORS: permite al frontend llamar al API desde otro origen. Los orígenes
	// válidos vienen de la env var ALLOWED_ORIGINS (separada por coma).
	// AllowCredentials=true es obligatorio para que el refresh token cookie
	// (HttpOnly) viaje en las requests del frontend.
	// Waitlist (plan B.5): the landing calls it cross-origin without
	// cookies; its own CORS (WAITLIST_ALLOWED_ORIGINS, no credentials)
	// replaces the app's on /waitlist paths.
	waitlistSender := opts.NewEmailSender(cfg.WaitlistFromEmail)
	if opts.TurnstileVerifier == nil && cfg.TurnstileSecretKey != "" {
		opts.TurnstileVerifier = waitlist.NewCloudflareTurnstile(cfg.TurnstileSecretKey)
	}
	waitlistSvc := waitlist.NewService(pool, waitlist.Config{
		SiteURL:        cfg.WaitlistSiteURL,
		AllowedHosts:   hostsOf(cfg.WaitlistAllowedOrigins),
		APIPublicURL:   cfg.APIPublicURL,
		Brand:          cfg.WaitlistBrandName,
		ContactEmail:   cfg.WaitlistContactEmail,
		RequireCaptcha: cfg.Env == "prod",
		UnsubscribeKey: []byte("waitlist:" + cfg.JWTSecret),
	}, opts.TurnstileVerifier, headerSender{waitlistSender}, logger)
	waitlistHandler := waitlist.NewHandler(waitlistSvc, cfg.WaitlistAllowedOrigins, opts.WaitlistLimiter, logger)
	// Background jobs on River (plan 0.8). Last starter, so it stops first.
	runner, err := jobs.New(pool, jobs.Units{
		FXRefresh:         fxSvc.Refresh,
		RecurringIncomes:  a.jobs.RecurringIncomes,
		RecurringExpenses: a.jobs.RecurringExpenses,
		Insights:          a.jobs.Insights,
		MonthlyReports:    a.jobs.MonthlyReports,
		AuditRetention:    func(ctx context.Context) error { return audit.ApplyRetention(ctx, pool, logger) },
		WaitlistCleanup:   waitlistSvc.Cleanup,
	}, logger)
	if err != nil {
		return nil, err
	}
	a.starters = append(a.starters, runner.Start)
	r.Use(byPathPrefix("/waitlist", waitlistHandler.CORS()))

	r.Use(skipPathPrefix("/waitlist", cors.Handler(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Household-ID", "X-Request-ID"},
		ExposedHeaders:   []string{"X-Request-ID", "Retry-After"},
		AllowCredentials: true,
		MaxAge:           300,
	})))

	// Logger de requests:
	//   dev  → DevRequestLogger con colores ANSI por status (verde/amarillo/rojo)
	//   prod → httplog con JSON estructurado (parsable por Loki/Coolify)
	// Query strings never reach the logs (they can carry tickets, search
	// text, e-mail addresses); see httpx.RedactQueryInLogs.
	r.Use(httpx.RedactQueryInLogs)
	if cfg.Env == "prod" {
		reqLogger := httplog.NewLogger("api", httplog.Options{
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
		r.Use(httpx.LimitBody(httpx.MaxJSONBody))

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

		// Reports (auth + household member).
		reportsHandler.Mount(r)

		// Push subscriptions + VAPID public key (public key sin auth;
		// subscribe/unsubscribe requieren auth).
		pushHandler.Mount(r)
	})

	// Waitlist (plan B.5): public, its own CORS and limits.
	waitlistHandler.Mount(r)
	waitlist.NewAdminHandler(waitlistSvc, authMW, auditRec, logger).Mount(r)

	// SSE stream of new insights: outside the 30 s timeout group (it lives as
	// long as the page is open; the handler pushes the write deadline forward
	// on each write). Opened with a single-use ticket from
	// POST /insights/stream-ticket, never with the access token (plan 0.9).
	insightsSSE.Mount(r)

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

func (a pushNotifierAdapter) NotifyUsersWithAmounts(ctx context.Context, userIDs []uuid.UUID, title, body, bodyWithAmounts, url, tag string) {
	a.svc.NotifyUsers(ctx, userIDs, push.Payload{
		Title:           title,
		Body:            body,
		BodyWithAmounts: bodyWithAmounts,
		URL:             url,
		Tag:             tag,
	})
}

// listInstallmentsDueOn returns the unpaid installments of a household due
// on a date, for the due-soon reminder. Direct query on the pool; it uses
// idx_installments_household_due_unpaid (migration 000030).
func listInstallmentsDueOn(pool *pgxpool.Pool) func(ctx context.Context, householdID uuid.UUID, due time.Time) ([]insights.InstallmentDueForReminder, error) {
	return func(ctx context.Context, householdID uuid.UUID, due time.Time) ([]insights.InstallmentDueForReminder, error) {
		const q = `
			SELECT
				ei.expense_id,
				e.created_by,
				e.description,
				ei.installment_number,
				e.installments,
				ei.installment_amount_base::float8,
				e.base_currency,
				ei.due_date
			FROM expense_installments ei
			JOIN expenses e ON e.id = ei.expense_id
			WHERE ei.household_id = $1
			  AND ei.is_paid = false
			  AND ei.due_date = $2
			  AND e.status = 'confirmed'
		`
		rows, err := pool.Query(ctx, q, householdID, pgtype.Date{Time: due, Valid: true})
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

// byPathPrefix applies mw only to requests under prefix.
func byPathPrefix(prefix string, mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, prefix) {
				wrapped.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// skipPathPrefix applies mw to every request except those under prefix.
func skipPathPrefix(prefix string, mw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, prefix) {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	}
}

// hostsOf: the hostnames of origins like "https://valuo.ar" (Turnstile
// reports the hostname the widget ran on).
func hostsOf(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, o := range origins {
		if u, err := url.Parse(o); err == nil && u.Hostname() != "" {
			out = append(out, u.Hostname())
		}
	}
	return out
}

// headerSender adapts an EmailSender for the waitlist, which needs extra
// headers (List-Unsubscribe). Senders without header support send without.
type headerSender struct{ EmailSender }

func (h headerSender) SendWithHeaders(ctx context.Context, to []string, subject, html string, headers map[string]string) error {
	if hs, ok := h.EmailSender.(interface {
		SendWithHeaders(ctx context.Context, to []string, subject, html string, headers map[string]string) error
	}); ok {
		return hs.SendWithHeaders(ctx, to, subject, html, headers)
	}
	return h.Send(ctx, to, subject, html)
}
