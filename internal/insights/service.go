package insights

import (
	"context"
	"fmt"
	"github.com/shopspring/decimal"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/dates"
	"github.com/LucianoR23/valuo_api/internal/domain"
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

// CardReminder: a fixed card reminder of the day for the owner of a
// personal space (plan 4.3). app.go builds them from statements, only for
// personal spaces, so each one reaches its owner once even with a hogar.
type CardReminder struct {
	OwnerUserID uuid.UUID
	domain.CardReminder
}

// CardRemindersAdapter: the reminders of a household on a day (empty for a
// hogar). A function so insights does not import statements.
type CardRemindersAdapter func(ctx context.Context, householdID uuid.UUID, today time.Time) ([]CardReminder, error)

// InstallmentDueForReminder: shape mínimo que precisa el generador de
// avisos de cuota próxima a vencer.
type InstallmentDueForReminder struct {
	ExpenseID         uuid.UUID
	CreatedBy         uuid.UUID
	Description       string
	InstallmentNumber int
	TotalInstallments int
	AmountBase        float64
	BaseCurrency      string
	DueDate           time.Time
}

type installmentsDueLister interface {
	ListInstallmentsDueOn(ctx context.Context, householdID uuid.UUID, dueDate time.Time) ([]InstallmentDueForReminder, error)
}

// InstallmentsDueAdapter: adapter funcional. app.go envuelve una query
// SQL directa contra expense_installments.
type InstallmentsDueAdapter func(ctx context.Context, householdID uuid.UUID, dueDate time.Time) ([]InstallmentDueForReminder, error)

func (a InstallmentsDueAdapter) ListInstallmentsDueOn(ctx context.Context, householdID uuid.UUID, dueDate time.Time) ([]InstallmentDueForReminder, error) {
	return a(ctx, householdID, dueDate)
}

// pushNotifier: dependencia opcional para fire-and-forget de web push.
// Misma shape que el de expenses/settlements para reusar el adapter de main.
type pushNotifier interface {
	NotifyUsers(ctx context.Context, userIDs []uuid.UUID, title, body, url, tag string)
	// NotifyUsersWithAmounts: body never shows an amount (lock screen, plan
	// 1.9); bodyWithAmounts is shown only on devices that opted in.
	NotifyUsersWithAmounts(ctx context.Context, userIDs []uuid.UUID, title, body, bodyWithAmounts, url, tag string)
}

type Service struct {
	repo            *Repository
	households      householdsLister
	goals           goalsProgress
	logger          *slog.Logger
	notify          notifier
	cardReminders   CardRemindersAdapter  // opcional; cableado via SetCardRemindersDep
	push            pushNotifier          // opcional; cableado via SetPushNotifier
	installmentsDue installmentsDueLister // opcional; cableado via SetInstallmentsDueDep
}

func NewService(repo *Repository, households householdsLister, goals goalsProgress, logger *slog.Logger) *Service {
	return &Service{repo: repo, households: households, goals: goals, logger: logger}
}

// SetNotifier cablea el publish a Postgres LISTEN/NOTIFY. Llamar después de
// construir el hub+listener en main. Si nunca se llama, los insights se
// crean igual pero no se propagan en tiempo real (cae al polling).
func (s *Service) SetNotifier(n notifier) { s.notify = n }

// SetCardRemindersDep wires the card closing and due reminders (plan 4.3).
// Without it the generator is a no-op.
func (s *Service) SetCardRemindersDep(d CardRemindersAdapter) { s.cardReminders = d }

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
		InsightDate: dates.Today(),
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

func (s *Service) List(ctx context.Context, householdID uuid.UUID, f ListFilters) ([]domain.DailyInsight, int64, error) {
	return s.repo.ListByHousehold(ctx, householdID, f)
}

func (s *Service) CountUnread(ctx context.Context, householdID, viewer uuid.UUID, userID *uuid.UUID) (int64, error) {
	return s.repo.CountUnread(ctx, householdID, viewer, userID)
}

// visible: the insight is in the household and is either household-level or
// the viewer's own (plan 0.6). Otherwise it does not exist for the viewer.
func (s *Service) visible(ctx context.Context, householdID, viewer, id uuid.UUID) (domain.DailyInsight, error) {
	ins, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return domain.DailyInsight{}, err
	}
	if ins.HouseholdID != householdID || (ins.UserID != nil && *ins.UserID != viewer) {
		return domain.DailyInsight{}, domain.ErrNotFound
	}
	return ins, nil
}

func (s *Service) Get(ctx context.Context, householdID, viewer, id uuid.UUID) (domain.DailyInsight, error) {
	return s.visible(ctx, householdID, viewer, id)
}

