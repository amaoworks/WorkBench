package todo

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"workbench/internal/contracts"
)

func TestTaskMetadataOrderingAndClearCategory(t *testing.T) {
	m, _ := openTodoModule(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	due := now.Add(24 * time.Hour)
	for _, priority := range []string{"none", "low", "high", "medium"} {
		if _, err := m.Create(ctx, CreateTask{Title: priority, DueAt: &due, Priority: priority, Category: " work "}); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
	}
	rec := httptest.NewRecorder()
	m.listTasks(rec, httptest.NewRequest("GET", "/?limit=10", nil))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var body struct {
		Items []Task `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := []string{"high", "medium", "low", "none"}
	for i, task := range body.Items {
		if task.Priority != want[i] || task.Category != "work" {
			t.Fatalf("item %d = %+v", i, task)
		}
	}
	updated, err := m.update(ctx, body.Items[0].ID, UpdateTask{ClearCategory: true})
	if err != nil || updated.Category != "" {
		t.Fatalf("clear category: %+v %v", updated, err)
	}
	bad := "urgent"
	if _, err := m.update(ctx, body.Items[0].ID, UpdateTask{Priority: &bad}); err == nil {
		t.Fatal("invalid priority accepted")
	}
}

func TestTaskViewsCountsAndDSTWeekBoundary(t *testing.T) {
	m, _ := openTodoModule(t)
	ctx := context.Background()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 6, 12, 0, 0, 0, loc)
	m.now = func() time.Time { return now }
	create := func(title string, due *time.Time) Task {
		task, err := m.Create(ctx, CreateTask{Title: title, DueAt: due})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	at := func(year int, month time.Month, day, hour int) *time.Time {
		value := time.Date(year, month, day, hour, 0, 0, 0, loc)
		return &value
	}
	create("overdue", at(2026, time.March, 6, 11))
	create("today", at(2026, time.March, 6, 18))
	create("weekend", at(2026, time.March, 8, 12))
	create("later", at(2026, time.March, 9, 0))
	create("none", nil)
	done := create("done", at(2026, time.March, 6, 10))
	completed := true
	if _, err := m.update(ctx, done.ID, UpdateTask{Completed: &completed}); err != nil {
		t.Fatal(err)
	}

	for view, want := range map[string]int{"today": 2, "week": 3, "later": 2, "done": 1, "overdue": 1} {
		rec := httptest.NewRecorder()
		m.listTasks(rec, httptest.NewRequest("GET", "/?view="+view+"&tz=America%2FNew_York", nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %s", view, rec.Body.String())
		}
		var body struct {
			Items []Task `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Items) != want {
			t.Fatalf("%s items=%d err=%v", view, len(body.Items), err)
		}
	}
	rec := httptest.NewRecorder()
	m.taskCounts(rec, httptest.NewRequest("GET", "/?tz=America%2FNew_York", nil))
	var counts TaskCounts
	if err := json.Unmarshal(rec.Body.Bytes(), &counts); err != nil {
		t.Fatal(err)
	}
	if counts != (TaskCounts{Overdue: 1, Today: 1, Week: 1, Later: 1, None: 1, Done: 1}) {
		t.Fatalf("counts = %+v", counts)
	}
}

func TestAgendaCompletedTodayUsesRequestedTimeZone(t *testing.T) {
	m, _ := openTodoModule(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 7, 4, 30, 0, 0, time.UTC) // March 6 in New York.
	m.now = func() time.Time { return now }
	yesterday, err := m.Create(ctx, CreateTask{Title: "yesterday"})
	if err != nil {
		t.Fatal(err)
	}
	completed := true
	if _, err := m.update(ctx, yesterday.ID, UpdateTask{Completed: &completed}); err != nil {
		t.Fatal(err)
	}
	now = time.Date(2026, 3, 7, 15, 0, 0, 0, time.UTC)
	today, err := m.Create(ctx, CreateTask{Title: "today"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.update(ctx, today.ID, UpdateTask{Completed: &completed}); err != nil {
		t.Fatal(err)
	}
	agenda, err := m.agenda(ctx, "America/New_York")
	if err != nil || agenda.CompletedToday != 1 {
		t.Fatalf("agenda=%+v err=%v", agenda, err)
	}
}

func TestTodoAIToolsListAndComplete(t *testing.T) {
	m, db := openTodoModule(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	due := now.Add(time.Hour)
	created, err := m.createTaskTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(
		`{"title":"AI task","description":null,"dueAt":"` + due.Format(time.RFC3339) + `","priority":"high","category":"work"}`,
	)})
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	if err := json.Unmarshal(created.Content, &task); err != nil || task.Priority != "high" || task.Category != "work" {
		t.Fatalf("created=%s err=%v", created.Content, err)
	}
	listed, err := m.listTasksTool(ctx, contracts.ToolCall{Arguments: json.RawMessage(`{"view":"today","timeZone":"UTC","limit":10}`)})
	if err != nil || !json.Valid(listed.Content) || !containsJSONID(listed.Content, task.ID) {
		t.Fatalf("list result=%s err=%v", listed.Content, err)
	}
	call := contracts.ToolCall{Arguments: json.RawMessage(`{"taskId":"` + task.ID + `"}`)}
	if _, err := m.completeTaskTool(ctx, call); err != nil {
		t.Fatal(err)
	}
	var eventCount int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM events_log WHERE topic = 'todo.task.completed'").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if _, err := m.completeTaskTool(ctx, call); err != nil {
		t.Fatal(err)
	}
	var repeated int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM events_log WHERE topic = 'todo.task.completed'").Scan(&repeated); err != nil || repeated != eventCount {
		t.Fatalf("repeated completion events=%d want=%d err=%v", repeated, eventCount, err)
	}
}

func containsJSONID(data []byte, id string) bool {
	var body struct {
		Items []Task `json:"items"`
	}
	if json.Unmarshal(data, &body) != nil {
		return false
	}
	return len(body.Items) == 1 && body.Items[0].ID == id
}
