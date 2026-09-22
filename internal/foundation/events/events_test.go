package events

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

func openTestDatabase(t *testing.T) *workbenchdb.Database {
	t.Helper()
	database, err := workbenchdb.Open(context.Background(), workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(context.Background()); err != nil {
		t.Fatalf("MigrateCore() error = %v", err)
	}
	return database
}

func TestPublishTxRollsBackWithBusinessTransaction(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database.SQL())
	tx, err := database.SQL().BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PublishTx(context.Background(), tx, contracts.NewEvent{
		Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo", Payload: []byte(`{"id":"one"}`),
	})
	if err != nil {
		t.Fatalf("PublishTx() error = %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM events_log").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("events after rollback = %d, want 0", count)
	}
}

type retryHint struct {
	delay     time.Duration
	permanent bool
}

func (retryHint) Error() string               { return "external delivery failure" }
func (e retryHint) RetryAfter() time.Duration { return e.delay }
func (e retryHint) Permanent() bool           { return e.permanent }

func TestDispatcherPersistsRetryAfterAndPermanentFailure(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		db := openTestDatabase(t)
		store := NewStore(db.SQL())
		consumer := contracts.EventConsumer{ID: "core.external", Module: "core", Topics: []string{"core.test.created"}, MaxAttempts: 12,
			Handler: func(context.Context, contracts.Event) error { return retryHint{delay: time.Hour, permanent: permanent} }}
		dispatcher, err := NewDispatcher(db.SQL(), store, []contracts.EventConsumer{consumer}, func(contracts.ModuleID) bool { return true }, testLogger())
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(time.Second).UTC()
		dispatcher.now = func() time.Time { return now }
		if _, err := store.Publish(context.Background(), contracts.NewEvent{Topic: "core.test.created", SchemaVersion: 1, SourceModule: "core", Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.DispatchOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		var status string
		var next int64
		if err := db.SQL().QueryRow("SELECT status,COALESCE(next_attempt_at,0) FROM event_deliveries WHERE consumer_id='core.external'").Scan(&status, &next); err != nil {
			t.Fatal(err)
		}
		if permanent {
			if status != "dead" || next != 0 {
				t.Fatal(status, next)
			}
		} else if status != "retry" || next != now.Add(time.Hour).UnixMilli() {
			t.Fatal("Retry-After not persisted", status, next)
		}
	}
}

func TestDispatcherDeliversOnceAfterSuccess(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database.SQL())
	var calls atomic.Int32
	consumer := contracts.EventConsumer{
		ID: "todo.notification", Module: "todo", Topics: []string{"todo.task.created"},
		Timeout: time.Second, MaxAttempts: 3,
		Handler: func(context.Context, contracts.Event) error {
			calls.Add(1)
			return nil
		},
	}
	dispatcher, err := NewDispatcher(database.SQL(), store, []contracts.EventConsumer{consumer}, func(contracts.ModuleID) bool { return true }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(context.Background(), contracts.NewEvent{
		Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
}

func TestDispatcherPausesDisabledConsumer(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database.SQL())
	var calls atomic.Int32
	var enabled atomic.Bool
	consumer := contracts.EventConsumer{
		ID: "todo.notification", Module: "todo", Topics: []string{"todo.task.created"},
		Handler: func(context.Context, contracts.Event) error { calls.Add(1); return nil },
	}
	dispatcher, err := NewDispatcher(database.SQL(), store, []contracts.EventConsumer{consumer}, func(contracts.ModuleID) bool { return enabled.Load() }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(context.Background(), contracts.NewEvent{
		Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("disabled consumer was called")
	}
	enabled.Store(true)
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("re-enabled consumer did not process backlog")
	}
}

func TestDispatcherMovesRepeatedFailureToDead(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database.SQL())
	consumer := contracts.EventConsumer{
		ID: "todo.failure", Module: "todo", Topics: []string{"todo.task.created"},
		MaxAttempts: 2, Handler: func(context.Context, contracts.Event) error { return errors.New("boom") },
	}
	dispatcher, err := NewDispatcher(database.SQL(), store, []contracts.EventConsumer{consumer}, func(contracts.ModuleID) bool { return true }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	// Publish uses the store clock, so keep the dispatcher clock safely after
	// the event's millisecond availability timestamp. This also avoids a
	// scheduler-sensitive race under `go test -race`.
	base := time.Now().UTC().Add(time.Second)
	dispatcher.now = func() time.Time { return base }
	if _, err := store.Publish(context.Background(), contracts.NewEvent{
		Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	dispatcher.now = func() time.Time { return base.Add(2 * time.Second) }
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	if err := database.SQL().QueryRow("SELECT status, attempts FROM event_deliveries WHERE consumer_id = ?", consumer.ID).Scan(&status, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "dead" || attempts != 2 {
		t.Fatalf("delivery = (%s, %d), want (dead, 2)", status, attempts)
	}
}

func TestDispatcherRecoversExpiredRunningLease(t *testing.T) {
	database := openTestDatabase(t)
	store := NewStore(database.SQL())
	var calls atomic.Int32
	enabled := false
	consumer := contracts.EventConsumer{
		ID: "todo.recovery", Module: "todo", Topics: []string{"todo.task.created"},
		Timeout: time.Second, MaxAttempts: 3,
		Handler: func(context.Context, contracts.Event) error {
			calls.Add(1)
			return nil
		},
	}
	dispatcher, err := NewDispatcher(database.SQL(), store, []contracts.EventConsumer{consumer}, func(contracts.ModuleID) bool { return enabled }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(time.Second)
	dispatcher.now = func() time.Time { return base }
	eventID, err := store.Publish(context.Background(), contracts.NewEvent{
		Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo", Payload: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	lockedUntil := base.Add(time.Minute)
	if _, err := database.SQL().Exec(`
		UPDATE event_deliveries
		SET status = 'running', attempts = 1, locked_until = ?
		WHERE event_id = ? AND consumer_id = ?`, lockedUntil.UnixMilli(), eventID, consumer.ID); err != nil {
		t.Fatal(err)
	}

	enabled = true
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("delivery was reclaimed before its lease expired")
	}
	dispatcher.now = func() time.Time { return lockedUntil.Add(time.Millisecond) }
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls after lease expiry = %d, want 1", calls.Load())
	}
	var status string
	if err := database.SQL().QueryRow(`
		SELECT status FROM event_deliveries
		WHERE event_id = ? AND consumer_id = ?`, eventID, consumer.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Fatalf("delivery status = %q, want succeeded", status)
	}
}

func TestRetryDoesNotDuplicateIdempotentConsumerSideEffect(t *testing.T) {
	database := openTestDatabase(t)
	if _, err := database.SQL().Exec("CREATE TABLE consumer_effects(id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	store := NewStore(database.SQL())
	var calls atomic.Int32
	consumer := contracts.EventConsumer{
		ID: "todo.idempotent", Module: "todo", Topics: []string{"todo.task.created"},
		Timeout: time.Second, MaxAttempts: 3,
		Handler: func(ctx context.Context, event contracts.Event) error {
			if _, err := database.SQL().ExecContext(ctx, "INSERT OR IGNORE INTO consumer_effects(id) VALUES (?)", event.ID); err != nil {
				return err
			}
			if calls.Add(1) == 1 {
				return errors.New("crashed after committing side effect")
			}
			return nil
		},
	}
	dispatcher, err := NewDispatcher(database.SQL(), store, []contracts.EventConsumer{consumer}, func(contracts.ModuleID) bool { return true }, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Add(time.Second)
	dispatcher.now = func() time.Time { return base }
	if _, err := store.Publish(context.Background(), contracts.NewEvent{
		Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo", Payload: []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	dispatcher.now = func() time.Time { return base.Add(2 * time.Second) }
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var effects int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM consumer_effects").Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if effects != 1 || calls.Load() != 2 {
		t.Fatalf("side effects/handler calls = %d/%d, want 1/2", effects, calls.Load())
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
