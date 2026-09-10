package todo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"workbench/internal/contracts"
)

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
