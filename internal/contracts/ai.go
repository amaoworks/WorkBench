package contracts

import (
	"context"
	"encoding/json"
)

type ToolRisk string

const (
	ToolRiskReadOnly  ToolRisk = "read_only"
	ToolRiskLowWrite  ToolRisk = "low_write"
	ToolRiskHighWrite ToolRisk = "high_write"
)

type GenerateRequest struct {
	Profile            string
	Messages           []AIMessage
	Tools              []AITool
	PreviousResponseID string
	ToolOutputs        []ToolOutput
	ConversationID     string
	MessageID          string
}

type AIMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type GenerateResult struct {
	ResponseID string
	Text       string
	ToolCalls  []ToolCall
	Usage      AIUsage
}

type ToolOutput struct {
	CallID  string
	Content json.RawMessage
}

type AIUsage struct {
	InputTokens  int64
	OutputTokens int64
}

type AIStreamEvent struct {
	Type   string
	Text   string
	Result *GenerateResult
	Done   bool
	Err    error
}

type AIStream interface {
	Events() <-chan AIStreamEvent
	Close() error
}

type AIProvider interface {
	Generate(context.Context, GenerateRequest) (GenerateResult, error)
	Stream(context.Context, GenerateRequest) (AIStream, error)
}

type AITool struct {
	Name             string
	Module           ModuleID
	SchemaVersion    int
	Description      string
	ParametersSchema json.RawMessage
	Risk             ToolRisk
	Handler          ToolHandler
}

type ToolHandler func(context.Context, ToolCall) (ToolResult, error)

type ToolCall struct {
	ID             string
	Name           string
	Arguments      json.RawMessage
	IdempotencyKey string
	ConfirmedBy    string
	ConversationID string
	MessageID      string
}

type ToolResult struct {
	Content json.RawMessage
}
