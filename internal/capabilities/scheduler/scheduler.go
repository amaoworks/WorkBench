package scheduler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/robfig/cron/v3"

	"workbench/internal/contracts"
	"workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/identity"
)

type EnabledFunc func(contracts.ModuleID) bool

type Scheduler struct {
	queries     *dbsqlc.Queries
	engine      gocron.Scheduler
	definitions map[contracts.JobID]contracts.JobDefinition
	jobs        map[contracts.JobID]gocron.Job
	enabled     EnabledFunc
	logger      *slog.Logger
	now         func() time.Time
	mu          sync.Mutex
	started     bool
	misfires    []misfire
}

type misfire struct {
	definition  contracts.JobDefinition
	scheduledAt time.Time
}

func New(ctx context.Context, db *sql.DB, definitions []contracts.JobDefinition, enabled EnabledFunc, logger *slog.Logger) (*Scheduler, error) {
	if db == nil || enabled == nil {
		return nil, errors.New("database and enabled function are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	engine, err := gocron.NewScheduler(gocron.WithLocation(time.UTC))
	if err != nil {
		return nil, fmt.Errorf("create scheduler: %w", err)
	}
	s := &Scheduler{
		queries: dbsqlc.New(db), engine: engine, definitions: make(map[contracts.JobID]contracts.JobDefinition),
		jobs: make(map[contracts.JobID]gocron.Job), enabled: enabled, logger: logger, now: time.Now,
	}
	if err := s.queries.RecoverInterruptedJobRuns(ctx, sql.NullInt64{Int64: time.Now().UTC().UnixMilli(), Valid: true}); err != nil {
		_ = engine.Shutdown()
		return nil, fmt.Errorf("recover interrupted job runs: %w", err)
	}

	seen := make(map[contracts.JobID]struct{}, len(definitions))
	for _, definition := range definitions {
		if err := validateDefinition(definition); err != nil {
			_ = engine.Shutdown()
			return nil, err
		}
		if _, exists := seen[definition.ID]; exists {
			_ = engine.Shutdown()
			return nil, fmt.Errorf("duplicate job id %q", definition.ID)
		}
		seen[definition.ID] = struct{}{}
		s.definitions[definition.ID] = definition
		previousNext, err := s.persistDefinition(ctx, definition)
		if err != nil {
			_ = engine.Shutdown()
			return nil, err
		}
		if previousNext != nil && previousNext.Before(s.now().UTC()) && definition.MisfirePolicy == contracts.MisfireRunOnce {
			s.misfires = append(s.misfires, misfire{definition: definition, scheduledAt: *previousNext})
		}
		if definition.Schedule.Kind == contracts.ScheduleOnce && !definition.Schedule.RunAt.After(s.now().UTC()) {
			continue
		}
		if err := s.register(definition); err != nil {
			_ = engine.Shutdown()
			return nil, err
		}
	}
	return s, nil
}

func validateDefinition(definition contracts.JobDefinition) error {
	if definition.ID == "" || definition.Module == "" || definition.Handler == nil {
		return fmt.Errorf("job %q is incomplete", definition.ID)
	}
	if definition.TimeZone == "" {
		return fmt.Errorf("job %q requires a timezone", definition.ID)
	}
	if _, err := time.LoadLocation(definition.TimeZone); err != nil {
		return fmt.Errorf("job %q timezone: %w", definition.ID, err)
	}
	if definition.Timeout <= 0 {
		return fmt.Errorf("job %q requires a positive timeout", definition.ID)
	}
	if definition.Retry.MaxAttempts <= 0 {
		return fmt.Errorf("job %q requires at least one attempt", definition.ID)
	}
	switch definition.Schedule.Kind {
	case contracts.ScheduleCron:
		if strings.TrimSpace(definition.Schedule.Expression) == "" {
			return fmt.Errorf("job %q has an empty cron expression", definition.ID)
		}
	case contracts.ScheduleInterval:
		if definition.Schedule.Interval <= 0 {
			return fmt.Errorf("job %q has a non-positive interval", definition.ID)
		}
	case contracts.ScheduleOnce:
		if definition.Schedule.RunAt.IsZero() {
			return fmt.Errorf("job %q has an empty run time", definition.ID)
		}
	default:
		return fmt.Errorf("job %q has unsupported schedule kind %q", definition.ID, definition.Schedule.Kind)
	}
	if definition.OverlapPolicy != contracts.OverlapSkip {
		return fmt.Errorf("job %q has unsupported overlap policy %q", definition.ID, definition.OverlapPolicy)
	}
	if definition.MisfirePolicy != contracts.MisfireSkip && definition.MisfirePolicy != contracts.MisfireRunOnce {
		return fmt.Errorf("job %q has unsupported misfire policy %q", definition.ID, definition.MisfirePolicy)
	}
	return nil
}

func (s *Scheduler) persistDefinition(ctx context.Context, definition contracts.JobDefinition) (*time.Time, error) {
	previous, err := s.queries.GetScheduledJobNextRun(ctx, string(definition.ID))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	expression := scheduleExpression(definition.Schedule)
	hash := definitionHash(definition, expression)
	err = s.queries.UpsertScheduledJob(ctx, dbsqlc.UpsertScheduledJobParams{
		ID: string(definition.ID), Module: string(definition.Module), ScheduleKind: string(definition.Schedule.Kind),
		ScheduleExpr: expression, Timezone: definition.TimeZone, TimeoutMs: definition.Timeout.Milliseconds(),
		OverlapPolicy: string(definition.OverlapPolicy), MisfirePolicy: string(definition.MisfirePolicy),
		MaxAttempts: int64(definition.Retry.MaxAttempts), DefinitionHash: hash, UpdatedAt: s.now().UTC().UnixMilli(),
	})
	if err != nil {
		return nil, fmt.Errorf("persist job %q: %w", definition.ID, err)
	}
	if previous.Valid {
		value := time.UnixMilli(previous.Int64).UTC()
		return &value, nil
	}
	return nil, nil
}

func scheduleExpression(schedule contracts.ScheduleSpec) string {
	switch schedule.Kind {
	case contracts.ScheduleInterval:
		return schedule.Interval.String()
	case contracts.ScheduleOnce:
		return schedule.RunAt.UTC().Format(time.RFC3339Nano)
	default:
		return schedule.Expression
	}
}

func definitionHash(definition contracts.JobDefinition, expression string) string {
	value := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d\x00%s\x00%s\x00%d",
		definition.ID, definition.Module, definition.Schedule.Kind, expression,
		definition.Timeout.Milliseconds(), definition.TimeZone, definition.MisfirePolicy,
		definition.Retry.MaxAttempts,
	)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (s *Scheduler) register(definition contracts.JobDefinition) error {
	var schedule gocron.JobDefinition
	switch definition.Schedule.Kind {
	case contracts.ScheduleCron:
		expression := definition.Schedule.Expression
		if !strings.HasPrefix(expression, "TZ=") && !strings.HasPrefix(expression, "CRON_TZ=") {
			expression = "CRON_TZ=" + definition.TimeZone + " " + expression
		}
		schedule = gocron.CronJob(expression, false)
	case contracts.ScheduleInterval:
		schedule = gocron.DurationJob(definition.Schedule.Interval)
	case contracts.ScheduleOnce:
		schedule = gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(definition.Schedule.RunAt))
	}
	identifier := uuid.NewSHA1(uuid.NameSpaceOID, []byte(definition.ID))
	job, err := s.engine.NewJob(
		schedule,
		gocron.NewTask(func(ctx context.Context) error {
			err := s.execute(ctx, definition, s.now().UTC())
			if err != nil {
				s.logger.Error("scheduled job failed", "jobId", definition.ID, "errorType", fmt.Sprintf("%T", err))
			}
			return err
		}),
		gocron.WithIdentifier(identifier),
		gocron.WithName(string(definition.ID)),
		gocron.WithSingletonMode(gocron.LimitModeReschedule),
	)
	if err != nil {
		return fmt.Errorf("register job %q: %w", definition.ID, err)
	}
	s.jobs[definition.ID] = job
	return nil
}

func (s *Scheduler) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("scheduler already started")
	}
	s.started = true
	s.mu.Unlock()
	s.engine.Start()
	if err := s.updateNextRuns(ctx); err != nil {
		return err
	}
	for _, missed := range s.misfires {
		missed := missed
		go func() {
			if !s.enabled(missed.definition.Module) {
				return
			}
			if err := s.execute(ctx, missed.definition, missed.scheduledAt); err != nil {
				s.logger.Error("misfired job failed", "jobId", missed.definition.ID, "errorType", fmt.Sprintf("%T", err))
			}
		}()
	}
	return nil
}

