package todo

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"workbench/internal/capabilities/notifications"
	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/events"
)

type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, contracts.NewEvent) (contracts.EventID, error) {
	return "", errors.New("event store unavailable")
}

func (failingPublisher) PublishTx(context.Context, *sql.Tx, contracts.NewEvent) (contracts.EventID, error) {
	return "", errors.New("event store unavailable")
}

func openTodoModule(t *testing.T) (*Module, *workbenchdb.Database) {
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
	eventStore := events.NewStore(database.SQL())
	notificationService, err := notifications.NewService(database.SQL(), eventStore)
	if err != nil {
		t.Fatal(err)
	}
	module, err := New(database.SQL(), eventStore, notificationService)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MigrateModule(ctx, "todo", module.Migrations()); err != nil {
		t.Fatal(err)
	}
	return module, database
}

func TestTaskLifecyclePublishesDomainEvents(t *testing.T) {
	module, database := openTodoModule(t)
	created, err := module.Create(context.Background(), CreateTask{Title: "  Write tests  ", Description: "Cover events"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Title != "Write tests" {
		t.Fatalf("created title = %q", created.Title)
	}
	completed := true
	updated, err := module.update(context.Background(), created.ID, UpdateTask{Completed: &completed})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CompletedAt == nil {
		t.Fatal("task was not completed")
	}
	rows, err := database.SQL().Query("SELECT topic FROM events_log ORDER BY created_at, id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var topics []string
	for rows.Next() {
		var topic string
		if err := rows.Scan(&topic); err != nil {
			t.Fatal(err)
		}
		topics = append(topics, topic)
	}
	if len(topics) != 2 || topics[0] != "todo.task.created" || topics[1] != "todo.task.completed" {
		t.Fatalf("topics = %v", topics)
	}
}

func TestDueScanCreatesOneIdempotentNotification(t *testing.T) {
	module, database := openTodoModule(t)
	now := time.Unix(1_000, 0).UTC()
	module.now = func() time.Time { return now }
	due := now.Add(-time.Minute)
	if _, err := module.Create(context.Background(), CreateTask{Title: "Overdue", DueAt: &due}); err != nil {
		t.Fatal(err)
	}
	run := contracts.JobRun{ID: "run", JobID: "todo.scan_due", ScheduledAt: now, Attempt: 1}
	if err := module.scanDue(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	if err := module.scanDue(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("notification count = %d, want 1", count)
	}
}

func TestCreateRollsBackWhenDomainEventFails(t *testing.T) {
	_, database := openTodoModule(t)
	notificationService, err := notifications.NewService(database.SQL(), events.NewStore(database.SQL()))
	if err != nil {
		t.Fatal(err)
	}
	module, err := New(database.SQL(), failingPublisher{}, notificationService)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.Create(context.Background(), CreateTask{Title: "Must roll back"}); err == nil {
		t.Fatal("Create succeeded when event publication failed")
	}
	var tasks int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM todo_tasks").Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 {
		t.Fatalf("tasks after event failure = %d, want 0", tasks)
	}
}
