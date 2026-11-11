// Package jobs runs the background work on River (plan 0.8), a job queue
// kept in Postgres. Every replica runs a River client; periodic jobs are
// enqueued only by the elected leader and each job is worked once, so N API
// instances never double-run a day. Failed jobs retry with backoff, and days
// missed while the API was down are backfilled when it starts.
//
// The units of work live in their own packages (incomes, insights, ...); this
// package only schedules them.
package jobs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// Units are the units of work behind the jobs. All are required.
type Units struct {
	FXRefresh         func(ctx context.Context) error
	CPIRefresh        func(ctx context.Context) error
	RecurringIncomes  func(ctx context.Context, date time.Time) (created int, err error)
	RecurringExpenses func(ctx context.Context, date time.Time) (created, failed int, err error)
	Insights          func(ctx context.Context, at time.Time) (generated, failed int, err error)
	// MonthlyReports emails the summary of the month before "at".
	MonthlyReports  func(ctx context.Context, at time.Time) error
	AuditRetention  func(ctx context.Context) error
	WaitlistCleanup func(ctx context.Context) (deleted int64, err error)
	// DeliverPush sends a push that waited for the end of quiet hours.
	DeliverPush func(ctx context.Context, userID uuid.UUID, kind string, payload []byte) error
}

// Queues. Generation jobs (recurring, insights, monthly email) run one at a
// time, in insert order: a backfill of several days must apply day by day,
// as the old daily loop did.
const (
	queueGeneration  = "generation"
	queueMaintenance = river.QueueDefault
)

const dateLayout = "2006-01-02"
const monthLayout = "2006-01"

// generationOpts: one job per day (or month) ever, whichever replica or
// backfill inserts it first. River's default unique states include
// completed, so a finished day is not run again while its row is retained.
func generationOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       queueGeneration,
		MaxAttempts: 5,
		UniqueOpts:  river.UniqueOpts{ByArgs: true},
	}
}

// FXRefreshArgs refreshes the exchange rates (every 15 minutes).
type FXRefreshArgs struct{}

func (FXRefreshArgs) Kind() string { return "fx_refresh" }
func (FXRefreshArgs) InsertOpts() river.InsertOpts {
	// The next run is 15 minutes away; a long retry chain only piles up.
	return river.InsertOpts{Queue: queueMaintenance, MaxAttempts: 3}
}

// CPIRefreshArgs downloads INDEC's price index (daily, plan 5.4). Most
// days nothing is new; INDEC publishes a month around the 12th of the next.
type CPIRefreshArgs struct{}

func (CPIRefreshArgs) Kind() string { return "cpi_refresh" }
func (CPIRefreshArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queueMaintenance, MaxAttempts: 5}
}

// RecurringIncomesArgs generates the recurring incomes due on Date.
type RecurringIncomesArgs struct {
	Date string `json:"date"`
}

func (RecurringIncomesArgs) Kind() string                 { return "recurring_incomes" }
func (RecurringIncomesArgs) InsertOpts() river.InsertOpts { return generationOpts() }

// RecurringExpensesArgs generates the recurring expenses (or drafts) due on Date.
type RecurringExpensesArgs struct {
	Date string `json:"date"`
}

func (RecurringExpensesArgs) Kind() string                 { return "recurring_expenses" }
func (RecurringExpensesArgs) InsertOpts() river.InsertOpts { return generationOpts() }

// InsightsArgs generates the daily insights of every household for Date.
type InsightsArgs struct {
	Date string `json:"date"`
}

func (InsightsArgs) Kind() string                 { return "insights_daily" }
func (InsightsArgs) InsertOpts() river.InsertOpts { return generationOpts() }

// MonthlyReportArgs emails the summary of Month (YYYY-MM) to every household.
type MonthlyReportArgs struct {
	Month string `json:"month"`
}

func (MonthlyReportArgs) Kind() string { return "monthly_report" }
func (MonthlyReportArgs) InsertOpts() river.InsertOpts {
	o := generationOpts()
	// A retry after a partial send would mail the households that already
	// got it; the unit logs per-household failures and only errors when it
	// could not start (listing households).
	o.MaxAttempts = 3
	return o
}

// AuditRetentionArgs applies the audit log retention (daily).
type AuditRetentionArgs struct{}

func (AuditRetentionArgs) Kind() string { return "audit_retention" }
func (AuditRetentionArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queueMaintenance, MaxAttempts: 5}
}

// WaitlistCleanupArgs deletes stale unconfirmed waitlist sign-ups (daily).
type WaitlistCleanupArgs struct{}

func (WaitlistCleanupArgs) Kind() string { return "waitlist_cleanup" }
func (WaitlistCleanupArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queueMaintenance, MaxAttempts: 5}
}

// PushDeliveryArgs is a push held by the person's quiet hours (plan 10.1),
// scheduled for the end of the window.
type PushDeliveryArgs struct {
	UserID     uuid.UUID       `json:"user_id"`
	NoticeKind string          `json:"kind"`
	Payload    json.RawMessage `json:"payload"`
}

func (PushDeliveryArgs) Kind() string { return "push_delivery" }
func (PushDeliveryArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queueMaintenance, MaxAttempts: 3}
}
