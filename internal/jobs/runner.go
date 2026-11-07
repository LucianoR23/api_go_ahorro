package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/LucianoR23/valuo_api/internal/dates"
)

// Schedules, in the app timezone (the same times the old in-process loops
// used).
var (
	recurringSchedule = daily{0, 30}
	insightsSchedule  = daily{1, 0}
	auditSchedule     = daily{3, 30}
	waitlistSchedule  = daily{4, 0}
	reportsSchedule   = monthly{8, 0}
	fxInterval        = 15 * time.Minute
)

// Backfill limits: how far back a restart recovers missed periods. Older
// days are skipped (logged). Insights for a day long gone are noise, and a
// monthly summary is only worth sending for the latest month.
const (
	recurringBackfillDays = 31
	insightsBackfillDays  = 7

	// retention keeps completed jobs, which are the backfill's record of
	// what already ran. It must outlast the monthly gap.
	retention = 62 * 24 * time.Hour

	stopTimeout = 30 * time.Second
)

// Runner owns the River client.
type Runner struct {
	client *river.Client[pgx.Tx]
	pool   *pgxpool.Pool
	logger *slog.Logger

	// now is the clock seam (plan 0.11).
	now func() time.Time
}

// New builds the River client with every worker and periodic job. It does
// not touch the database; Start does.
func New(pool *pgxpool.Pool, u Units, logger *slog.Logger) (*Runner, error) {
	return newRunner(pool, u, logger, false)
}

