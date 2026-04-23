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
	InsightTypeSharedExpense = "shared_expense"
	InsightTypeInvite        = "invite"
	InsightTypeSettlement    = "settlement"

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
