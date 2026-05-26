package insights

import (
	"context"
	"errors"
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

// notifier: dispara un pg_notify por cada insight nuevo. Inyectado para
// que el service no dependa del pool directamente (testeable + opcional).
type notifier func(ctx context.Context, ev Event) error

// CreditCardForReminder: vista mínima que necesita el generador de
// recordatorios. La provee el adapter de main.go iterando miembros del
// hogar + sus tarjetas activas.
type CreditCardForReminder struct {
	PaymentMethodID uuid.UUID
	OwnerUserID     uuid.UUID
	CreditCardID    uuid.UUID
	Alias           string
}

// creditCardsByHouseholdLister: lista las tarjetas de crédito activas de
// todos los miembros del hogar. Optional (puede ser nil si no se cablea,
// en cuyo caso el generador de recordatorios es no-op).
type creditCardsByHouseholdLister interface {
	ListActiveCreditCardsByHousehold(ctx context.Context, householdID uuid.UUID) ([]CreditCardForReminder, error)
}

// periodLatestReader: último período cargado de una tarjeta. Devuelve
// ErrNotFound si no hay períodos.
type periodLatestReader interface {
	GetLatest(ctx context.Context, creditCardID uuid.UUID) (domain.CreditCardPeriod, error)
}

// CreditCardsAdapter: adapter funcional para no acoplar insights →
// paymethods/households en el import graph. main.go arma el closure que
// compone households.ListMembers + paymethods.ListCreditCards.
type CreditCardsAdapter func(ctx context.Context, householdID uuid.UUID) ([]CreditCardForReminder, error)

func (a CreditCardsAdapter) ListActiveCreditCardsByHousehold(ctx context.Context, householdID uuid.UUID) ([]CreditCardForReminder, error) {
	return a(ctx, householdID)
}

// PeriodLatestAdapter: adapter funcional para no acoplar insights →
// creditperiods. main.go envuelve creditperiods.Repository.GetLatest.
type PeriodLatestAdapter func(ctx context.Context, creditCardID uuid.UUID) (domain.CreditCardPeriod, error)

func (a PeriodLatestAdapter) GetLatest(ctx context.Context, creditCardID uuid.UUID) (domain.CreditCardPeriod, error) {
	return a(ctx, creditCardID)
}

// InstallmentDueForReminder: shape mínimo que precisa el generador de
// avisos de cuota próxima a vencer.
type InstallmentDueForReminder struct {
	ExpenseID         uuid.UUID
	HouseholdID       uuid.UUID
	CreatedBy         uuid.UUID
	Description       string
	InstallmentNumber int
	TotalInstallments int
	AmountBase        float64
	BaseCurrency      string
	DueDate           time.Time
}

type installmentsDueLister interface {
	ListInstallmentsDueOn(ctx context.Context, dueDate time.Time) ([]InstallmentDueForReminder, error)
}

// InstallmentsDueAdapter: adapter funcional. main.go envuelve una query
// SQL directa contra expense_installments.
type InstallmentsDueAdapter func(ctx context.Context, dueDate time.Time) ([]InstallmentDueForReminder, error)

func (a InstallmentsDueAdapter) ListInstallmentsDueOn(ctx context.Context, dueDate time.Time) ([]InstallmentDueForReminder, error) {
	return a(ctx, dueDate)
}

// pushNotifier: dependencia opcional para fire-and-forget de web push.
// Misma shape que el de expenses/settlements para reusar el adapter de main.
type pushNotifier interface {
	NotifyUsers(ctx context.Context, userIDs []uuid.UUID, title, body, url, tag string)
}

type Service struct {
	repo        *Repository
	households  householdsLister
	goals       goalsProgress
	logger      *slog.Logger
	notify      notifier
	creditCards     creditCardsByHouseholdLister // opcional; cableado via SetCreditPeriodDeps
	periods         periodLatestReader           // opcional; cableado via SetCreditPeriodDeps
	push            pushNotifier                 // opcional; cableado via SetPushNotifier
	installmentsDue installmentsDueLister        // opcional; cableado via SetInstallmentsDueDep
}

func NewService(repo *Repository, households householdsLister, goals goalsProgress, logger *slog.Logger) *Service {
	return &Service{repo: repo, households: households, goals: goals, logger: logger}
}

// SetNotifier cablea el publish a Postgres LISTEN/NOTIFY. Llamar después de
// construir el hub+listener en main. Si nunca se llama, los insights se
// crean igual pero no se propagan en tiempo real (cae al polling).
func (s *Service) SetNotifier(n notifier) { s.notify = n }

// SetCreditPeriodDeps cablea las dependencias necesarias para el generador
// de recordatorios de período de tarjeta. Si no se llama, el generador es
// no-op (los demás insights se generan igual).
func (s *Service) SetCreditPeriodDeps(cc creditCardsByHouseholdLister, p periodLatestReader) {
	s.creditCards = cc
	s.periods = p
}

// SetPushNotifier cablea web push para insights generados por el worker.
// Opcional: si no se cablea, los insights se crean igual (SSE + /notifs).
func (s *Service) SetPushNotifier(p pushNotifier) { s.push = p }

// SetInstallmentsDueDep cablea la query de cuotas próximas a vencer. Si
// no se cablea, el generador de aviso de vencimiento es no-op.
func (s *Service) SetInstallmentsDueDep(d installmentsDueLister) { s.installmentsDue = d }

// CreateEvent: API pública para que otros servicios (expenses, invites,
// settlements) creen insights "por evento" linkeados a una entidad origen
// vía refID. De-dup automático por (hh, user, type, ref_id). Si created=true,
// emite el NOTIFY para fan-out en tiempo real.
type EventInput struct {
	HouseholdID uuid.UUID
	UserID      *uuid.UUID
	InsightType string
	Title       string
	Body        string
	Severity    string
	RefID       uuid.UUID
	Metadata    map[string]any
}

func (s *Service) CreateEvent(ctx context.Context, in EventInput) (domain.DailyInsight, bool, error) {
	ref := in.RefID
	ins, created, err := s.repo.Create(ctx, CreateParams{
		HouseholdID: in.HouseholdID,
		UserID:      in.UserID,
		InsightDate: dayStart(time.Now()),
		InsightType: in.InsightType,
		Title:       in.Title,
		Body:        in.Body,
		Severity:    in.Severity,
		Metadata:    in.Metadata,
		RefID:       &ref,
	})
	if err != nil {
		return ins, false, err
	}
	if created {
		s.publish(ctx, ins)
	}
	return ins, created, nil
}

// publish: best-effort, never blocks the caller. Si falla, el insight queda
// igual en DB y la UI lo verá en el próximo polling.
func (s *Service) publish(ctx context.Context, ins domain.DailyInsight) {
	if s.notify == nil {
		return
	}
	if err := s.notify(ctx, Event{
		InsightID:   ins.ID,
		HouseholdID: ins.HouseholdID,
		UserID:      ins.UserID,
		InsightType: ins.InsightType,
	}); err != nil && s.logger != nil {
		s.logger.WarnContext(ctx, "insights: pg_notify falló",
			"insightId", ins.ID.String(), "error", err)
	}
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

	// 1. alerts — goals activos >=80%.
	n, nf := s.genGoalAlerts(ctx, householdID, at)
	created += n
	failed += nf

	// 2. weekly_review — solo domingos. Reemplaza al viejo daily_summary
	// que era demasiado ruidoso para gastos esporádicos.
	if at.Weekday() == time.Sunday {
		if c, err := s.genWeeklyReview(ctx, householdID, at); err != nil {
			failed++
			s.logWarn(ctx, "weekly_review falló", "householdId", householdID.String(), "error", err)
		} else if c {
			created++
		}
	}

	// 3. credit_period_reminder — un insight por tarjeta cuando hoy es el día
	// de cierre del último período cargado (señal: hay que cargar el siguiente).
	cn, cf := s.genCreditPeriodReminders(ctx, householdID, at)
	created += cn
	failed += cf

	// 4. installment_due_soon — cuotas que vencen en 3 días.
	in, ifail := s.genInstallmentsDueSoon(ctx, householdID, at)
	created += in
	failed += ifail

	return created, failed
}

// ---------- generators ----------

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
		ins, c, err := s.repo.Create(ctx, CreateParams{
			HouseholdID: householdID,
			UserID:      userID,
			InsightDate: dayStart(at),
			InsightType: domain.InsightTypeAlert,
			Title:       title,
			Body:        body,
			Severity:    severity,
			Metadata: map[string]any{
				"goal_id":   p.Goal.ID.String(),
				"goal_type": p.Goal.GoalType,
				"percent":   p.Percent,
				"current":   p.CurrentAmount,
				"target":    p.TargetAmount,
			},
		})
		if err != nil {
			failed++
			s.logWarn(ctx, "alert create falló", "goalId", p.Goal.ID.String(), "error", err)
			continue
		}
		if c {
			created++
			s.publish(ctx, ins)
		}
	}
	return created, failed
}

