package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
)

// Input caps (plan §8.3). Features cut tighter per task; these are the
// last line before a provider.
const (
	MaxTextBytes    = 20 << 10
	MaxMediaBytes   = 5 << 20
	MaxOutputTokens = 2000
	attempts        = 2 // an invalid or transient answer gets one retry
	audioTokensPerS = 32
)

// Config builds a Service.
type Config struct {
	Enabled       bool
	Routes        string // AI_ROUTES
	Prices        string // AI_PRICES
	DisabledTasks []string
	Limits        Limits
	Providers     []Provider
	Location      *time.Location
	Now           func() time.Time
	// Alert is told when the day's spend crosses 50, 80 and 100 percent of
	// the global budget. Optional.
	Alert  func(ctx context.Context, percent int, spent, budget int64)
	Logger *slog.Logger
}

// Service runs AI tasks behind the guardrails.
type Service struct {
	q         *sqlcgen.Queries
	enabled   bool
	disabled  map[string]bool
	routes    map[string]Route
	prices    map[string]Price
	providers map[string]Provider
	limits    Limits
	loc       *time.Location
	now       func() time.Time
	alert     func(ctx context.Context, percent int, spent, budget int64)
	logger    *slog.Logger
}

// New validates the routes against the providers and prices: a route to a
// model without a price would make every call free and skip the budget.
func New(pool *pgxpool.Pool, cfg Config) (*Service, error) {
	routes, err := ParseRoutes(cfg.Routes)
	if err != nil {
		return nil, err
	}
	prices, err := ParsePrices(cfg.Prices)
	if err != nil {
		return nil, err
	}
	providers := map[string]Provider{}
	for _, p := range cfg.Providers {
		providers[p.Name()] = p
	}
	for task, r := range routes {
		if _, ok := providers[r.Provider]; !ok {
			return nil, fmt.Errorf("AI_ROUTES: %s uses provider %q, which is not set up", task, r.Provider)
		}
		if _, ok := prices[r.Model]; !ok {
			return nil, fmt.Errorf("AI_PRICES: no price for %q (used by %s)", r.Model, task)
		}
	}
	disabled := map[string]bool{}
	for _, t := range cfg.DisabledTasks {
		disabled[t] = true
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Location == nil {
		cfg.Location = time.UTC
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{
		q: sqlcgen.New(pool), enabled: cfg.Enabled, disabled: disabled,
		routes: routes, prices: prices, providers: providers, limits: cfg.Limits,
		loc: cfg.Location, now: cfg.Now, alert: cfg.Alert, logger: cfg.Logger,
	}, nil
}

// Limits returns the configured guardrails (for the admin panel).
func (s *Service) Limits() Limits { return s.limits }

// Enabled reports the kill switch.
func (s *Service) Enabled() bool { return s.enabled }

var taskRe = regexp.MustCompile(`^[a-z_]{1,40}$`)

func validChannel(c Channel) bool {
	switch c {
	case ChannelApp, ChannelTelegram, ChannelWhatsApp, ChannelEmail, ChannelSystem:
		return true
	}
	return false
}

func checkRequest(req Request, out any) error {
	if !taskRe.MatchString(string(req.Task)) || !validChannel(req.Channel) {
		return fmt.Errorf("%w: task or channel", ErrBadRequest)
	}
	if len(req.Schema) == 0 || !json.Valid(req.Schema) {
		return fmt.Errorf("%w: schema", ErrBadRequest)
	}
	if req.MaxOutputTokens <= 0 || req.MaxOutputTokens > MaxOutputTokens {
		return fmt.Errorf("%w: max output tokens", ErrBadRequest)
	}
	if len(req.Parts) == 0 {
		return fmt.Errorf("%w: no input", ErrBadRequest)
	}
	text, media := 0, 0
	for _, p := range req.Parts {
		if p.isMedia() {
			media++
			if len(p.Data) > MaxMediaBytes || p.MIME == "" {
				return fmt.Errorf("%w: media", ErrBadRequest)
			}
			continue
		}
		text += len(p.Text)
	}
	if text > MaxTextBytes || media > 1 {
		return fmt.Errorf("%w: input too large", ErrBadRequest)
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("%w: out must be a non-nil pointer", ErrBadRequest)
	}
	return nil
}

// Run checks the guardrails, calls the task's provider and decodes the
// answer into out (a pointer to the task's struct). Unknown fields, a
// failed Validate or a cut answer count as invalid and get one retry.
func (s *Service) Run(ctx context.Context, req Request, out any) error {
	if err := checkRequest(req, out); err != nil {
		return err
	}
	if !s.enabled || s.disabled[string(req.Task)] {
		s.record(ctx, req, Route{}, "disabled", Output{}, 0, 0)
		return ErrDisabled
	}
	u, err := s.q.GetUserByID(ctx, req.UserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnverified
	}
	if err != nil {
		return err
	}
	if !u.EmailVerifiedAt.Valid {
		return ErrUnverified
	}

	now := s.now()
	y, m, d := now.In(s.loc).Date()
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, s.loc)
	uid := req.UserID
	use, err := s.q.AIUserUsageSince(ctx, sqlcgen.AIUserUsageSinceParams{
		MinuteStart: ts(now.Add(-time.Minute)), UserID: &uid, DayStart: ts(dayStart),
	})
	if err != nil {
		return err
	}
	if int(use.CallsMinute) >= s.limits.PerMinute || int(use.CallsDay) >= s.limits.PerDay {
		s.record(ctx, req, Route{}, "rate_limited", Output{}, 0, 0)
		return ErrRateLimited
	}
	if use.CostDay >= s.limits.UserDailyMicroUSD {
		s.record(ctx, req, Route{}, "user_budget", Output{}, 0, 0)
		return ErrUserBudget
	}
	spent, err := s.q.AICostSince(ctx, ts(dayStart))
	if err != nil {
		return err
	}
	if spent >= s.limits.DailyMicroUSD {
		s.record(ctx, req, Route{}, "global_budget", Output{}, 0, 0)
		return ErrGlobalBudget
	}

	route, ok := s.routes[string(req.Task)]
	if !ok {
		route = s.routes[defaultRoute]
	}
	provider, price := s.providers[route.Provider], s.prices[route.Model]
	call := Call{
		Model: route.Model, Effort: route.Effort, Thinking: route.Thinking,
		System: req.System, Parts: req.Parts, Schema: req.Schema, MaxOutputTokens: req.MaxOutputTokens,
	}

	target := reflect.TypeOf(out).Elem()
	for attempt := 1; ; attempt++ {
		cctx, cancel := context.WithTimeout(ctx, callTimeout)
		start := time.Now()
		o, err := provider.Generate(cctx, call)
		latency := time.Since(start)
		cancel()
		if err != nil {
			if errors.Is(err, ErrBadRequest) {
				return err
			}
			status, retry := "provider_error", false
			var pe *ProviderError
			if errors.As(err, &pe) {
				retry = pe.Retryable
				if pe.Timeout {
					status = "timeout"
				}
			}
			s.record(ctx, req, route, status, Output{}, 0, latency)
			s.logger.WarnContext(ctx, "ai: provider call failed", "task", req.Task, "provider", route.Provider, "attempt", attempt, "err", err)
			if retry && attempt < attempts && ctx.Err() == nil {
				continue
			}
			return fmt.Errorf("%w: %w", ErrProvider, err)
		}

		cost := price.Cost(o)
		val := reflect.New(target)
		bad := o.Truncated
		if !bad {
			bad = decode(o.Text, val.Interface()) != nil
		}
		status := "ok"
		if bad {
			status = "invalid_output"
		}
		s.record(ctx, req, route, status, o, cost, latency)
		s.checkBudget(ctx, dayStart, cost)
		if !bad {
			reflect.ValueOf(out).Elem().Set(val.Elem())
			return nil
		}
		s.logger.WarnContext(ctx, "ai: invalid answer", "task", req.Task, "model", route.Model, "attempt", attempt, "truncated", o.Truncated)
		if attempt >= attempts || ctx.Err() != nil {
			return ErrInvalidOutput
		}
	}
}

// decode reads the answer strictly and runs its Validate, if any.
func decode(text string, v any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data")
	}
	if val, ok := v.(Validator); ok {
		return val.Validate()
	}
	return nil
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func clamp32(n int64) int32 {
	if n > 1<<31-1 {
		return 1<<31 - 1
	}
	if n < 0 {
		return 0
	}
	return int32(n)
}

// record writes one ai_usage row. It survives a cancelled request: the
// call happened and costs the same.
func (s *Service) record(ctx context.Context, req Request, r Route, status string, o Output, cost int64, latency time.Duration) {
	uid := req.UserID
	err := s.q.InsertAIUsage(context.WithoutCancel(ctx), sqlcgen.InsertAIUsageParams{
		UserID: &uid, HouseholdID: req.HouseholdID,
		Task: string(req.Task), Channel: string(req.Channel),
		Provider: r.Provider, Model: r.Model, PromptVersion: req.PromptVersion,
		Status:       status,
		InputTokens:  clamp32(int64(o.InputTokens)),
		OutputTokens: clamp32(int64(o.OutputTokens)),
		AudioSeconds: clamp32(int64((o.AudioTokens + audioTokensPerS - 1) / audioTokensPerS)),
		CostMicroUsd: cost,
		LatencyMs:    clamp32(latency.Milliseconds()),
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "ai: record usage", "task", req.Task, "status", status, "err", err)
	}
}

// alertSteps are the budget percentages that send an alert.
var alertSteps = []int{50, 80, 100}

// checkBudget alerts when this call's cost made the day's spend cross a
// step. Two calls crossing at the same instant may both alert; harmless.
func (s *Service) checkBudget(ctx context.Context, dayStart time.Time, cost int64) {
	if s.alert == nil || cost == 0 || s.limits.DailyMicroUSD <= 0 {
		return
	}
	bg := context.WithoutCancel(ctx)
	after, err := s.q.AICostSince(bg, ts(dayStart))
	if err != nil {
		s.logger.ErrorContext(ctx, "ai: budget check", "err", err)
		return
	}
	before := after - cost
	for _, pct := range alertSteps {
		step := s.limits.DailyMicroUSD * int64(pct) / 100
		if before < step && after >= step {
			s.alert(bg, pct, after, s.limits.DailyMicroUSD)
		}
	}
}
