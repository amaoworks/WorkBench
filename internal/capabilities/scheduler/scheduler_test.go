package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

func openSchedulerDatabase(t *testing.T) *workbenchdb.Database {
	t.Helper()
	ctx := context.Background()
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = database.SQL().ExecContext(ctx, `
		INSERT INTO modules(id, name, version, contract_version, enabled, installed_at, updated_at)
		VALUES ('todo', 'Todo', '0.1.0', 1, 1, 1, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func validJob(handler func(context.Context, contracts.JobRun) error) contracts.JobDefinition {
	return contracts.JobDefinition{
		ID: "todo.scan_due", Module: "todo",
		Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: time.Hour},
		TimeZone: "UTC", Timeout: time.Second,
		OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
		Retry:   contracts.RetryPolicy{MaxAttempts: 2, InitialWait: time.Millisecond, MaxWait: time.Millisecond},
		Handler: handler,
	}
}

func TestNewPersistsDefinition(t *testing.T) {
	database := openSchedulerDatabase(t)
	scheduler, err := New(context.Background(), database.SQL(), []contracts.JobDefinition{
		validJob(func(context.Context, contracts.JobRun) error { return nil }),
	}, func(contracts.ModuleID) bool { return true }, schedulerTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })

	var kind, expression, hash string
	if err := database.SQL().QueryRow(`
		SELECT schedule_kind, schedule_expr, definition_hash
		FROM scheduled_jobs WHERE id = 'todo.scan_due'`).Scan(&kind, &expression, &hash); err != nil {
		t.Fatal(err)
	}
	if kind != "interval" || expression != "1h0m0s" || hash == "" {
		t.Fatalf("persisted definition = %q, %q, %q", kind, expression, hash)
	}
}

func TestExecuteRetriesAndRecordsHistory(t *testing.T) {
	database := openSchedulerDatabase(t)
	var calls atomic.Int32
	definition := validJob(func(context.Context, contracts.JobRun) error {
		if calls.Add(1) == 1 {
			return errors.New("transient")
		}
		return nil
	})
	scheduler, err := New(context.Background(), database.SQL(), []contracts.JobDefinition{definition}, func(contracts.ModuleID) bool { return true }, schedulerTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })
	if err := scheduler.execute(context.Background(), definition, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("handler calls = %d, want 2", calls.Load())
	}
	rows, err := database.SQL().Query("SELECT status FROM scheduled_job_runs ORDER BY attempt")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var statuses []string
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, status)
	}
	if len(statuses) != 2 || statuses[0] != "failed" || statuses[1] != "succeeded" {
		t.Fatalf("run statuses = %v", statuses)
	}
}

func TestExecuteEnforcesTimeout(t *testing.T) {
	database := openSchedulerDatabase(t)
	definition := validJob(func(ctx context.Context, _ contracts.JobRun) error {
		<-ctx.Done()
		return ctx.Err()
	})
	definition.Timeout = 10 * time.Millisecond
	definition.Retry.MaxAttempts = 1
	scheduler, err := New(context.Background(), database.SQL(), []contracts.JobDefinition{definition}, func(contracts.ModuleID) bool { return true }, schedulerTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })
	if err := scheduler.execute(context.Background(), definition, time.Unix(200, 0)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("execute error = %v, want deadline exceeded", err)
	}
	var status string
	if err := database.SQL().QueryRow("SELECT status FROM scheduled_job_runs").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "timed_out" {
		t.Fatalf("run status = %q, want timed_out", status)
	}
}

func TestExecuteSkipsDisabledModule(t *testing.T) {
	database := openSchedulerDatabase(t)
	var calls atomic.Int32
	definition := validJob(func(context.Context, contracts.JobRun) error {
		calls.Add(1)
		return nil
	})
	scheduler, err := New(context.Background(), database.SQL(), []contracts.JobDefinition{definition}, func(contracts.ModuleID) bool { return false }, schedulerTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scheduler.Shutdown(context.Background()) })
	if err := scheduler.execute(context.Background(), definition, time.Unix(300, 0)); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("disabled job handler calls = %d, want 0", calls.Load())
	}
	var runs int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM scheduled_job_runs").Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("disabled job persisted %d runs, want 0", runs)
	}
}

func TestStartRunsPastDueJobOnce(t *testing.T) {
	database := openSchedulerDatabase(t)
	definition := validJob(func(context.Context, contracts.JobRun) error { return nil })
	first, err := New(context.Background(), database.SQL(), []contracts.JobDefinition{definition}, func(contracts.ModuleID) bool { return true }, schedulerTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour)
	if _, err := database.SQL().Exec("UPDATE scheduled_jobs SET next_run_at = ? WHERE id = ?", past.UnixMilli(), definition.ID); err != nil {
		t.Fatal(err)
	}
	runs := make(chan contracts.JobRun, 2)
	definition.Handler = func(_ context.Context, run contracts.JobRun) error {
		runs <- run
		return nil
	}
	second, err := New(context.Background(), database.SQL(), []contracts.JobDefinition{definition}, func(contracts.ModuleID) bool { return true }, schedulerTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Shutdown(context.Background()) })
	if err := second.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case run := <-runs:
		if run.ScheduledAt.UnixMilli() != past.UnixMilli() {
			t.Fatalf("misfire scheduledAt = %v, want %v", run.ScheduledAt, past)
		}
	case <-time.After(time.Second):
		t.Fatal("past-due run_once job did not run")
	}
	select {
	case run := <-runs:
		t.Fatalf("past-due job ran more than once: %+v", run)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEnginePreventsOverlappingRuns(t *testing.T) {
	database := openSchedulerDatabase(t)
	var current, maximum, calls atomic.Int32
	started := make(chan struct{}, 4)
	definition := validJob(func(ctx context.Context, _ contracts.JobRun) error {
		calls.Add(1)
		started <- struct{}{}
		active := current.Add(1)
		defer current.Add(-1)
		for {
			seen := maximum.Load()
			if active <= seen || maximum.CompareAndSwap(seen, active) {
				break
			}
		}
		select {
		case <-time.After(25 * time.Millisecond):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	definition.Schedule.Interval = 5 * time.Millisecond
	definition.Timeout = time.Second
	scheduler, err := New(context.Background(), database.SQL(), []contracts.JobDefinition{definition}, func(contracts.ModuleID) bool { return true }, schedulerTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := scheduler.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for observed := 0; observed < 2; observed++ {
		select {
		case <-started:
		case <-deadline.C:
			cancel()
			t.Fatalf("job calls = %d, want at least 2", calls.Load())
		}
	}
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	if err := scheduler.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent runs = %d, want 1", maximum.Load())
	}
}

func TestNextRunCalculatesCronAndCollapsesMissedIntervals(t *testing.T) {
	now := time.Date(2026, time.September, 4, 8, 30, 0, 0, time.UTC)
	cronDefinition := validJob(func(context.Context, contracts.JobRun) error { return nil })
	cronDefinition.Schedule = contracts.ScheduleSpec{Kind: contracts.ScheduleCron, Expression: "0 9 * * *"}
	next, exists, err := nextRun(cronDefinition, now, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.September, 4, 9, 0, 0, 0, time.UTC)
	if !exists || !next.Equal(want) {
		t.Fatalf("cron next run = %v, %v; want %v, true", next, exists, want)
	}

	intervalDefinition := validJob(func(context.Context, contracts.JobRun) error { return nil })
	intervalDefinition.Schedule.Interval = time.Hour
	next, exists, err = nextRun(intervalDefinition, now.Add(-3*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if !exists || !next.Equal(now.Add(time.Hour)) {
		t.Fatalf("collapsed interval next run = %v, %v; want %v, true", next, exists, now.Add(time.Hour))
	}
}

func schedulerTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