func (s *Service) MarkRead(ctx context.Context, householdID, viewer, id uuid.UUID) error {
	if _, err := s.visible(ctx, householdID, viewer, id); err != nil {
		return err
	}
	return s.repo.MarkRead(ctx, id)
}

func (s *Service) MarkAllRead(ctx context.Context, householdID, viewer uuid.UUID, userID *uuid.UUID) error {
	return s.repo.MarkAllRead(ctx, householdID, viewer, userID)
}

func (s *Service) Delete(ctx context.Context, householdID, viewer, id uuid.UUID) error {
	if _, err := s.visible(ctx, householdID, viewer, id); err != nil {
		return err
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

	// 3. card_closing_soon / card_due_soon — a card closes tomorrow or its
	// statement is due in 3 days (plan 4.3). Personal spaces only.
	cn, cf := s.genCardReminders(ctx, householdID, at)
	created += cn
	failed += cf

	// 4. installment_due_soon — cuotas que vencen en 3 días (not credit
	// cards: their statement reminder covers them).
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
		// ref = the goal: one alert per goal and day (migration 000023), so
		// two goals over their limits the same day both get one.
		goalID := p.Goal.ID
		ins, c, err := s.repo.Create(ctx, CreateParams{
			RefID:       &goalID,
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
				"target":    p.TargetAmountBase,
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
	if prevTotal.Sign() > 0 && thisTotal.GreaterThan(prevTotal.Mul(decimal.New(12, -1))) {
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

// genCardReminders: the card reminders of the day (plan 4.3), one insight
// and one push each, personal to the card owner. RefID = payment method,
// plus the date in the unique index (000036): once per card and day.
func (s *Service) genCardReminders(ctx context.Context, householdID uuid.UUID, at time.Time) (int, int) {
	if s.cardReminders == nil {
		return 0, 0
	}
	today := dayStart(at.UTC())
	reminders, err := s.cardReminders(ctx, householdID, today)
	if err != nil {
		s.logWarn(ctx, "card reminders lookup falló", "householdId", householdID.String(), "error", err)
		return 0, 1
	}
	created, failed := 0, 0
	for _, r := range reminders {
		msg := cardReminderMessage(r.CardReminder)
		ownerID, refID := r.OwnerUserID, r.PaymentMethodID
		ins, c, err := s.repo.Create(ctx, CreateParams{
			HouseholdID: householdID,
			UserID:      &ownerID,
			InsightDate: today,
			InsightType: msg.insightType,
			Title:       msg.title,
			Body:        msg.full,
			Severity:    msg.severity,
			Metadata: map[string]any{
				"payment_method_id": r.PaymentMethodID.String(),
				"period_ym":         r.PeriodYM,
				"date":              r.Date.Format(time.DateOnly),
			},
			RefID: &refID,
		})
		if err != nil {
			failed++
			s.logWarn(ctx, "card reminder create falló", "paymentMethodId", r.PaymentMethodID.String(), "error", err)
			continue
		}
		if c {
			created++
			s.publish(ctx, ins)
			if s.push != nil {
				s.push.NotifyUsersWithAmounts(ctx, []uuid.UUID{ownerID}, msg.title, msg.lockScreen, msg.full, msg.url,
					msg.insightType+":"+r.PaymentMethodID.String())
			}
		}
	}
	return created, failed
}

type cardMessage struct {
	insightType, title, lockScreen, full, url string
	severity                                  string
}

var weekdays = [...]string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// cardReminderMessage: the texts of a card reminder. The push body
// (lockScreen) never has an amount (plan 1.9); full is the in-app text.
func cardReminderMessage(r domain.CardReminder) cardMessage {
	day := fmt.Sprintf("%s %d/%d", weekdays[r.Date.Weekday()], r.Date.Day(), int(r.Date.Month()))
	if r.Kind == domain.CardReminderClosing {
		body := fmt.Sprintf("Lo que compres con tu %s desde pasado mañana entra en el resumen siguiente. Si este mes cierra otro día, cargalo en Tarjetas.", r.CardName)
		return cardMessage{
			insightType: domain.InsightTypeCardClosingSoon, title: "Mañana cierra tu " + r.CardName,
			lockScreen: body, full: body, url: "/tarjetas", severity: domain.InsightSeverityInfo,
		}
	}
	head := fmt.Sprintf("El resumen de tu %s vence el %s", r.CardName, day)
	amounts := make([]string, len(r.Totals))
	for i, t := range r.Totals {
		amounts[i] = fmt.Sprintf("%s %s", t.Amount.StringFixed(2), t.Currency)
	}
	return cardMessage{
		insightType: domain.InsightTypeCardDueSoon, title: fmt.Sprintf("En %d días vence tu %s", domain.CardReminderDueDays, r.CardName),
		lockScreen: head + ".", full: head + ": " + strings.Join(amounts, " + ") + ".",
		url: "/tarjetas/" + r.PaymentMethodID.String() + "/resumen/" + r.PeriodYM, severity: domain.InsightSeverityWarning,
	}
}

// genInstallmentsDueSoon: avisa al creador del gasto cuando una cuota
// suya vence dentro de 3 días. Útil para tarjetas de crédito (cuotas
// futuras del resumen) — para gastos en efectivo/débito las cuotas ya
// están marcadas paid en su creación, así que el filtro is_paid=false
// del query las excluye naturalmente.
//
// Dedupe: one alert per expense and day (ref = expense, plus the date in the
// unique index since migration 000023). Each installment is due on a
// different date, so each one gets its alert; several installments of the
// same expense due the same day share one.
//
// No-op si la dep no está cableada.
func (s *Service) genInstallmentsDueSoon(ctx context.Context, householdID uuid.UUID, at time.Time) (int, int) {
	if s.installmentsDue == nil {
		return 0, 0
	}
	targetDue := dayStart(at.UTC()).AddDate(0, 0, 3)
	// Only this household's rows (plan 0.14: this used to read every
	// household's installments once per household).
	rows, err := s.installmentsDue.ListInstallmentsDueOn(ctx, householdID, targetDue)
	if err != nil {
		s.logWarn(ctx, "installments due lookup falló", "householdId", householdID.String(), "error", err)
		return 0, 1
	}
	created, failed := 0, 0
	for _, row := range rows {
		owner := row.CreatedBy
		refID := row.ExpenseID
		title := "Cuota próxima a vencer"
		lockScreen, body := installmentDueMessage(row)
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
				s.push.NotifyUsersWithAmounts(
					ctx,
					[]uuid.UUID{owner},
					title, lockScreen, body,
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
		if p.TargetAmountBase.Sign() <= 0 {
			return "", "", "", false
		}
		ratio := p.CurrentAmount.Div(p.TargetAmountBase).InexactFloat64()
		if ratio < 0.8 {
			return "", "", "", false
		}
		sev := domain.InsightSeverityWarning
		if ratio >= 1 {
			sev = domain.InsightSeverityCritical
		}
		title := fmt.Sprintf("Objetivo al %.0f%%", ratio*100)
		body := fmt.Sprintf("Llevás %s gastados de %s (%s).",
			formatMoney(p.CurrentAmount), formatMoney(p.TargetAmountBase), p.Goal.GoalType)
		return title, body, sev, true

	case domain.GoalTypeSavings:
		// Solo alertamos savings después del día 20 si vamos <50%.
		if at.Day() < 20 || p.TargetAmountBase.Sign() <= 0 {
			return "", "", "", false
		}
		ratio := p.CurrentAmount.Div(p.TargetAmountBase).InexactFloat64()
		if ratio >= 0.5 {
			return "", "", "", false
		}
		title := "Vas atrasado con tu ahorro"
		body := fmt.Sprintf("Llevás %s ahorrados de %s objetivo este mes.",
			formatMoney(p.CurrentAmount), formatMoney(p.TargetAmountBase))
		return title, body, domain.InsightSeverityWarning, true
	}
	return "", "", "", false
}

func weeklyBody(thisWeek, prev decimal.Decimal, counts SpentCounts, monthDue decimal.Decimal) string {
	var summary string
	if counts.Total == 0 {
		summary = "Esta semana no registraste gastos."
	} else {
		summary = fmt.Sprintf("Gastaste %s en %d transaccion(es) de %d categoría(s).",
			formatMoney(thisWeek), counts.Total, counts.DistinctCategories)
	}

	var compare string
	switch {
	case prev.IsZero() && counts.Total > 0:
		compare = "No hay semana previa comparable."
	case prev.Sign() > 0:
		delta := thisWeek.Sub(prev)
		pct := delta.Div(prev).Mul(decimal.NewFromInt(100)).InexactFloat64()
		dir := "más"
		if delta.Sign() < 0 {
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

func formatMoney(v decimal.Decimal) string {
	return "$" + v.StringFixed(2)
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

// installmentDueMessage returns the push text without the amount (lock
// screen, plan 1.9) and the full text the in-app notification keeps.
func installmentDueMessage(row InstallmentDueForReminder) (lockScreen, full string) {
	head := fmt.Sprintf("En 3 días vence la cuota %d/%d de \"%s\"", row.InstallmentNumber, row.TotalInstallments, row.Description)
	return head + ".", fmt.Sprintf("%s (%.2f %s).", head, row.AmountBase, row.BaseCurrency)
}