func (s *Scheduler) Shutdown(ctx context.Context) error {
	return s.engine.ShutdownWithContext(ctx)
}

func (s *Scheduler) execute(parent context.Context, definition contracts.JobDefinition, scheduledAt time.Time) error {
	if !s.enabled(definition.Module) {
		return nil
	}
	startAttempt, err := s.nextAttempt(parent, definition.ID, scheduledAt)
	if err != nil {
		return err
	}
	if startAttempt > definition.Retry.MaxAttempts {
		return nil
	}
	var lastErr error
	for attempt := startAttempt; attempt <= definition.Retry.MaxAttempts; attempt++ {
		runID, err := identity.New()
		if err != nil {
			return err
		}
		now := s.now().UTC()
		err = s.queries.InsertScheduledJobRun(parent, dbsqlc.InsertScheduledJobRunParams{
			ID: runID, JobID: string(definition.ID), ScheduledAt: scheduledAt.UTC().UnixMilli(),
			StartedAt: sql.NullInt64{Int64: now.UnixMilli(), Valid: true}, Attempt: int64(attempt),
		})
		if err != nil {
			return fmt.Errorf("start job run %q: %w", definition.ID, err)
		}

		runCtx, cancel := context.WithTimeout(parent, definition.Timeout)
		s.logger.Debug("job attempt started", "jobId", definition.ID, "runId", runID, "attempt", attempt)
		lastErr = definition.Handler(runCtx, contracts.JobRun{
			ID: runID, JobID: definition.ID, ScheduledAt: scheduledAt.UTC(), Attempt: attempt,
		})
		deadlineExceeded := errors.Is(runCtx.Err(), context.DeadlineExceeded)
		cancel()
		status := "succeeded"
		errorMessage := ""
		if lastErr != nil || deadlineExceeded {
			status = "failed"
			if deadlineExceeded {
				status = "timed_out"
				lastErr = context.DeadlineExceeded
			}
			errorMessage = truncate(lastErr)
		}
		finished := s.now().UTC()
		if err := s.queries.FinishScheduledJobRun(context.WithoutCancel(parent), dbsqlc.FinishScheduledJobRunParams{
			Status: status, FinishedAt: sql.NullInt64{Int64: finished.UnixMilli(), Valid: true},
			Error: errorMessage, ID: runID,
		}); err != nil {
			return fmt.Errorf("finish job run %q: %w", definition.ID, err)
		}
		if lastErr == nil {
			s.logger.Debug("job completed", "jobId", definition.ID, "runId", runID, "attempt", attempt, "durationMs", finished.Sub(now).Milliseconds())
			_ = s.queries.MarkScheduledJobLastRun(context.WithoutCancel(parent), dbsqlc.MarkScheduledJobLastRunParams{
				LastRunAt: sql.NullInt64{Int64: finished.UnixMilli(), Valid: true}, UpdatedAt: finished.UnixMilli(), ID: string(definition.ID),
			})
			s.updateNextRun(context.WithoutCancel(parent), definition.ID, scheduledAt)
			return nil
		}
		if attempt < definition.Retry.MaxAttempts {
			wait := retryWait(definition.Retry, attempt)
			// Handler errors can include upstream response bodies or credentials.
			s.logger.Warn("job attempt failed; retry scheduled", "jobId", definition.ID, "runId", runID, "attempt", attempt, "status", status, "retryInMs", wait.Milliseconds(), "errorType", fmt.Sprintf("%T", lastErr))
			timer := time.NewTimer(wait)
			select {
			case <-parent.Done():
				timer.Stop()
				return parent.Err()
			case <-timer.C:
			}
		}
	}
	s.updateNextRun(context.WithoutCancel(parent), definition.ID, scheduledAt)
	return lastErr
}

