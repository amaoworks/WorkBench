package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-co-op/gocron/v2"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

type startProbeEngine struct {
	gocron.Scheduler
	probe func()
}

func (e *startProbeEngine) Start() {
	e.probe()
	e.Scheduler.Start()
}

func newMisfireScheduler(t *testing.T, database *workbenchdb.Database, definitions []contracts.JobDefinition) (*Scheduler, time.Time) {
	t.Helper()
	first, err := New(context.Background(), database.SQL(), definitions, func(contracts.ModuleID) bool { return true }, schedulerTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	if _, err := database.SQL().Exec("UPDATE scheduled_jobs SET next_run_at = ? WHERE id = ?", past.UnixMilli(), definitions[0].ID); err != nil {
		t.Fatal(err)
	}
	s, err := New(context.Background(), database.SQL(), definitions, func(contracts.ModuleID) bool { return true }, schedulerTestLogger())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return s, past
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for lifecycle barrier")
		var zero T
		return zero
	}
}

func TestMisfireReservesJobAndSkipsOverlappingRuns(t *testing.T) {
	database := openSchedulerDatabase(t)
	entered := make(chan contracts.JobRun, 1)
	var calls, otherCalls atomic.Int32
	definition := validJob(func(ctx context.Context, run contracts.JobRun) error {
		calls.Add(1)
		entered <- run
		<-ctx.Done()
		return ctx.Err()
	})
	definition.Schedule.Interval = time.Millisecond
	definition.Timeout = time.Minute
	other := validJob(func(context.Context, contracts.JobRun) error {
		otherCalls.Add(1)
		return nil
	})
	other.ID = "todo.other"
	s, past := newMisfireScheduler(t, database, []contracts.JobDefinition{definition, other})
	var reserved bool
	s.engine = &startProbeEngine{Scheduler: s.engine, probe: func() {
		reserved = s.pending[definition.ID].Equal(past)
	}}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reserved {
		t.Fatal("misfire was not reserved before starting the periodic engine")
	}
	if run := receive(t, entered); !run.ScheduledAt.Equal(past) {
		t.Fatalf("first run = %v, want overdue run %v", run.ScheduledAt, past)
	}
	if err := s.execute(context.Background(), definition, time.Now()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("overlapping handler calls = %d, want 1", calls.Load())
	}
	if err := s.execute(context.Background(), other, time.Now()); err != nil {
		t.Fatal(err)
	}
	if otherCalls.Load() != 1 {
		t.Fatal("a different JobID was blocked")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	var runs int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM scheduled_job_runs WHERE job_id = ?", definition.ID).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("misfire history count = %d, error = %v; want 1", runs, err)
	}
}

func TestShutdownCancelsAndWaitsForRuns(t *testing.T) {
	for _, start := range []bool{false, true} {
		name := "without_start"
		if start {
			name = "misfire"
		}
		t.Run(name, func(t *testing.T) {
			database := openSchedulerDatabase(t)
			entered := make(chan struct{})
			canceled := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			var calls atomic.Int32
			definition := validJob(func(ctx context.Context, _ contracts.JobRun) error {
				calls.Add(1)
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
				return ctx.Err()
			})
			definition.Timeout = time.Minute
			s, _ := newMisfireScheduler(t, database, []contracts.JobDefinition{definition})
			executed := make(chan error, 1)
			if start {
				if err := s.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				go func() { executed <- s.execute(context.Background(), definition, time.Now()) }()
			}
			receive(t, entered)
			waitCtx, cancelWait := context.WithCancel(context.Background())
			defer cancelWait()
			firstShutdown := make(chan error, 1)
			go func() { firstShutdown <- s.Shutdown(waitCtx) }()
			receive(t, canceled)
			select {
			case err := <-firstShutdown:
				t.Fatalf("Shutdown returned before handler cleanup: %v", err)
			default:
			}
			cancelWait()
			if err := receive(t, firstShutdown); !errors.Is(err, context.Canceled) {
				t.Fatalf("Shutdown error = %v, want canceled wait", err)
			}
			if err := s.execute(context.Background(), definition, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := s.Start(context.Background()); err == nil {
				t.Fatal("Start succeeded after shutdown began")
			}
			if calls.Load() != 1 {
				t.Fatalf("handler calls after shutdown = %d, want 1", calls.Load())
			}
			finished := make(chan error, 1)
			go func() { finished <- s.Shutdown(context.Background()) }()
			// Send a release token; the deferred close also unblocks cleanup after failures.
			release <- struct{}{}
			if err := receive(t, finished); err != nil {
				t.Fatal(err)
			}
			if !start {
				if err := receive(t, executed); !errors.Is(err, context.Canceled) {
					t.Fatalf("execute error = %v, want context canceled", err)
				}
			}
			var unfinished int
			if err := database.SQL().QueryRow("SELECT COUNT(*) FROM scheduled_job_runs WHERE finished_at IS NULL").Scan(&unfinished); err != nil || unfinished != 0 {
				t.Fatalf("unfinished runs = %d, error = %v", unfinished, err)
			}
		})
	}
}

func TestStartFailureDoesNotLaunchJobs(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "persistence_failure"
		if canceled {
			name = "canceled_context"
		}
		t.Run(name, func(t *testing.T) {
			database := openSchedulerDatabase(t)
			var calls atomic.Int32
			definition := validJob(func(context.Context, contracts.JobRun) error { calls.Add(1); return nil })
			definition.Schedule.Interval = time.Millisecond
			s, _ := newMisfireScheduler(t, database, []contracts.JobDefinition{definition})
			var engineStarted bool
			s.engine = &startProbeEngine{Scheduler: s.engine, probe: func() { engineStarted = true }}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
			} else if _, err := database.SQL().Exec(`CREATE TRIGGER reject_next_run BEFORE UPDATE OF next_run_at ON scheduled_jobs BEGIN SELECT RAISE(ABORT, 'reject next run'); END`); err != nil {
				t.Fatal(err)
			}
			if err := s.Start(ctx); err == nil {
				t.Fatal("Start succeeded despite initialization failure")
			}
			if engineStarted || calls.Load() != 0 {
				t.Fatalf("failed Start launched work: engine=%v calls=%d", engineStarted, calls.Load())
			}
			if s.runCtx.Err() == nil {
				t.Fatal("failed Start left its lifecycle active")
			}
			if err := s.Start(context.Background()); err == nil {
				t.Fatal("failed scheduler was restarted")
			}
			if err := s.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
