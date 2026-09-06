package todo

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
	"workbench/internal/foundation/identity"
	"workbench/internal/modules/todo/sqlc"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Module struct {
	db            *sql.DB
	events        contracts.EventPublisher
	notifications contracts.NotificationService
	queries       *todosqlc.Queries
	now           func() time.Time
}

func New(db *sql.DB, events contracts.EventPublisher, notifications contracts.NotificationService) (*Module, error) {
	if db == nil || events == nil || notifications == nil {
		return nil, errors.New("database, events, and notifications are required")
	}
	return &Module{db: db, events: events, notifications: notifications, queries: todosqlc.New(db), now: time.Now}, nil
}

func (m *Module) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{
		ID: "todo", Name: "待办", Version: "0.1.0", ContractVersion: 1, Icon: "check-square",
		Navigation: []contracts.NavigationItem{{Label: "待办", Route: "/todo", PageKey: "todo.list", Order: 10}},
	}
}

func (m *Module) Migrations() contracts.MigrationSet {
	return contracts.MigrationSet{FS: migrations, Dir: "migrations"}
}

func (m *Module) Register(r contracts.ModuleRegistrar) error {
	registrations := []error{
		r.Handle(http.MethodGet, "/api/modules/todo/tasks", http.HandlerFunc(m.listTasks)),
		r.Handle(http.MethodPost, "/api/modules/todo/tasks", http.HandlerFunc(m.createTask)),
		r.Handle(http.MethodPatch, "/api/modules/todo/tasks/{id}", http.HandlerFunc(m.updateTask)),
		r.Handle(http.MethodDelete, "/api/modules/todo/tasks/{id}", http.HandlerFunc(m.deleteTask)),
		r.Handle(http.MethodGet, "/api/modules/todo/widget/summary", http.HandlerFunc(m.summary)),
		r.Widget(contracts.WidgetDefinition{
			ID: "todo.summary", Module: "todo", SchemaVersion: 1, Title: "待办概览", WidgetKind: "todo.summary",
			DataRoute: "/api/modules/todo/widget/summary", Size: contracts.WidgetSmall, Order: 10,
		}),
		r.Job(contracts.JobDefinition{
			ID: "todo.scan_due", Module: "todo",
			Schedule: contracts.ScheduleSpec{Kind: contracts.ScheduleInterval, Interval: time.Minute},
			TimeZone: "UTC", Timeout: 30 * time.Second,
			OverlapPolicy: contracts.OverlapSkip, MisfirePolicy: contracts.MisfireRunOnce,
			Retry:   contracts.RetryPolicy{MaxAttempts: 3, InitialWait: time.Second, MaxWait: 30 * time.Second},
			Handler: m.scanDue,
		}),
		r.Tool(contracts.AITool{
			Name: "todo.create_task", Module: "todo", SchemaVersion: 1, Description: "创建一条待办任务",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"description":{"type":["string","null"]},"dueAt":{"type":["string","null"],"format":"date-time"}},"required":["title","description","dueAt"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskLowWrite, Handler: m.createTaskTool,
		}),
	}
	return errors.Join(registrations...)
}