// genWeeklyReview: domingo, compara spent_at de esta semana vs anterior y
// agrega un resumen con cantidad de movimientos + total que falta pagar
// este mes (lo que antes vivía en el daily_summary, ahora consolidado acá).
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
	counts, err := s.repo.CountSpentAt(ctx, householdID, thisStart, thisEnd)
	if err != nil {
		return false, err
	}
	monthStart, monthEnd := monthRange(at)
	monthDue, err := s.repo.SumDue(ctx, householdID, monthStart, monthEnd)
	if err != nil {
		return false, err
	}

	title := fmt.Sprintf("Semana cerrada: %s", formatMoney(thisTotal))
	body := weeklyBody(thisTotal, prevTotal, counts, monthDue)
	severity := domain.InsightSeverityInfo
	if prevTotal > 0 && thisTotal > prevTotal*1.2 {
		severity = domain.InsightSeverityWarning
	}

	ins, created, err := s.repo.Create(ctx, CreateParams{
		HouseholdID: householdID,
		InsightDate: dayStart(at),
		InsightType: domain.InsightTypeWeeklyReview,
		Title:       title,
		Body:        body,
		Severity:    severity,
		Metadata: map[string]any{
			"this_week_total":      thisTotal,
			"prev_week_total":      prevTotal,
			"this_week_count":      counts.Total,
			"this_week_categories": counts.DistinctCategories,
			"month_due":            monthDue,
		},
	})
	if created {
		s.publish(ctx, ins)
	}
	return created, err
}

