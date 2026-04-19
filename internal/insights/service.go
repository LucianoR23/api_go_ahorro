package insights

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// householdsLister: el worker necesita iterar todos los hogares.
type householdsLister interface {
	ListAllIDs(ctx context.Context) ([]uuid.UUID, error)
}

// goalsProgress: para generar alerts sobre goals >80% consumido.
type goalsProgress interface {
	ProgressList(ctx context.Context, householdID uuid.UUID, f goalsListFilters, at time.Time) ([]domain.BudgetGoalProgress, error)
}

// goalsListFilters: duplicamos el tipo mínimo que necesitamos para no
// importar goals y romper el DAG (insights ← goals, y goals no importa
// insights — está bien, pero el tipo hay que pasarlo). En la wiring de main
// se usa un adapter chiquito.
type goalsListFilters struct {
	OnlyActive *bool
}

// GoalsAdapter: para main.go — envuelve goals.Service para satisfacer la
// interface local sin que insights importe goals directamente.
type GoalsAdapter func(ctx context.Context, householdID uuid.UUID, onlyActive *bool, at time.Time) ([]domain.BudgetGoalProgress, error)

func (g GoalsAdapter) ProgressList(ctx context.Context, householdID uuid.UUID, f goalsListFilters, at time.Time) ([]domain.BudgetGoalProgress, error) {
	return g(ctx, householdID, f.OnlyActive, at)
}

type Service struct {
	repo       *Repository
	households householdsLister
	goals      goalsProgress
	logger     *slog.Logger
}

func NewService(repo *Repository, households householdsLister, goals goalsProgress, logger *slog.Logger) *Service {
	return &Service{repo: repo, households: households, goals: goals, logger: logger}
}

// ===================== lectura / escritura =====================

func (s *Service) List(ctx context.Context, householdID uuid.UUID, f ListFilters) ([]domain.DailyInsight, error) {
	return s.repo.ListByHousehold(ctx, householdID, f)
}

func (s *Service) CountUnread(ctx context.Context, householdID uuid.UUID, userID *uuid.UUID) (int64, error) {
	return s.repo.CountUnread(ctx, householdID, userID)
}

func (s *Service) Get(ctx context.Context, householdID, id uuid.UUID) (domain.DailyInsight, error) {
	ins, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.DailyInsight{}, err
	}
	if ins.HouseholdID != householdID {
		return domain.DailyInsight{}, domain.ErrNotFound
	}
	return ins, nil
}

func (s *Service) MarkRead(ctx context.Context, householdID, id uuid.UUID) error {
	ins, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if ins.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return s.repo.MarkRead(ctx, id)
}

func (s *Service) MarkAllRead(ctx context.Context, householdID uuid.UUID, userID *uuid.UUID) error {
	return s.repo.MarkAllRead(ctx, householdID, userID)
}

func (s *Service) Delete(ctx context.Context, householdID, id uuid.UUID) error {
	ins, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if ins.HouseholdID != householdID {
		return domain.ErrNotFound
	}
	return s.repo.Delete(ctx, id)
}

// ===================== generación =====================

// GenerateAll: worker API. Itera todos los hogares y corre los generadores
// del día. Idempotente gracias a UNIQUE(household, user, date, type) + ON
// CONFLICT DO NOTHING. Devuelve (creados, fallados). Nunca aborta por un
// hogar — loguea y sigue.
func (s *Service) GenerateAll(ctx context.Context, at time.Time) (int, int, error) {
	ids, err := s.households.ListAllIDs(ctx)
	if err != nil {
		return 0, 0, err
	}
	created, failed := 0, 0
	for _, hhID := range ids {
		c, f := s.generateForHousehold(ctx, hhID, at)
		created += c
		failed += f
	}
	return created, failed, nil
}

// GenerateForHousehold: versión pública para testing manual o endpoint admin.
func (s *Service) GenerateForHousehold(ctx context.Context, householdID uuid.UUID, at time.Time) (int, int) {
	return s.generateForHousehold(ctx, householdID, at)
}