func (s *Scheduler) nextAttempt(ctx context.Context, id contracts.JobID, scheduledAt time.Time) (int, error) {
	attempt, err := s.queries.GetScheduledJobMaxAttempt(ctx, dbsqlc.GetScheduledJobMaxAttemptParams{
		JobID: string(id), ScheduledAt: scheduledAt.UTC().UnixMilli(),
	})
	return int(attempt) + 1, err
}

func retryWait(policy contracts.RetryPolicy, attempt int) time.Duration {
	wait := policy.InitialWait
	if wait <= 0 {
		wait = time.Second
	}
	for i := 1; i < attempt; i++ {
		wait *= 2
		if policy.MaxWait > 0 && wait >= policy.MaxWait {
			return policy.MaxWait
		}
	}
	return wait
}

func truncate(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 2000 {
		return message[:2000]
	}
	return message
}

func (s *Scheduler) updateNextRuns(ctx context.Context) error {
	for id := range s.jobs {
		if err := s.updateNextRun(ctx, id, s.now().UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) updateNextRun(ctx context.Context, id contracts.JobID, scheduledAt time.Time) error {
	definition, exists := s.definitions[id]
	if !exists {
		return nil
	}
	next, exists, err := nextRun(definition, scheduledAt, s.now().UTC())
	if err != nil {
		return err
	}
	if !exists {
		return s.queries.ClearScheduledJobNextRun(ctx, dbsqlc.ClearScheduledJobNextRunParams{UpdatedAt: s.now().UTC().UnixMilli(), ID: string(id)})
	}
	return s.queries.SetScheduledJobNextRun(ctx, dbsqlc.SetScheduledJobNextRunParams{
		NextRunAt: sql.NullInt64{Int64: next.UTC().UnixMilli(), Valid: true}, UpdatedAt: s.now().UTC().UnixMilli(), ID: string(id),
	})
}

func nextRun(definition contracts.JobDefinition, scheduledAt, now time.Time) (time.Time, bool, error) {
	switch definition.Schedule.Kind {
	case contracts.ScheduleInterval:
		next := scheduledAt.UTC().Add(definition.Schedule.Interval)
		if !next.After(now) {
			missed := now.Sub(next)/definition.Schedule.Interval + 1
			next = next.Add(missed * definition.Schedule.Interval)
		}
		return next, true, nil
	case contracts.ScheduleOnce:
		if definition.Schedule.RunAt.After(now) {
			return definition.Schedule.RunAt.UTC(), true, nil
		}
		return time.Time{}, false, nil
	case contracts.ScheduleCron:
		location, err := time.LoadLocation(definition.TimeZone)
		if err != nil {
			return time.Time{}, false, err
		}
		parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
		schedule, err := parser.Parse(definition.Schedule.Expression)
		if err != nil {
			return time.Time{}, false, fmt.Errorf("parse job %q schedule: %w", definition.ID, err)
		}
		return schedule.Next(now.In(location)).UTC(), true, nil
	default:
		return time.Time{}, false, fmt.Errorf("job %q has unsupported schedule kind %q", definition.ID, definition.Schedule.Kind)
	}
}