type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	DueAt       *time.Time `json:"dueAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type CreateTask struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	DueAt       *time.Time `json:"dueAt"`
}

func (m *Module) Create(ctx context.Context, input CreateTask) (Task, error) {
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len(input.Title) > 300 {
		return Task{}, errors.New("title must contain 1 to 300 bytes")
	}
	if len(input.Description) > 10_000 {
		return Task{}, errors.New("description exceeds 10000 bytes")
	}
	id, err := identity.New()
	if err != nil {
		return Task{}, err
	}
	now := m.now().UTC()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	err = m.queries.WithTx(tx).CreateTask(ctx, todosqlc.CreateTaskParams{
		ID: id, Title: input.Title, Description: input.Description,
		DueAt: toNullTime(input.DueAt), CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	})
	if err != nil {
		return Task{}, fmt.Errorf("insert task: %w", err)
	}
	payload, _ := json.Marshal(map[string]string{"taskId": id, "title": input.Title})
	if _, err := m.events.PublishTx(ctx, tx, contracts.NewEvent{
		Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo", AggregateID: id, Payload: payload,
	}); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return Task{ID: id, Title: input.Title, Description: input.Description, DueAt: input.DueAt, CreatedAt: now, UpdatedAt: now}, nil
}

func (m *Module) createTask(w http.ResponseWriter, r *http.Request) {
	var input CreateTask
	if err := httpapi.Decode(w, r, &input, 16*1024); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid task")
		return
	}
	task, err := m.Create(r.Context(), input)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "todo_create_failed", err.Error())
		return
	}
	httpapi.Write(w, http.StatusCreated, task)
}

type UpdateTask struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	DueAt       *time.Time `json:"dueAt"`
	ClearDueAt  bool       `json:"clearDueAt"`
	Completed   *bool      `json:"completed"`
}

func (m *Module) updateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input UpdateTask
	if id == "" || httpapi.Decode(w, r, &input, 16*1024) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid task update")
		return
	}
	task, err := m.update(r.Context(), id, input)
	if errors.Is(err, sql.ErrNoRows) {
		httpapi.Error(w, http.StatusNotFound, "todo_not_found", "task not found")
		return
	}
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "todo_update_failed", err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, task)
}

func (m *Module) update(ctx context.Context, id string, input UpdateTask) (Task, error) {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	row, err := m.queries.WithTx(tx).GetTask(ctx, id)
	if err != nil {
		return Task{}, err
	}
	task := taskFromRow(row)
	if input.Title != nil {
		title := strings.TrimSpace(*input.Title)
		if title == "" || len(title) > 300 {
			return Task{}, errors.New("title must contain 1 to 300 bytes")
		}
		task.Title = title
	}
	if input.Description != nil {
		if len(*input.Description) > 10_000 {
			return Task{}, errors.New("description exceeds 10000 bytes")
		}
		task.Description = *input.Description
	}
	if input.ClearDueAt {
		task.DueAt = nil
	} else if input.DueAt != nil {
		due := input.DueAt.UTC()
		task.DueAt = &due
	}
	topic := "todo.task.updated"
	if input.Completed != nil {
		if *input.Completed && task.CompletedAt == nil {
			now := m.now().UTC()
			task.CompletedAt = &now
			topic = "todo.task.completed"
		} else if !*input.Completed {
			task.CompletedAt = nil
		}
	}
	task.UpdatedAt = m.now().UTC()
	err = m.queries.WithTx(tx).UpdateTask(ctx, todosqlc.UpdateTaskParams{
		Title: task.Title, Description: task.Description, DueAt: toNullTime(task.DueAt),
		CompletedAt: toNullTime(task.CompletedAt), UpdatedAt: task.UpdatedAt.UnixMilli(), ID: id,
	})
	if err != nil {
		return Task{}, err
	}
	payload, _ := json.Marshal(map[string]string{"taskId": id})
	if _, err := m.events.PublishTx(ctx, tx, contracts.NewEvent{
		Topic: topic, SchemaVersion: 1, SourceModule: "todo", AggregateID: id, Payload: payload,
	}); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (m *Module) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tx, err := m.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_delete_failed", "could not delete task")
		return
	}
	defer tx.Rollback()
	rows, err := m.queries.WithTx(tx).DeleteTask(r.Context(), id)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_delete_failed", "could not delete task")
		return
	}
	if rows == 0 {
		httpapi.Error(w, http.StatusNotFound, "todo_not_found", "task not found")
		return
	}
	payload, _ := json.Marshal(map[string]string{"taskId": id})
	if _, err := m.events.PublishTx(r.Context(), tx, contracts.NewEvent{
		Topic: "todo.task.deleted", SchemaVersion: 1, SourceModule: "todo", AggregateID: id, Payload: payload,
	}); err != nil || tx.Commit() != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_delete_failed", "could not delete task")
		return
	}
	httpapi.Write(w, http.StatusNoContent, nil)
}

func (m *Module) summary(w http.ResponseWriter, r *http.Request) {
	result, err := m.queries.TodoSummary(r.Context(), sql.NullInt64{Int64: m.now().UTC().UnixMilli(), Valid: true})
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "todo_summary_failed", "could not load summary")
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]int64{"open": result.Open, "overdue": result.Overdue, "completed": result.Completed})
}

func (m *Module) scanDue(ctx context.Context, _ contracts.JobRun) error {
	rows, err := m.queries.ListDueTasks(ctx, sql.NullInt64{Int64: m.now().UTC().UnixMilli(), Valid: true})
	if err != nil {
		return err
	}
	for _, task := range rows {
		_, err := m.notifications.Create(ctx, contracts.NewNotification{
			SourceModule: "todo", Severity: contracts.NotificationWarning,
			Title: "待办已到期", Content: task.Title, ActionLabel: "查看待办", ActionRoute: "/todo",
			IdempotencyKey: fmt.Sprintf("todo:due:%s:%d", task.ID, task.DueAt.Int64),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) createTaskTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	var input struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		DueAt       string `json:"dueAt"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return contracts.ToolResult{}, err
	}
	request := CreateTask{Title: input.Title, Description: input.Description}
	if input.DueAt != "" {
		due, err := time.Parse(time.RFC3339, input.DueAt)
		if err != nil {
			return contracts.ToolResult{}, errors.New("dueAt must be RFC3339")
		}
		request.DueAt = &due
	}
	task, err := m.Create(ctx, request)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	result, _ := json.Marshal(task)
	return contracts.ToolResult{Content: result}, nil
}

func taskFromRow(row todosqlc.TodoTask) Task {
	return Task{
		ID: row.ID, Title: row.Title, Description: row.Description,
		DueAt: nullableTime(row.DueAt), CompletedAt: nullableTime(row.CompletedAt),
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
	}
}

func nullableTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}
	result := time.UnixMilli(value.Int64).UTC()
	return &result
}

func toNullTime(value *time.Time) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: value.UTC().UnixMilli(), Valid: true}
}