// genCreditPeriodReminders: para cada tarjeta de crédito activa de algún
// miembro del hogar, dispara un insight el día que cierra el último período
// cargado. Señal explícita al user: "ya cerró este ciclo, cargá el próximo
// para que los gastos siguientes se asignen al período correcto".
//
// Reglas:
//   - Si la tarjeta no tiene ningún período cargado → skip (lo cubre el
//     onboarding del frontend al crear la tarjeta — current_period es
//     obligatorio en la API).
//   - Si today != latest.ClosingDate (truncado a día UTC) → skip.
//   - El insight queda linkeado al user dueño de la tarjeta vía UserID,
//     porque las tarjetas son personales (no del hogar). RefID =
//     paymentMethodID dedupea si el worker corre dos veces el mismo día.
//
// No-op si las deps no están cableadas (SetCreditPeriodDeps no llamado).
func (s *Service) genCreditPeriodReminders(ctx context.Context, householdID uuid.UUID, at time.Time) (int, int) {
	if s.creditCards == nil || s.periods == nil {
		return 0, 0
	}
	cards, err := s.creditCards.ListActiveCreditCardsByHousehold(ctx, householdID)
	if err != nil {
		s.logWarn(ctx, "credit cards lookup falló", "householdId", householdID.String(), "error", err)
		return 0, 1
	}
	today := dayStart(at.UTC())
	created, failed := 0, 0
	for _, card := range cards {
		latest, err := s.periods.GetLatest(ctx, card.CreditCardID)
		if err != nil {
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			failed++
			s.logWarn(ctx, "latest period falló",
				"creditCardId", card.CreditCardID.String(), "error", err)
			continue
		}
		closing := dayStart(latest.ClosingDate.UTC())
		if !closing.Equal(today) {
			continue
		}
		ownerID := card.OwnerUserID
		refID := card.PaymentMethodID
		title := fmt.Sprintf("Cargá el próximo período de %s", card.Alias)
		body := fmt.Sprintf(
			"Hoy cerró el período %s. Cargá el siguiente para que los gastos se asignen al ciclo correcto.",
			latest.PeriodYM,
		)
		ins, c, err := s.repo.Create(ctx, CreateParams{
			HouseholdID: householdID,
			UserID:      &ownerID,
			InsightDate: today,
			InsightType: domain.InsightTypeCreditPeriodReminder,
			Title:       title,
			Body:        body,
			Severity:    domain.InsightSeverityWarning,
			Metadata: map[string]any{
				"payment_method_id": card.PaymentMethodID.String(),
				"credit_card_id":    card.CreditCardID.String(),
				"latest_period_ym":  latest.PeriodYM,
				"alias":             card.Alias,
			},
			RefID: &refID,
		})
		if err != nil {
			failed++
			s.logWarn(ctx, "credit_period_reminder create falló",
				"paymentMethodId", card.PaymentMethodID.String(), "error", err)
			continue
		}
		if c {
			created++
			s.publish(ctx, ins)
			if s.push != nil {
				s.push.NotifyUsers(
					ctx,
					[]uuid.UUID{ownerID},
					title,
					body,
					"/ajustes",
					"credit-period-reminder:"+card.PaymentMethodID.String(),
				)
			}
		}
	}
	return created, failed
}

