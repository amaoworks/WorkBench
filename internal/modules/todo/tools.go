package todo

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"

	"workbench/internal/contracts"
	todosqlc "workbench/internal/modules/todo/sqlc"
)

func (m *Module) createTaskTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	var input struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		DueAt       string  `json:"dueAt"`
		Priority    *string `json:"priority"`
		Category    *string `json:"category"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return contracts.ToolResult{}, err
	}
	request := CreateTask{Title: input.Title, Description: input.Description}
	if input.Priority != nil {
		request.Priority = *input.Priority
	}
	if input.Category != nil {
		request.Category = *input.Category
	}
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

func (m *Module) listTasksTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	var input struct {
		View     string `json:"view"`
		TimeZone string `json:"timeZone"`
		Limit    int64  `json:"limit"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return contracts.ToolResult{}, err
	}
	view, ok := taskViews[input.View]
	if !ok || input.Limit < 1 || input.Limit > 100 {
		return contracts.ToolResult{}, errors.New("invalid list parameters")
	}
	now := m.now().UTC()
	endToday, endWeek, err := taskTimeBoundaries(now, input.TimeZone)
	if err != nil {
		return contracts.ToolResult{}, err
	}
	rows, err := m.queries.ListTasksPage(ctx, todosqlc.ListTasksPageParams{
		View: view, NowMs: now.UnixMilli(), EndToday: endToday.UnixMilli(), EndWeek: endWeek.UnixMilli(),
		Due: math.MaxInt64, PageLimit: input.Limit,
	})
	if err != nil {
		return contracts.ToolResult{}, err
	}
	items := make([]Task, 0, len(rows))
	for _, row := range rows {
		items = append(items, taskFromRow(row))
	}
	result, _ := json.Marshal(struct {
		Items []Task `json:"items"`
	}{Items: items})
	return contracts.ToolResult{Content: result}, nil
}

func (m *Module) completeTaskTool(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	var input struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return contracts.ToolResult{}, err
	}
	if !cursorID.MatchString(input.TaskID) {
		return contracts.ToolResult{}, errors.New("invalid taskId")
	}
	row, err := m.queries.GetTask(ctx, input.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return contracts.ToolResult{}, errors.New("task not found")
		}
		return contracts.ToolResult{}, err
	}
	task := taskFromRow(row)
	if task.CompletedAt == nil {
		completed := true
		task, err = m.update(ctx, input.TaskID, UpdateTask{Completed: &completed})
		if err != nil {
			return contracts.ToolResult{}, err
		}
	}
	result, _ := json.Marshal(task)
	return contracts.ToolResult{Content: result}, nil
}
