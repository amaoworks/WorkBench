package todo

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"workbench/internal/contracts"
	todosqlc "workbench/internal/modules/todo/sqlc"
)

type disablingDirectory struct{ checks int }

func (*disablingDirectory) ActiveUserIDs(context.Context) ([]string, error) {
	return []string{"alice"}, nil
}

func (d *disablingDirectory) IsUserActive(context.Context, string) (bool, error) {
	d.checks++
	return d.checks == 1, nil
}

func (d *disablingDirectory) IsUserActiveTx(ctx context.Context, _ *sql.Tx, id string) (bool, error) {
	return d.IsUserActive(ctx, id)
}

func TestDueScanRechecksDisabledUserBeforeDelivery(t *testing.T) {
	m, _ := openTodoModule(t)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	due := now.Add(-time.Minute)
	if _, err := m.Create(userContext("alice"), CreateTask{Title: "private", DueAt: &due}); err != nil {
		t.Fatal(err)
	}
	m.users = &disablingDirectory{}
	recorder := &capturedNotifications{}
	m.notifications = recorder
	if err := m.scanDue(context.Background(), contracts.JobRun{}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.items) != 0 {
		t.Fatalf("sent notification after disable: %+v", recorder.items)
	}
}

func TestWallosJobRechecksDisabledOwnerAfterNetworkFetch(t *testing.T) {
	m, db := openTodoModule(t)
	m.now = func() time.Time { return time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC) }
	m.users = &disablingDirectory{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"subscriptions":[{"id":1,"name":"Private","next_payment":"2026-09-10"}]}`))
	}))
	defer server.Close()
	if err := m.queries.SaveWallosSettings(userContext("alice"), todosqlc.SaveWallosSettingsParams{
		UserID: "alice", Config: `{"enabled":true,"baseUrl":"` + server.URL + `","apiKey":"secret","daysBefore":3,"reminderHour":9,"timeZone":"Asia/Shanghai"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.forActiveUsers(context.Background(), func(ctx context.Context) error {
		_, err := m.syncWallos(ctx)
		return err
	}); err == nil {
		t.Fatal("expected disabled-user error")
	}
	for _, table := range []string{"todo_tasks", "todo_wallos_occurrences", "events_log"} {
		var count int
		if err := db.SQL().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s wrote data after disable: %d %v", table, count, err)
		}
	}
}

func TestTodoJobsRejectDirectoryWithMissingOwner(t *testing.T) {
	m, _ := openTodoModule(t)
	m.users = todoUsers{""}
	called := false
	if err := m.forActiveUsers(context.Background(), func(context.Context) error { called = true; return nil }); err == nil || called {
		t.Fatalf("empty owner accepted: called=%v err=%v", called, err)
	}
}