func (s *Service) generateForHousehold(ctx context.Context, householdID uuid.UUID, at time.Time) (int, int) {
	created, failed := 0, 0

	// 1. daily_summary — ayer.
	if c, err := s.genDailySummary(ctx, householdID, at); err != nil {
		failed++
		s.logWarn(ctx, "daily_summary falló", "householdId", householdID.String(), "error", err)
	} else if c {
		created++
	}

	// 2. alerts — goals activos >=80%.
	n, nf := s.genGoalAlerts(ctx, householdID, at)
	created += n
	failed += nf

	// 3. weekly_review — solo domingos.
	if at.Weekday() == time.Sunday {
		if c, err := s.genWeeklyReview(ctx, householdID, at); err != nil {
			failed++
			s.logWarn(ctx, "weekly_review falló", "householdId", householdID.String(), "error", err)
		} else if c {
			created++
		}
	}

	return created, failed
}

// ---------- generators ----------

// genDailySummary: un insight por hogar para `at`. Título corto con total
// de ayer; body con conteos + total facturado del mes.
func (s *Service) genDailySummary(ctx context.Context, householdID uuid.UUID, at time.Time) (bool, error) {
	yesterday := at.AddDate(0, 0, -1)
	yStart := dayStart(yesterday)
	yEnd := yStart
	spent, err := s.repo.SumSpentAt(ctx, householdID, yStart, yEnd)
	if err != nil {
		return false, err
	}
	counts, err := s.repo.CountSpentAt(ctx, householdID, yStart, yEnd)
	if err != nil {
		return false, err
	}
	monthStart, monthEnd := monthRange(at)
	due, err := s.repo.SumDue(ctx, householdID, monthStart, monthEnd)
	if err != nil {
		return false, err
	}

	var title, body string
	if counts.Total == 0 {
		title = "Ayer no hubo movimientos"
		body = fmt.Sprintf("No registraste gastos ayer. Este mes te vienen a cobrar %s.", formatMoney(due))
	} else {
		title = fmt.Sprintf("Ayer gastaste %s", formatMoney(spent))
		body = fmt.Sprintf("%d transaccion(es) en %d categoría(s). Este mes te vienen a cobrar %s en total.",
			counts.Total, counts.DistinctCategories, formatMoney(due))
	}

	_, created, err := s.repo.Create(ctx, CreateParams{
		HouseholdID: householdID,
		InsightDate: dayStart(at),
		InsightType: domain.InsightTypeDailySummary,
		Title:       title,
		Body:        body,
		Severity:    domain.InsightSeverityInfo,
		Metadata: map[string]any{
			"yesterday_spent": spent,
			"yesterday_count": counts.Total,
			"month_due":       due,
		},
	})
	return created, err
}

// genGoalAlerts: un insight por goal activo con >=80% consumido (limits) o
// <50% del target ahorrado después del día 20 del mes (savings). Severidad
// según qué tan pasado esté.
func (s *Service) genGoalAlerts(ctx context.Context, householdID uuid.UUID, at time.Time) (int, int) {
	active := true
	progs, err := s.goals.ProgressList(ctx, householdID, goalsListFilters{OnlyActive: &active}, at)
	if err != nil {
		s.logWarn(ctx, "goals progress falló", "householdId", householdID.String(), "error", err)
		return 0, 1
	}
	created, failed := 0, 0
	for _, p := range progs {
		title, body, severity, ok := alertText(p, at)
		if !ok {
			continue
		}
		var userID *uuid.UUID
		if p.Goal.Scope == domain.GoalScopeUser {
			userID = p.Goal.UserID
		}
		_, c, err := s.repo.Create(ctx, CreateParams{
			HouseholdID: householdID,
			UserID:      userID,
			InsightDate: dayStart(at),
			InsightType: domain.InsightTypeAlert,
			Title:       title,
			Body:        body,
			Severity:    severity,
			Metadata: map[string]any{
				"goal_id":       p.Goal.ID.String(),
				"goal_type":     p.Goal.GoalType,
				"percent":       p.Percent,
				"current":       p.CurrentAmount,
				"target":        p.TargetAmount,
			},
		})
		if err != nil {
			failed++
			s.logWarn(ctx, "alert create falló", "goalId", p.Goal.ID.String(), "error", err)
			continue
		}
		if c {
			created++
		}
	}
	return created, failed
}

