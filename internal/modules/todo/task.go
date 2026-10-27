package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"workbench/internal/contracts"
	"workbench/internal/foundation/identity"
	"workbench/internal/modules/todo/sqlc"
)

type Task struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	DueAt       *time.Time `json:"dueAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Priority    string     `json:"priority"`
	Category    string     `json:"category,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type CreateTask struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	DueAt       *time.Time `json:"dueAt"`
	Priority    string     `json:"priority"`
	Category    string     `json:"category"`
}

type UpdateTask struct {
	Title         *string    `json:"title"`
	Description   *string    `json:"description"`
	DueAt         *time.Time `json:"dueAt"`
	ClearDueAt    bool       `json:"clearDueAt"`
	Completed     *bool      `json:"completed"`
	Priority      *string    `json:"priority"`
	Category      *string    `json:"category"`
	ClearCategory bool       `json:"clearCategory"`
}

func (m *Module) Create(ctx context.Context, input CreateTask) (Task, error) {
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
	task, err := m.createTaskTx(ctx, tx, id, input, now)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func validateTaskContent(title, description string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 300 {
		return "", errors.New("title must contain 1 to 300 bytes")
	}
	if len(description) > 10_000 {
		return "", errors.New("description exceeds 10000 bytes")
	}
	return title, nil
}

func normalizeTaskMetadata(priority, category string) (string, string, error) {
	priority = strings.TrimSpace(priority)
	if priority == "" {
		priority = "none"
	}
	switch priority {
	case "none", "low", "medium", "high":
	default:
		return "", "", errors.New("priority must be none, low, medium, or high")
	}
	category = strings.TrimSpace(category)
	if utf8.RuneCountInString(category) > 40 {
		return "", "", errors.New("category exceeds 40 characters")
	}
	return priority, category, nil
}

func (m *Module) createTaskTx(ctx context.Context, tx *sql.Tx, id string, input CreateTask, now time.Time) (Task, error) {
	title, err := validateTaskContent(input.Title, input.Description)
	if err != nil {
		return Task{}, err
	}
	priority, category, err := normalizeTaskMetadata(input.Priority, input.Category)
	if err != nil {
		return Task{}, err
	}
	err = m.queries.WithTx(tx).CreateTask(ctx, todosqlc.CreateTaskParams{
		ID: id, Title: title, Description: input.Description,
		DueAt: toNullTime(input.DueAt), Priority: priority, Category: toNullString(category),
		CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	})
	if err != nil {
		return Task{}, fmt.Errorf("insert task: %w", err)
	}
	task := Task{ID: id, Title: title, Description: input.Description, DueAt: input.DueAt, Priority: priority, Category: category, CreatedAt: now, UpdatedAt: now}
	if err := m.publishTaskTx(ctx, tx, task, "todo.task.created"); err != nil {
		return Task{}, err
	}
	return task, nil
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
		task.Title = *input.Title
	}
	if input.Description != nil {
		task.Description = *input.Description
	}
	if input.ClearDueAt {
		task.DueAt = nil
	} else if input.DueAt != nil {
		due := input.DueAt.UTC()
		task.DueAt = &due
	}
	if input.Priority != nil {
		task.Priority = *input.Priority
	}
	if input.ClearCategory {
		task.Category = ""
	} else if input.Category != nil {
		task.Category = *input.Category
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
	task, err = m.updateTaskTx(ctx, tx, task, topic)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (m *Module) updateTaskTx(ctx context.Context, tx *sql.Tx, task Task, topic string) (Task, error) {
	title, err := validateTaskContent(task.Title, task.Description)
	if err != nil {
		return Task{}, err
	}
	task.Title = title
	task.Priority, task.Category, err = normalizeTaskMetadata(task.Priority, task.Category)
	if err != nil {
		return Task{}, err
	}
	err = m.queries.WithTx(tx).UpdateTask(ctx, todosqlc.UpdateTaskParams{
		Title: task.Title, Description: task.Description, DueAt: toNullTime(task.DueAt),
		CompletedAt: toNullTime(task.CompletedAt), Priority: task.Priority, Category: toNullString(task.Category),
		UpdatedAt: task.UpdatedAt.UnixMilli(), ID: task.ID,
	})
	if err != nil {
		return Task{}, err
	}
	if err := m.publishTaskTx(ctx, tx, task, topic); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (m *Module) publishTaskTx(ctx context.Context, tx *sql.Tx, task Task, topic string) error {
	data := map[string]string{"taskId": task.ID}
	if topic == "todo.task.created" {
		data["title"] = task.Title
	}
	payload, _ := json.Marshal(data)
	_, err := m.events.PublishTx(ctx, tx, contracts.NewEvent{
		Topic: topic, SchemaVersion: 1, SourceModule: "todo", AggregateID: task.ID, Payload: payload,
	})
	return err
}

func taskFromRow(row todosqlc.TodoTask) Task {
	return Task{
		ID: row.ID, Title: row.Title, Description: row.Description,
		DueAt: nullableTime(row.DueAt), CompletedAt: nullableTime(row.CompletedAt),
		Priority: row.Priority, Category: nullableString(row.Category),
		CreatedAt: time.UnixMilli(row.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(row.UpdatedAt).UTC(),
	}
}

func nullableString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func toNullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
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
