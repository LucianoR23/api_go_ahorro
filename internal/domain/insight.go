package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	InsightTypeDailySummary = "daily_summary"
	InsightTypeAlert        = "alert"
	InsightTypeTip          = "tip"
	InsightTypeWeeklyReview = "weekly_review"
	// Event-driven insights: viven al lado de los agregados diarios pero usan
	// ref_id para de-duplicar contra la entidad origen.
	InsightTypeSharedExpense        = "shared_expense"
	InsightTypeInvite               = "invite"
	InsightTypeSettlement           = "settlement"
	InsightTypeCreditPeriodReminder = "credit_period_reminder"
	// RecurringSpike: el usuario confirmó un gasto recurrente de monto
	// variable con un importe que supera el threshold configurado en la
	// serie (ej: luz subió +29% vs el mes anterior con threshold=20%).
	// ref_id apunta al expense confirmado, no a la recurring_expense, así
	// el frontend puede deep-link al detalle del gasto.
	InsightTypeRecurringSpike = "recurring_spike"

	InsightSeverityInfo     = "info"
	InsightSeverityWarning  = "warning"
	InsightSeverityCritical = "critical"
)

type DailyInsight struct {
	ID          uuid.UUID
	HouseholdID uuid.UUID
	UserID      *uuid.UUID
	InsightDate time.Time
	InsightType string
	Title       string
	Body        string
	Severity    string
	IsRead      bool
	Metadata    []byte
	CreatedAt   time.Time
	RefID       *uuid.UUID
}