func newRunner(pool *pgxpool.Pool, u Units, logger *slog.Logger, testOnly bool) (*Runner, error) {
	r := &Runner{pool: pool, logger: logger, now: dates.Now}

	workers := river.NewWorkers()
	addWorker(workers, 30*time.Second, func(ctx context.Context, _ FXRefreshArgs) error {
		if err := u.FXRefresh(ctx); err != nil {
			return err
		}
		logger.InfoContext(ctx, "fx refresh: ok")
		return nil
	})
	addWorker(workers, 2*time.Minute, func(ctx context.Context, a RecurringIncomesArgs) error {
		date, err := time.Parse(dateLayout, a.Date)
		if err != nil {
			return river.JobCancel(err)
		}
		created, err := u.RecurringIncomes(ctx, date)
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "recurring incomes: ok", "date", a.Date, "created", created)
		return nil
	})
	addWorker(workers, 5*time.Minute, func(ctx context.Context, a RecurringExpensesArgs) error {
		date, err := time.Parse(dateLayout, a.Date)
		if err != nil {
			return river.JobCancel(err)
		}
		created, failed, err := u.RecurringExpenses(ctx, date)
		if err != nil {
			return err
		}
		level := slog.LevelInfo
		if failed > 0 {
			level = slog.LevelWarn
		}
		logger.Log(ctx, level, "recurring expenses: done", "date", a.Date, "created", created, "failed", failed)
		return nil
	})
	addWorker(workers, 10*time.Minute, func(ctx context.Context, a InsightsArgs) error {
		date, err := time.Parse(dateLayout, a.Date)
		if err != nil {
			return river.JobCancel(err)
		}
		generated, failed, err := u.Insights(ctx, date)
		if err != nil {
			return err
		}
		level := slog.LevelInfo
		if failed > 0 {
			level = slog.LevelWarn
		}
		logger.Log(ctx, level, "insights: done", "date", a.Date, "generated", generated, "failed", failed)
		return nil
	})
	addWorker(workers, 10*time.Minute, func(ctx context.Context, a MonthlyReportArgs) error {
		month, err := time.Parse(monthLayout, a.Month)
		if err != nil {
			return river.JobCancel(err)
		}
		// The unit reports the month before "at".
		return u.MonthlyReports(ctx, month.AddDate(0, 1, 0))
	})
	addWorker(workers, 2*time.Minute, func(ctx context.Context, _ AuditRetentionArgs) error {
		return u.AuditRetention(ctx)
	})
	addWorker(workers, time.Minute, func(ctx context.Context, _ WaitlistCleanupArgs) error {
		n, err := u.WaitlistCleanup(ctx)
		if err != nil {
			return err
		}
		logger.InfoContext(ctx, "waitlist cleanup: ok", "deleted", n)
		return nil
	})

	periodic := []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(fxInterval),
			func() (river.JobArgs, *river.InsertOpts) { return FXRefreshArgs{}, nil },
			&river.PeriodicJobOpts{ID: "fx_refresh", RunOnStart: true}),
		// Daily generation: the constructor names the latest day due, so a
		// RunOnStart before 00:30 names yesterday (already done: a unique
		// duplicate) and the first start ever still covers today, as the old
		// loops' catch-up run did.
		river.NewPeriodicJob(recurringSchedule,
			func() (river.JobArgs, *river.InsertOpts) {
				return RecurringIncomesArgs{Date: recurringSchedule.lastDay(r.now()).Format(dateLayout)}, nil
			},
			&river.PeriodicJobOpts{ID: "recurring_incomes", RunOnStart: true}),
		river.NewPeriodicJob(recurringSchedule,
			func() (river.JobArgs, *river.InsertOpts) {
				return RecurringExpensesArgs{Date: recurringSchedule.lastDay(r.now()).Format(dateLayout)}, nil
			},
			&river.PeriodicJobOpts{ID: "recurring_expenses", RunOnStart: true}),
		river.NewPeriodicJob(insightsSchedule,
			func() (river.JobArgs, *river.InsertOpts) {
				return InsightsArgs{Date: insightsSchedule.lastDay(r.now()).Format(dateLayout)}, nil
			},
			&river.PeriodicJobOpts{ID: "insights_daily", RunOnStart: true}),
		// No RunOnStart: the old loop never sent on start, and a first
		// deploy mid-month must not mail last month's summary. A missed
		// day 1 is recovered by the backfill, which needs a previous send.
		river.NewPeriodicJob(reportsSchedule,
			func() (river.JobArgs, *river.InsertOpts) {
				return MonthlyReportArgs{Month: reportsSchedule.lastReported(r.now()).Format(monthLayout)}, nil
			},
			&river.PeriodicJobOpts{ID: "monthly_report"}),
		river.NewPeriodicJob(auditSchedule,
			func() (river.JobArgs, *river.InsertOpts) { return AuditRetentionArgs{}, nil },
			&river.PeriodicJobOpts{ID: "audit_retention", RunOnStart: true}),
		river.NewPeriodicJob(waitlistSchedule,
			func() (river.JobArgs, *river.InsertOpts) { return WaitlistCleanupArgs{}, nil },
			&river.PeriodicJobOpts{ID: "waitlist_cleanup", RunOnStart: true}),
	}

	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			queueGeneration:  {MaxWorkers: 1},
			queueMaintenance: {MaxWorkers: 2},
		},
		Workers:                     workers,
		PeriodicJobs:                periodic,
		CompletedJobRetentionPeriod: retention,
		Logger:                      logger,
		TestOnly:                    testOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("jobs: river client: %w", err)
	}
	r.client = client
	return r, nil
}

// Start backfills the periods missed while the API was down, then starts
// the River client. The returned function stops it, letting running jobs
// finish for up to 30 seconds before cancelling them.
func (r *Runner) Start(ctx context.Context) func() {
	if err := r.Backfill(ctx); err != nil {
		// Not fatal: the periodic jobs still run; the next start retries.
		r.logger.ErrorContext(ctx, "jobs: backfill failed", "error", err)
	}
	if err := r.client.Start(ctx); err != nil {
		r.logger.ErrorContext(ctx, "jobs: river start failed", "error", err)
		return func() {}
	}
	return func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if err := r.client.Stop(stopCtx); err != nil {
			r.logger.Warn("jobs: graceful stop timed out, cancelling running jobs", "error", err)
			hardCtx, hardCancel := context.WithTimeout(context.Background(), stopTimeout)
			defer hardCancel()
			_ = r.client.StopAndCancel(hardCtx)
		}
	}
}

