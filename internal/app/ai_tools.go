package app

import (
	"context"
	"encoding/json"

	"workbench/internal/contracts"
)

func notificationAITools(service contracts.NotificationService) []contracts.AITool {
	return []contracts.AITool{
		{
			Name: "notifications.list", Module: "core", SchemaVersion: 1,
			Description:      "查询工作台通知，可只返回未读通知",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"unreadOnly":{"type":"boolean"},"limit":{"type":"integer","minimum":1,"maximum":100},"cursor":{"type":["string","null"]}},"required":["unreadOnly","limit","cursor"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskReadOnly,
			Handler: func(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
				var input struct {
					UnreadOnly bool   `json:"unreadOnly"`
					Limit      int    `json:"limit"`
					Cursor     string `json:"cursor"`
				}
				if err := json.Unmarshal(call.Arguments, &input); err != nil {
					return contracts.ToolResult{}, err
				}
				page, err := service.List(ctx, contracts.NotificationQuery{
					UnreadOnly: input.UnreadOnly,
					Limit:      input.Limit,
					Cursor:     input.Cursor,
				})
				if err != nil {
					return contracts.ToolResult{}, err
				}
				content, err := json.Marshal(page)
				return contracts.ToolResult{Content: content}, err
			},
		},
		{
			Name: "notifications.mark_read", Module: "core", SchemaVersion: 1,
			Description:      "按通知 ID 标记通知为已读",
			ParametersSchema: json.RawMessage(`{"type":"object","properties":{"ids":{"type":"array","items":{"type":"string","pattern":"^[a-f0-9]{32}$"},"minItems":1,"maxItems":100,"uniqueItems":true}},"required":["ids"],"additionalProperties":false}`),
			Risk:             contracts.ToolRiskLowWrite,
			Handler: func(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
				var input struct {
					IDs []string `json:"ids"`
				}
				if err := json.Unmarshal(call.Arguments, &input); err != nil {
					return contracts.ToolResult{}, err
				}
				if err := service.MarkRead(ctx, input.IDs); err != nil {
					return contracts.ToolResult{}, err
				}
				content, err := json.Marshal(map[string][]string{"markedRead": input.IDs})
				return contracts.ToolResult{Content: content}, err
			},
		},
	}
}
