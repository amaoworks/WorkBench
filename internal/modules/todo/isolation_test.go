package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workbench/internal/contracts"
	todosqlc "workbench/internal/modules/todo/sqlc"
)

func userContext(id string) context.Context {
	return contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: id, Role: contracts.RoleUser})
}

func userRequest(id, method, target, body string) *http.Request {
	return httptest.NewRequest(method, target, strings.NewReader(body)).WithContext(userContext(id))
}

type ownedEvents struct {
	contracts.EventPublisher
	events []contracts.NewEvent
}

func (r *ownedEvents) PublishTx(ctx context.Context, tx *sql.Tx, event contracts.NewEvent) (contracts.EventID, error) {
	r.events = append(r.events, event)
	return r.EventPublisher.PublishTx(ctx, tx, event)
}

func TestPrivateTaskCRUDAndIdempotency(t *testing.T) {
	m, db := openTodoModule(t)
	ctxA, ctxB := userContext("alice"), userContext("bob")
	recorder := &ownedEvents{EventPublisher: m.events}
	m.events = recorder
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	due := now.Add(-time.Hour)
	a, err := m.Create(ctxA, CreateTask{Title: "Alice", DueAt: &due, IdempotencyKey: "same-key"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Create(ctxB, CreateTask{Title: "Bob", DueAt: &due, IdempotencyKey: "same-key"})
	if err != nil || b.ID == a.ID {
		t.Fatalf("independent idempotency: %+v %v", b, err)
	}
	replayed, err := m.Create(ctxA, CreateTask{Title: "Alice", DueAt: &due, IdempotencyKey: "same-key"})
	if err != nil || replayed.ID != a.ID {
		t.Fatalf("replay: %+v %v", replayed, err)
	}
	if _, err := m.Create(ctxA, CreateTask{Title: "Different", IdempotencyKey: "same-key"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict = %v", err)
	}
	if _, err := m.queries.GetTask(ctxB, todosqlc.GetTaskParams{ID: a.ID, UserID: "bob"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign detail = %v", err)
	}
	title := "Hijacked"
	if _, err := m.update(ctxB, a.ID, UpdateTask{Title: &title}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign update = %v", err)
	}
	request := userRequest("bob", "DELETE", "/", "")
	request.SetPathValue("id", a.ID)
	w := httptest.NewRecorder()
	m.deleteTask(w, request)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign delete: %d %s", w.Code, w.Body.String())
	}
	for id, own := range map[string]Task{"alice": a, "bob": b} {
		w = httptest.NewRecorder()
		m.listTasks(w, userRequest(id, "GET", "/?view=all", ""))
		var page struct {
			Items []Task `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != own.ID {
			t.Fatalf("%s list: %s %v", id, w.Body.String(), err)
		}
		w = httptest.NewRecorder()
		m.taskCounts(w, userRequest(id, "GET", "/", ""))
		var counts TaskCounts
		if err := json.Unmarshal(w.Body.Bytes(), &counts); err != nil || counts.Overdue != 1 {
			t.Fatalf("%s counts: %s %v", id, w.Body.String(), err)
		}
		w = httptest.NewRecorder()
		m.summary(w, userRequest(id, "GET", "/", ""))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"open":1`) {
			t.Fatalf("%s summary: %s", id, w.Body.String())
		}
		agenda, err := m.agenda(userContext(id), "UTC")
		if err != nil || agenda.Total != 1 || len(agenda.Items) != 1 || agenda.Items[0].ID != own.ID {
			t.Fatalf("%s agenda: %+v %v", id, agenda, err)
		}
	}
	if len(recorder.events) != 2 || recorder.events[0].UserID != "alice" || recorder.events[1].UserID != "bob" {
		t.Fatalf("event owners: %+v", recorder.events)
	}
	request = userRequest("alice", "DELETE", "/", "")
	request.SetPathValue("id", a.ID)
	w = httptest.NewRecorder()
	m.deleteTask(w, request)
	if w.Code != http.StatusNoContent || recorder.events[len(recorder.events)-1].UserID != "alice" {
		t.Fatalf("owner delete: %d %s", w.Code, w.Body.String())
	}
	var count int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM todo_tasks WHERE user_id = 'bob'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("Bob changed: %d %v", count, err)
	}
}

func TestTodoRejectsMissingIdentity(t *testing.T) {
	m, db := openTodoModule(t)
	ctx := context.Background()
	if _, err := m.Create(ctx, CreateTask{Title: "No user"}); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("create: %v", err)
	}
	if _, err := m.update(ctx, "missing", UpdateTask{}); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("update: %v", err)
	}
	if _, err := m.agenda(ctx, "UTC"); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("agenda: %v", err)
	}
	if _, err := m.loadWallos(ctx); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("settings: %v", err)
	}
	if _, err := m.syncWallos(ctx); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("sync: %v", err)
	}
	if _, err := m.importWallos(ctx, wallosTestConfig(), nil); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("import: %v", err)
	}
	for name, handler := range map[string]http.HandlerFunc{
		"create": m.createTask, "update": m.updateTask, "delete": m.deleteTask,
		"list": m.listTasks, "counts": m.taskCounts, "summary": m.summary, "agenda": m.agendaHTTP,
		"wallos read": m.getWallos, "wallos write": m.saveWallos, "wallos sync": m.syncWallosHTTP,
	} {
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s status = %d", name, w.Code)
		}
	}
	for name, handler := range map[string]func(context.Context, contracts.ToolCall) (contracts.ToolResult, error){
		"create": m.createTaskTool, "list": m.listTasksTool, "update": m.updateTaskTool, "complete": m.completeTaskTool,
	} {
		args := json.RawMessage(`{"title":"No user","view":"all","timeZone":"UTC","limit":10,"taskId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
		if _, err := handler(ctx, contracts.ToolCall{Arguments: args}); !errors.Is(err, contracts.ErrUnauthenticated) {
			t.Errorf("%s tool: %v", name, err)
		}
	}
	m.users = nil
	if err := m.scanDue(ctx, contracts.JobRun{}); err == nil {
		t.Fatal("job silently ran without user directory")
	}
	var tasks int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM todo_tasks").Scan(&tasks); err != nil || tasks != 0 {
		t.Fatalf("unauthenticated writes: %d %v", tasks, err)
	}
}

func TestTodoToolsCannotAccessAnotherUser(t *testing.T) {
	m, _ := openTodoModule(t)
	a, err := m.Create(userContext("alice"), CreateTask{Title: "Alice private"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := userContext("bob")
	listed, err := m.listTasksTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"view":"all","timeZone":"UTC","limit":10}`)})
	if err != nil || strings.Contains(string(listed.Content), a.ID) {
		t.Fatalf("foreign list: %s %v", listed.Content, err)
	}
	args := json.RawMessage(`{"taskId":"` + a.ID + `","title":"Hijacked"}`)
	if _, err := m.updateTaskTool(ctx, contracts.ToolCall{Arguments: args}); err == nil {
		t.Fatal("tool updated foreign task")
	}
	if _, err := m.completeTaskTool(ctx, contracts.ToolCall{Arguments: args}); err == nil {
		t.Fatal("tool completed foreign task")
	}
}