// genInstallmentsDueSoon: avisa al creador del gasto cuando una cuota
// suya vence dentro de 3 días. Útil para tarjetas de crédito (cuotas
// futuras del resumen) — para gastos en efectivo/débito las cuotas ya
// están marcadas paid en su creación, así que el filtro is_paid=false
// del query las excluye naturalmente.
//
// Dedupe: refID = expenseID (mismo gasto cuotas distintas comparten
// expenseID; si el user tiene varias cuotas venciendo el mismo día se
// genera solo una entrada por gasto, que es lo deseable UX-wise).
//
// No-op si la dep no está cableada.
func (s *Service) genInstallmentsDueSoon(ctx context.Context, householdID uuid.UUID, at time.Time) (int, int) {
	if s.installmentsDue == nil {
		return 0, 0
	}
	targetDue := dayStart(at.UTC()).AddDate(0, 0, 3)
	rows, err := s.installmentsDue.ListInstallmentsDueOn(ctx, targetDue)
	if err != nil {
		s.logWarn(ctx, "installments due lookup falló", "householdId", householdID.String(), "error", err)
		return 0, 1
	}
	created, failed := 0, 0
	for _, row := range rows {
		if row.HouseholdID != householdID {
			continue
		}
		owner := row.CreatedBy
		refID := row.ExpenseID
		title := "Cuota próxima a vencer"
		body := fmt.Sprintf(
			"En 3 días vence la cuota %d/%d de \"%s\" (%.2f %s).",
			row.InstallmentNumber, row.TotalInstallments, row.Description,
			row.AmountBase, row.BaseCurrency,
		)
		ins, c, err := s.repo.Create(ctx, CreateParams{
			HouseholdID: householdID,
			UserID:      &owner,
			InsightDate: dayStart(at),
			InsightType: domain.InsightTypeAlert,
			Title:       title,
			Body:        body,
			Severity:    domain.InsightSeverityWarning,
			Metadata: map[string]any{
				"kind":               "installment_due_soon",
				"expense_id":         row.ExpenseID.String(),
				"installment_number": row.InstallmentNumber,
				"total_installments": row.TotalInstallments,
				"amount_base":        row.AmountBase,
				"base_currency":      row.BaseCurrency,
				"due_date":           row.DueDate.Format("2006-01-02"),
			},
			RefID: &refID,
		})
		if err != nil {
			failed++
			s.logWarn(ctx, "installment_due_soon create falló",
				"expenseId", row.ExpenseID.String(), "error", err)
			continue
		}
		if c {
			created++
			s.publish(ctx, ins)
			if s.push != nil {
				s.push.NotifyUsers(
					ctx,
					[]uuid.UUID{owner},
					title, body,
					"/movimientos/"+row.ExpenseID.String(),
					"installment-due-soon:"+row.ExpenseID.String(),
				)
			}
		}
	}
	return created, failed
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

func weeklyBody(thisWeek, prev float64, counts SpentCounts, monthDue float64) string {
	var summary string
	if counts.Total == 0 {
		summary = "Esta semana no registraste gastos."
	} else {
		summary = fmt.Sprintf("Gastaste %s en %d transaccion(es) de %d categoría(s).",
			formatMoney(thisWeek), counts.Total, counts.DistinctCategories)
	}

	var compare string
	switch {
	case prev == 0 && counts.Total > 0:
		compare = "No hay semana previa comparable."
	case prev > 0:
		delta := thisWeek - prev
		pct := (delta / prev) * 100
		dir := "más"
		if delta < 0 {
			dir = "menos"
			pct = -pct
		}
		compare = fmt.Sprintf("%.0f%% %s que la anterior (%s).", pct, dir, formatMoney(prev))
	}

	tail := fmt.Sprintf("Este mes te vienen a cobrar %s.", formatMoney(monthDue))

	parts := summary
	if compare != "" {
		parts += " " + compare
	}
	return parts + " " + tail
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
