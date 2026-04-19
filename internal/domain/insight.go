package domain

import (
	"time"

	"github.com/google/uuid"
)

const (
	InsightTypeDailySummary  = "daily_summary"
	InsightTypeAlert         = "alert"
	InsightTypeTip           = "tip"
	InsightTypeWeeklyReview  = "weekly_review"

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
}