func TestWallosSettingsOccurrencesAndStatusArePrivate(t *testing.T) {
	m, db := openTodoModule(t)
	m.users = todoUsers{"alice", "bob"}
	m.now = func() time.Time { return time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("api_key") == "alice-secret" {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"subscriptions":[]}`))
	}))
	defer server.Close()
	for _, id := range []string{"alice", "bob"} {
		c := wallosTestConfig()
		c.BaseURL, c.APIKey = server.URL, id+"-secret"
		data, _ := json.Marshal(c)
		w := httptest.NewRecorder()
		m.saveWallos(w, userRequest(id, "PUT", "/", string(data)))
		if w.Code != 200 {
			t.Fatalf("%s settings: %s", id, w.Body.String())
		}
		stored, err := m.loadWallos(userContext(id))
		if err != nil || stored.APIKey != id+"-secret" {
			t.Fatalf("%s stored: %+v %v", id, stored, err)
		}
		subs := []wallosSubscription{{ID: "1", Name: id + " subscription", NextPayment: "2026-09-10"}}
		if n, err := m.importWallos(userContext(id), c, subs); err != nil || n != 1 {
			t.Fatalf("%s import: %d %v", id, n, err)
		}
		if n, err := m.importWallos(userContext(id), c, subs); err != nil || n != 0 {
			t.Fatalf("%s dedup: %d %v", id, n, err)
		}
	}
	if err := m.forActiveUsers(context.Background(), func(ctx context.Context) error {
		_, err := m.syncWallos(ctx)
		return err
	}); err == nil {
		t.Fatal("Alice sync should fail without preventing Bob's sync")
	}
	a, _ := m.loadWallos(userContext("alice"))
	b, _ := m.loadWallos(userContext("bob"))
	if a.LastError == "" || a.LastSync != nil || b.LastError != "" || b.LastSync == nil {
		t.Fatalf("sync status crossed: alice=%+v bob=%+v", a, b)
	}
	for _, id := range []string{"alice", "bob"} {
		w := httptest.NewRecorder()
		m.getWallos(w, userRequest(id, "GET", "/", ""))
		if strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("secret in HTTP response: %s", w.Body.String())
		}
		var count int
		if err := db.SQL().QueryRow("SELECT COUNT(*) FROM todo_wallos_occurrences WHERE user_id=?", id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s occurrences: %d %v", id, count, err)
		}
	}
	// A stale/corrupted mapping must not make another user's task visible or mutable.
	if _, err := db.SQL().Exec("UPDATE todo_wallos_occurrences SET task_id=(SELECT id FROM todo_tasks WHERE user_id='alice') WHERE user_id='bob'"); err != nil {
		t.Fatal(err)
	}
	c := wallosTestConfig()
	c.BaseURL = server.URL
	if n, err := m.importWallos(userContext("bob"), c, []wallosSubscription{{ID: "1", Name: "Bob renewed", NextPayment: "2026-09-10"}}); err != nil || n != 1 {
		t.Fatalf("repair foreign reference: %d %v", n, err)
	}
	var title string
	if err := db.SQL().QueryRow("SELECT title FROM todo_tasks WHERE user_id='alice'").Scan(&title); err != nil || title != wallosTitlePrefix+"alice subscription" {
		t.Fatalf("Alice task changed: %q %v", title, err)
	}
}

type capturedNotifications struct {
	contracts.NotificationService
	items    []contracts.NewNotification
	contexts []string
	failUser string
}

func (r *capturedNotifications) Create(ctx context.Context, n contracts.NewNotification) (contracts.Notification, error) {
	r.items = append(r.items, n)
	r.contexts = append(r.contexts, contracts.UserID(ctx))
	if n.RecipientID == r.failUser {
		return contracts.Notification{}, errors.New("notification unavailable")
	}
	return contracts.Notification{}, nil
}

func TestRemindersEnumerateOnlyActiveOwnersAndDoNotStopAtOneFailure(t *testing.T) {
	m, _ := openTodoModule(t)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	due := now.Add(-time.Minute)
	for _, id := range []string{"alice", "bob", "disabled"} {
		if _, err := m.Create(userContext(id), CreateTask{Title: id + " private", DueAt: &due}); err != nil {
			t.Fatal(err)
		}
	}
	m.users = todoUsers{"alice", "bob"}
	recorder := &capturedNotifications{failUser: "alice"}
	m.notifications = recorder
	if err := m.scanDue(context.Background(), contracts.JobRun{}); err == nil {
		t.Fatal("expected Alice delivery failure")
	}
	if len(recorder.items) != 2 {
		t.Fatalf("reminders = %+v", recorder.items)
	}
	for i, id := range []string{"alice", "bob"} {
		n := recorder.items[i]
		if n.RecipientID != id || recorder.contexts[i] != id || n.Content != id+" private" {
			t.Fatalf("wrong recipient: %+v context=%s", n, recorder.contexts[i])
		}
	}
}