// Backfill enqueues every period from the one after the last completed run
// up to the latest one due, within each job's limit. With no completed run
// on record (first start, or a gap longer than the retention) it enqueues
// nothing: the periodic jobs take over from now. Safe on every replica: the
// inserts are unique.
func (r *Runner) Backfill(ctx context.Context) error {
	now := r.now()
	var params []river.InsertManyParams

	days := []struct {
		kind  string
		sched daily
		limit int
		args  func(day string) river.JobArgs
	}{
		{RecurringIncomesArgs{}.Kind(), recurringSchedule, recurringBackfillDays, func(d string) river.JobArgs { return RecurringIncomesArgs{Date: d} }},
		{RecurringExpensesArgs{}.Kind(), recurringSchedule, recurringBackfillDays, func(d string) river.JobArgs { return RecurringExpensesArgs{Date: d} }},
		{InsightsArgs{}.Kind(), insightsSchedule, insightsBackfillDays, func(d string) river.JobArgs { return InsightsArgs{Date: d} }},
	}
	for _, j := range days {
		last, ok, err := r.lastCompleted(ctx, j.kind, "date", dateLayout)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		due := j.sched.lastDay(now)
		from := last.AddDate(0, 0, 1)
		if floor := due.AddDate(0, 0, -(j.limit - 1)); from.Before(floor) {
			r.logger.WarnContext(ctx, "jobs: backfill limit reached, older days skipped",
				"kind", j.kind, "skipped_from", from.Format(dateLayout), "skipped_to", floor.AddDate(0, 0, -1).Format(dateLayout))
			from = floor
		}
		for d := from; !d.After(due); d = d.AddDate(0, 0, 1) {
			params = append(params, river.InsertManyParams{Args: j.args(d.Format(dateLayout))})
		}
	}

	if last, ok, err := r.lastCompleted(ctx, MonthlyReportArgs{}.Kind(), "month", monthLayout); err != nil {
		return err
	} else if ok {
		// Only the latest month: an older summary is stale.
		if due := reportsSchedule.lastReported(now); due.After(last) {
			params = append(params, river.InsertManyParams{Args: MonthlyReportArgs{Month: due.Format(monthLayout)}})
		}
	}

	if len(params) == 0 {
		return nil
	}
	res, err := r.client.InsertMany(ctx, params)
	if err != nil {
		return fmt.Errorf("jobs: backfill insert: %w", err)
	}
	inserted := 0
	for _, x := range res {
		if !x.UniqueSkippedAsDuplicate {
			inserted++
		}
	}
	r.logger.InfoContext(ctx, "jobs: backfill", "candidates", len(params), "inserted", inserted)
	return nil
}

// lastCompleted is the latest period (args field parsed with layout) among
// the completed jobs of kind. It reads River's job table directly: River's
// list API pages through rows, and this is one aggregate.
func (r *Runner) lastCompleted(ctx context.Context, kind, field, layout string) (time.Time, bool, error) {
	var last *string
	err := r.pool.QueryRow(ctx,
		`SELECT max(args->>$2) FROM river_job WHERE kind = $1 AND state = 'completed'`,
		kind, field).Scan(&last)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("jobs: last completed %s: %w", kind, err)
	}
	if last == nil {
		return time.Time{}, false, nil
	}
	t, err := time.Parse(layout, *last)
	if err != nil {
		return time.Time{}, false, errors.Join(fmt.Errorf("jobs: last completed %s: bad %s %q", kind, field, *last), err)
	}
	return t, true, nil
}

// fnWorker adapts a function to river.Worker with a per-kind timeout.
type fnWorker[T river.JobArgs] struct {
	river.WorkerDefaults[T]
	timeout time.Duration
	run     func(ctx context.Context, args T) error
}

func (w *fnWorker[T]) Work(ctx context.Context, job *river.Job[T]) error {
	return w.run(ctx, job.Args)
}

func (w *fnWorker[T]) Timeout(*river.Job[T]) time.Duration { return w.timeout }

func addWorker[T river.JobArgs](ws *river.Workers, timeout time.Duration, run func(context.Context, T) error) {
	river.AddWorker(ws, &fnWorker[T]{timeout: timeout, run: run})
}
