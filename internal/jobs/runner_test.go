package jobs

import (
	"context"
	"log/slog"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/LucianoR23/valuo_api/internal/testutil"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

// fakeUnits records the dates each unit ran for.
type fakeUnits struct {
	mu   sync.Mutex
	runs map[string][]string
}

func (f *fakeUnits) record(kind string, t time.Time, layout string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs[kind] = append(f.runs[kind], t.Format(layout))
}

func (f *fakeUnits) units() Units {
	f.runs = map[string][]string{}
	return Units{
		FXRefresh: func(context.Context) error { f.record("fx", time.Time{}, ""); return nil },
		RecurringIncomes: func(_ context.Context, d time.Time) (int, error) {
			f.record("incomes", d, time.RFC3339)
			return 0, nil
		},
		RecurringExpenses: func(_ context.Context, d time.Time) (int, int, error) {
			f.record("expenses", d, time.RFC3339)
			return 0, 0, nil
		},
		Insights: func(_ context.Context, d time.Time) (int, int, error) {
			f.record("insights", d, time.RFC3339)
			return 0, 0, nil
		},
		MonthlyReports: func(_ context.Context, at time.Time) error {
			f.record("reports", at, time.RFC3339)
			return nil
		},
		AuditRetention:  func(context.Context) error { f.record("audit", time.Time{}, ""); return nil },
		WaitlistCleanup: func(context.Context) (int64, error) { f.record("waitlist", time.Time{}, ""); return 0, nil },
	}
}

func (f *fakeUnits) get(kind string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.runs[kind]...)
}

func newTestRunner(t *testing.T, pool *pgxpool.Pool, f *fakeUnits, now time.Time) *Runner {
	t.Helper()
	r, err := newRunner(pool, f.units(), slog.New(slog.DiscardHandler), true)
	if err != nil {
		t.Fatal(err)
	}
	r.now = testutil.FixedClock(now)
	return r
}

