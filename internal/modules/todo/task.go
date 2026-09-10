package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type CreateTask struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	DueAt       *time.Time `json:"dueAt"`
}

type UpdateTask struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	DueAt       *time.Time `json:"dueAt"`
	ClearDueAt  bool       `json:"clearDueAt"`
	Completed   *bool      `json:"completed"`
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