// genWeeklyReview: domingo, compara spent_at de esta semana vs anterior.
func (s *Service) genWeeklyReview(ctx context.Context, householdID uuid.UUID, at time.Time) (bool, error) {
	thisStart, thisEnd := weekRange(at)
	prevEnd := thisStart.AddDate(0, 0, -1)
	prevStart := prevEnd.AddDate(0, 0, -6)

	thisTotal, err := s.repo.SumSpentAt(ctx, householdID, thisStart, thisEnd)
	if err != nil {
		return false, err
	}
	prevTotal, err := s.repo.SumSpentAt(ctx, householdID, prevStart, prevEnd)
	if err != nil {
		return false, err
	}

	title := fmt.Sprintf("Semana cerrada: %s", formatMoney(thisTotal))
	body := weeklyBody(thisTotal, prevTotal)
	severity := domain.InsightSeverityInfo
	if prevTotal > 0 && thisTotal > prevTotal*1.2 {
		severity = domain.InsightSeverityWarning
	}

	_, created, err := s.repo.Create(ctx, CreateParams{
		HouseholdID: householdID,
		InsightDate: dayStart(at),
		InsightType: domain.InsightTypeWeeklyReview,
		Title:       title,
		Body:        body,
		Severity:    severity,
		Metadata: map[string]any{
			"this_week_total": thisTotal,
			"prev_week_total": prevTotal,
		},
	})
	return created, err
}

// ===================== helpers =====================

func alertText(p domain.BudgetGoalProgress, at time.Time) (string, string, string, bool) {
	switch p.Goal.GoalType {
	case domain.GoalTypeCategoryLimit, domain.GoalTypeTotalLimit:
		if p.TargetAmount <= 0 {
			return "", "", "", false
		}
		ratio := p.CurrentAmount / p.TargetAmount
		if ratio < 0.8 {
			return "", "", "", false
		}
		sev := domain.InsightSeverityWarning
		if ratio >= 1 {
			sev = domain.InsightSeverityCritical
		}
		title := fmt.Sprintf("Objetivo al %.0f%%", ratio*100)
		body := fmt.Sprintf("Llevás %s gastados de %s (%s).",
			formatMoney(p.CurrentAmount), formatMoney(p.TargetAmount), p.Goal.GoalType)
		return title, body, sev, true

	case domain.GoalTypeSavings:
		// Solo alertamos savings después del día 20 si vamos <50%.
		if at.Day() < 20 || p.TargetAmount <= 0 {
			return "", "", "", false
		}
		ratio := p.CurrentAmount / p.TargetAmount
		if ratio >= 0.5 {
			return "", "", "", false
		}
		title := "Vas atrasado con tu ahorro"
		body := fmt.Sprintf("Llevás %s ahorrados de %s objetivo este mes.",
			formatMoney(p.CurrentAmount), formatMoney(p.TargetAmount))
		return title, body, domain.InsightSeverityWarning, true
	}
	return "", "", "", false
}

func weeklyBody(thisWeek, prev float64) string {
	if prev == 0 {
		return fmt.Sprintf("Gasto total de la semana: %s. No hay semana previa comparable.", formatMoney(thisWeek))
	}
	delta := thisWeek - prev
	pct := (delta / prev) * 100
	dir := "más"
	if delta < 0 {
		dir = "menos"
		pct = -pct
	}
	return fmt.Sprintf("Gastaste %s esta semana. %.0f%% %s que la anterior (%s).",
		formatMoney(thisWeek), pct, dir, formatMoney(prev))
}

func formatMoney(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}

func dayStart(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func monthRange(at time.Time) (time.Time, time.Time) {
	y, m, _ := at.Date()
	start := time.Date(y, m, 1, 0, 0, 0, 0, at.Location())
	end := start.AddDate(0, 1, -1)
	return start, end
}

func weekRange(at time.Time) (time.Time, time.Time) {
	at = dayStart(at)
	wd := int(at.Weekday())
	if wd == 0 {
		wd = 7
	}
	start := at.AddDate(0, 0, -(wd - 1))
	end := start.AddDate(0, 0, 6)
	return start, end
}

func (s *Service) logWarn(ctx context.Context, msg string, kv ...any) {
	if s.logger == nil {
		return
	}
	s.logger.WarnContext(ctx, msg, kv...)
}