// The unit gets the job's day as midnight UTC, as the old loops passed it.
func TestRunnerWorksJobs(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	f := &fakeUnits{}
	r := newTestRunner(t, pool, f, time.Date(2026, 3, 10, 12, 0, 0, 0, testutil.Location(t)))

	events, cancel := r.client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer cancel()
	stop := r.Start(ctx)
	defer stop()

	res, err := r.client.Insert(ctx, RecurringIncomesArgs{Date: "2026-02-03"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, res.Job.ID)

	got := f.get("incomes")
	found := false
	for _, d := range got {
		found = found || d == "2026-02-03T00:00:00Z"
	}
	if !found {
		t.Fatalf("incomes runs = %v, want one for 2026-02-03T00:00:00Z", got)
	}
}

func TestMonthlyReportGetsTheFollowingMonth(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	f := &fakeUnits{}
	r := newTestRunner(t, pool, f, time.Date(2026, 3, 10, 12, 0, 0, 0, testutil.Location(t)))

	events, cancel := r.client.Subscribe(river.EventKindJobCompleted, river.EventKindJobFailed)
	defer cancel()
	stop := r.Start(ctx)
	defer stop()

	res, err := r.client.Insert(ctx, MonthlyReportArgs{Month: "2026-12"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, res.Job.ID)
	// The unit reports the month before "at": December needs 1 January.
	if got := f.get("reports"); len(got) != 1 || got[0] != "2027-01-01T00:00:00Z" {
		t.Fatalf("reports runs = %v, want [2027-01-01T00:00:00Z]", got)
	}
}

func waitFor(t *testing.T, events <-chan *river.Event, id int64) {
	t.Helper()
	timeout := time.After(15 * time.Second)
	for {
		select {
		case ev := <-events:
			if ev.Job.ID != id {
				continue
			}
			if ev.Kind != river.EventKindJobCompleted {
				t.Fatalf("job %d: %s (errors %v)", id, ev.Kind, ev.Job.Errors)
			}
			return
		case <-timeout:
			t.Fatalf("job %d did not finish", id)
		}
	}
}

// Two replicas (or a backfill and the periodic job) inserting the same day
// produce one job.
func TestOneJobPerDay(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	r := newTestRunner(t, pool, &fakeUnits{}, time.Now())

	first, err := r.client.Insert(ctx, InsightsArgs{Date: "2026-03-10"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.client.Insert(ctx, InsightsArgs{Date: "2026-03-10"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.UniqueSkippedAsDuplicate || !second.UniqueSkippedAsDuplicate || first.Job.ID != second.Job.ID {
		t.Fatalf("first skipped=%v, second skipped=%v, ids %d/%d: want one job", first.UniqueSkippedAsDuplicate, second.UniqueSkippedAsDuplicate, first.Job.ID, second.Job.ID)
	}
	other, err := r.client.Insert(ctx, InsightsArgs{Date: "2026-03-11"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if other.UniqueSkippedAsDuplicate {
		t.Fatal("another day must be a new job")
	}
}

// completed records a finished job of kind for the backfill to find.
func completed(t *testing.T, pool *pgxpool.Pool, kind, args string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		INSERT INTO river_job (kind, args, state, attempt, max_attempts, attempted_at, finalized_at, queue)
		VALUES ($1, $2, 'completed', 1, 5, now(), now(), $3)`, kind, args, queueGeneration)
	if err != nil {
		t.Fatal(err)
	}
}

func pending(t *testing.T, pool *pgxpool.Pool, kind, field string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT args->>$2 FROM river_job WHERE kind = $1 AND state = 'available' ORDER BY id`, kind, field)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func TestBackfill(t *testing.T) {
	loc := testutil.Location(t)

	t.Run("no history enqueues nothing", func(t *testing.T) {
		pool := testdb.New(t)
		r := newTestRunner(t, pool, &fakeUnits{}, time.Date(2026, 3, 10, 12, 0, 0, 0, loc))
		if err := r.Backfill(context.Background()); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%d jobs enqueued, want 0", n)
		}
	})

	t.Run("missed days, in order, up to the latest due", func(t *testing.T) {
		pool := testdb.New(t)
		completed(t, pool, "recurring_incomes", `{"date":"2026-03-06"}`)
		completed(t, pool, "recurring_incomes", `{"date":"2026-03-07"}`)
		completed(t, pool, "insights_daily", `{"date":"2026-03-08"}`)
		// 00:45: recurring (00:30) is due today, insights (01:00) not yet.
		r := newTestRunner(t, pool, &fakeUnits{}, time.Date(2026, 3, 10, 0, 45, 0, 0, loc))
		if err := r.Backfill(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got, want := pending(t, pool, "recurring_incomes", "date"), []string{"2026-03-08", "2026-03-09", "2026-03-10"}; !slices.Equal(got, want) {
			t.Errorf("recurring_incomes = %v, want %v", got, want)
		}
		if got, want := pending(t, pool, "insights_daily", "date"), []string{"2026-03-09"}; !slices.Equal(got, want) {
			t.Errorf("insights_daily = %v, want %v", got, want)
		}
		if got := pending(t, pool, "recurring_expenses", "date"); len(got) != 0 {
			t.Errorf("recurring_expenses has no history, got %v", got)
		}

		// A second replica starting at the same time adds nothing.
		if err := r.Backfill(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := pending(t, pool, "recurring_incomes", "date"); len(got) != 3 {
			t.Errorf("second backfill duplicated jobs: %v", got)
		}
	})

	t.Run("limited to the last days", func(t *testing.T) {
		pool := testdb.New(t)
		completed(t, pool, "insights_daily", `{"date":"2026-01-01"}`)
		r := newTestRunner(t, pool, &fakeUnits{}, time.Date(2026, 3, 10, 12, 0, 0, 0, loc))
		if err := r.Backfill(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := pending(t, pool, "insights_daily", "date")
		if len(got) != insightsBackfillDays || got[0] != "2026-03-04" || got[len(got)-1] != "2026-03-10" {
			t.Fatalf("insights_daily = %v, want the 7 days 2026-03-04..2026-03-10", got)
		}
	})

	t.Run("monthly report: only the latest month", func(t *testing.T) {
		pool := testdb.New(t)
		completed(t, pool, "monthly_report", `{"month":"2025-12"}`)
		r := newTestRunner(t, pool, &fakeUnits{}, time.Date(2026, 3, 10, 12, 0, 0, 0, loc))
		if err := r.Backfill(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got, want := pending(t, pool, "monthly_report", "month"), []string{"2026-02"}; !slices.Equal(got, want) {
			t.Fatalf("monthly_report = %v, want %v", got, want)
		}
	})

	t.Run("monthly report already sent", func(t *testing.T) {
		pool := testdb.New(t)
		completed(t, pool, "monthly_report", `{"month":"2026-02"}`)
		r := newTestRunner(t, pool, &fakeUnits{}, time.Date(2026, 3, 10, 12, 0, 0, 0, loc))
		if err := r.Backfill(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := pending(t, pool, "monthly_report", "month"); len(got) != 0 {
			t.Fatalf("monthly_report = %v, want none", got)
		}
	})
}
