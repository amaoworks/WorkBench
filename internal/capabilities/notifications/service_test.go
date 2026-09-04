package notifications

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/events"
)

func openService(t *testing.T) (*Service, *workbenchdb.Database) {
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
	service, err := NewService(database.SQL(), events.NewStore(database.SQL()))
	if err != nil {
		t.Fatal(err)
	}
	return service, database
}

func TestCreateIsIdempotentAndPublishesOnce(t *testing.T) {
	service, database := openService(t)
	input := contracts.NewNotification{
		SourceModule: "todo", Severity: contracts.NotificationInfo,
		Title: "Task due", Content: "A task is due", ActionRoute: "/todo",
		IdempotencyKey: "todo:due:one",
	}
	first, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent create returned %q and %q", first.ID, second.ID)
	}
	var notifications, eventCount int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM notifications").Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM events_log WHERE topic = 'core.notification.created'").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if notifications != 1 || eventCount != 1 {
		t.Fatalf("notifications/events = %d/%d, want 1/1", notifications, eventCount)
	}
}

func TestNotificationLifecycle(t *testing.T) {
	service, _ := openService(t)
	created, err := service.Create(context.Background(), contracts.NewNotification{
		SourceModule: "todo", Severity: contracts.NotificationWarning,
		Title: "Due", Content: "Review", IdempotencyKey: "todo:due:two",
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := service.UnreadCount(context.Background()); err != nil || count != 1 {
		t.Fatalf("UnreadCount() = %d, %v", count, err)
	}
	if err := service.MarkRead(context.Background(), []string{created.ID}); err != nil {
		t.Fatal(err)
	}
	if count, err := service.UnreadCount(context.Background()); err != nil || count != 0 {
		t.Fatalf("UnreadCount() after read = %d, %v", count, err)
	}
	page, err := service.List(context.Background(), contracts.NotificationQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ReadAt == nil {
		t.Fatalf("read notification not returned correctly: %+v", page.Items)
	}
	if err := service.Archive(context.Background(), []string{created.ID}); err != nil {
		t.Fatal(err)
	}
	page, err = service.List(context.Background(), contracts.NotificationQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("archived notification remained visible: %+v", page.Items)
	}
}

func TestCreateRejectsExternalActionRoute(t *testing.T) {
	service, _ := openService(t)
	_, err := service.Create(context.Background(), contracts.NewNotification{
		SourceModule: "todo", Severity: contracts.NotificationInfo,
		Title: "Unsafe", Content: "Unsafe route", ActionRoute: "https://example.com",
		IdempotencyKey: "unsafe",
	})
	if err == nil {
		t.Fatal("Create() accepted external action route")
	}
}

func TestListUsesStableCursorPagination(t *testing.T) {
	service, _ := openService(t)
	base := time.Unix(1_000, 0).UTC()
	for index, title := range []string{"first", "second", "third"} {
		service.now = func() time.Time { return base.Add(time.Duration(index) * time.Millisecond) }
		if _, err := service.Create(context.Background(), contracts.NewNotification{
			SourceModule: "todo", Severity: contracts.NotificationInfo, Title: title,
			Content: title, IdempotencyKey: "page:" + title,
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.List(context.Background(), contracts.NotificationQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" || first.Items[0].Title != "third" || first.Items[1].Title != "second" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := service.List(context.Background(), contracts.NotificationQuery{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.NextCursor != "" || second.Items[0].Title != "first" {
		t.Fatalf("second page = %+v", second)
	}
}
