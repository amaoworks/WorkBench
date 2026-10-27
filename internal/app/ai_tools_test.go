package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"workbench/internal/capabilities/ai"
	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

type fakeNotificationService struct {
	page      contracts.NotificationPage
	query     contracts.NotificationQuery
	markedIDs []string
	markCalls int
}

func (*fakeNotificationService) Create(context.Context, contracts.NewNotification) (contracts.Notification, error) {
	return contracts.Notification{}, nil
}
func (*fakeNotificationService) CreateTx(context.Context, *sql.Tx, contracts.NewNotification) (contracts.Notification, error) {
	return contracts.Notification{}, nil
}
func (s *fakeNotificationService) List(_ context.Context, query contracts.NotificationQuery) (contracts.NotificationPage, error) {
	s.query = query
	return s.page, nil
}
func (s *fakeNotificationService) MarkRead(_ context.Context, ids []string) error {
	s.markCalls++
	s.markedIDs = append([]string(nil), ids...)
	return nil
}
func (*fakeNotificationService) MarkAllRead(context.Context, time.Time) error { return nil }
func (*fakeNotificationService) Archive(context.Context, []string) error      { return nil }
func (*fakeNotificationService) UnreadCount(context.Context) (int64, error)   { return 0, nil }

func TestNotificationAIToolsUseContractsAndRuntimeSafety(t *testing.T) {
	ctx := context.Background()
	database, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}

	id := "0123456789abcdef0123456789abcdef"
	service := &fakeNotificationService{page: contracts.NotificationPage{Items: []contracts.Notification{{
		ID: id, SourceModule: "todo", Severity: contracts.NotificationInfo, Title: "待办提醒",
	}}}}
	runtime, err := ai.NewToolRuntime(database.SQL(), notificationAITools(service), func(id contracts.ModuleID) bool { return id == "core" })
	if err != nil {
		t.Fatal(err)
	}
	available := runtime.AvailableTools()
	if len(available) != 2 || available[0].Name != "notifications.list" || available[1].Name != "notifications.mark_read" {
		t.Fatalf("available tools = %+v", available)
	}

	listed, err := runtime.Execute(ctx, contracts.ToolCall{
		Name: "notifications.list", Arguments: json.RawMessage(`{"unreadOnly":true,"limit":10,"cursor":null}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if service.query != (contracts.NotificationQuery{UnreadOnly: true, Limit: 10}) || !json.Valid(listed.Content) {
		t.Fatalf("list query/result = %+v / %s", service.query, listed.Content)
	}

	call := contracts.ToolCall{
		Name: "notifications.mark_read", Arguments: json.RawMessage(`{"ids":["` + id + `"]}`), IdempotencyKey: "mark-read-1",
	}
	withoutKey := call
	withoutKey.IdempotencyKey = ""
	if _, err := runtime.Execute(ctx, withoutKey); err == nil {
		t.Fatal("low-write notification tool executed without an idempotency key")
	}
	if service.markCalls != 0 {
		t.Fatalf("mark calls = %d before an idempotent runtime call, want 0", service.markCalls)
	}
	first, err := runtime.Execute(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Execute(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	if service.markCalls != 1 || len(service.markedIDs) != 1 || service.markedIDs[0] != id {
		t.Fatalf("mark calls/ids = %d/%v, want one contract call", service.markCalls, service.markedIDs)
	}
	if string(first.Content) != string(second.Content) {
		t.Fatalf("deduplicated result = %s, want %s", second.Content, first.Content)
	}
}
