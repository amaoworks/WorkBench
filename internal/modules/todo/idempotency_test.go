package todo

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestCreateTaskIdempotencyPersistsOriginalResponseAndOneEvent(t *testing.T) {
	module, database := openTodoModule(t)
	ctx := todoContext()
	input := CreateTask{Title: "Original", Description: "first", IdempotencyKey: "create-request-1"}
	created, err := module.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	renamed := "Changed later"
	updated, err := module.update(ctx, created.ID, UpdateTask{Title: &renamed, Revision: &created.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 {
		t.Fatalf("updated revision = %d, want 2", updated.Revision)
	}
	replayed, err := module.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != created.ID || replayed.Title != created.Title || replayed.Revision != 1 {
		t.Fatalf("replayed task = %+v, want original %+v", replayed, created)
	}
	if _, err := database.SQL().ExecContext(ctx, "DELETE FROM todo_tasks WHERE id = ?", created.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err = module.Create(ctx, input)
	if err != nil || replayed.ID != created.ID || replayed.Title != created.Title {
		t.Fatalf("replay after delete = %+v, error = %v", replayed, err)
	}
	var createdEvents int
	if err := database.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM events_log WHERE topic = 'todo.task.created'").Scan(&createdEvents); err != nil {
		t.Fatal(err)
	}
	if createdEvents != 1 {
		t.Fatalf("created events = %d, want 1", createdEvents)
	}
}

func TestCreateTaskIdempotencyRejectsDifferentRequest(t *testing.T) {
	module, database := openTodoModule(t)
	ctx := todoContext()
	if _, err := module.Create(ctx, CreateTask{Title: "First", IdempotencyKey: "shared-key"}); err != nil {
		t.Fatal(err)
	}
	if _, err := module.Create(ctx, CreateTask{Title: "Second", IdempotencyKey: "shared-key"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflicting request error = %v", err)
	}
	var tasks, events int
	if err := database.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM todo_tasks").Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM events_log WHERE topic = 'todo.task.created'").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 || events != 1 {
		t.Fatalf("tasks/events = %d/%d, want 1/1", tasks, events)
	}
}

func TestConcurrentDuplicateCreateCommitsOnce(t *testing.T) {
	module, database := openTodoModule(t)
	input := CreateTask{Title: "Weak network retry", IdempotencyKey: "concurrent-key"}
	results := make(chan Task, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			task, err := module.Create(todoContext(), input)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- task
		}()
	}
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent create: %v", err)
	}
	var id string
	for task := range results {
		if id == "" {
			id = task.ID
		} else if task.ID != id {
			t.Fatalf("concurrent IDs = %s and %s", id, task.ID)
		}
	}
	var tasks, events int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM todo_tasks").Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM events_log WHERE topic = 'todo.task.created'").Scan(&events); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 || events != 1 {
		t.Fatalf("tasks/events = %d/%d, want 1/1", tasks, events)
	}
}

func TestTaskHTTPReturnsIdempotencyAndRevisionConflicts(t *testing.T) {
	module, database := openTodoModule(t)
	create := func(title, key string) *httptest.ResponseRecorder {
		request := todoRequest(http.MethodPost, "/api/modules/todo/tasks", strings.NewReader(`{"title":"`+title+`"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		module.createTask(recorder, request)
		return recorder
	}
	first := create("First", "http-key")
	if first.Code != http.StatusCreated {
		t.Fatalf("first create = %d %s", first.Code, first.Body.String())
	}
	conflict := create("Different", "http-key")
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `"code":"idempotency_conflict"`) {
		t.Fatalf("idempotency conflict = %d %s", conflict.Code, conflict.Body.String())
	}
	invalid := todoRequest(http.MethodPost, "/api/modules/todo/tasks", strings.NewReader(`{"title":"Invalid key"}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid.Header.Add("Idempotency-Key", "one")
	invalid.Header.Add("Idempotency-Key", "two")
	invalidRecorder := httptest.NewRecorder()
	module.createTask(invalidRecorder, invalid)
	if invalidRecorder.Code != http.StatusBadRequest {
		t.Fatalf("duplicate idempotency header = %d", invalidRecorder.Code)
	}

	var task Task
	if err := json.Unmarshal(first.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	newTitle := "Current"
	current, err := module.update(todoContext(), task.ID, UpdateTask{Title: &newTitle, Revision: &task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	request := todoRequest(http.MethodPatch, "/api/modules/todo/tasks/"+task.ID, strings.NewReader(`{"title":"Stale","revision":1}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", task.ID)
	recorder := httptest.NewRecorder()
	module.updateTask(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"todo_conflict"`) {
		t.Fatalf("revision conflict = %d %s", recorder.Code, recorder.Body.String())
	}
	var storedRevision int64
	if err := database.SQL().QueryRow("SELECT revision FROM todo_tasks WHERE id = ?", task.ID).Scan(&storedRevision); err != nil {
		t.Fatal(err)
	}
	if storedRevision != current.Revision || current.Revision != 2 {
		t.Fatalf("stored/current revision = %d/%d, want 2/2", storedRevision, current.Revision)
	}
	var updatedEvents int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM events_log WHERE topic = 'todo.task.updated'").Scan(&updatedEvents); err != nil {
		t.Fatal(err)
	}
	if updatedEvents != 1 {
		t.Fatalf("updated events = %d, want 1", updatedEvents)
	}
}
