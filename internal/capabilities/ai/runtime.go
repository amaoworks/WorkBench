package ai

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"workbench/internal/contracts"
	"workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/identity"
)

var ErrUnavailable = errors.New("AI provider is not configured")

type ToolRuntime struct {
	queries *dbsqlc.Queries
	tools   map[string]compiledTool
	enabled func(contracts.ModuleID) bool
	now     func() time.Time
}

type compiledTool struct {
	definition contracts.AITool
	schema     *jsonschema.Schema
}

func NewToolRuntime(db *sql.DB, tools []contracts.AITool, enabled func(contracts.ModuleID) bool) (*ToolRuntime, error) {
	if db == nil || enabled == nil {
		return nil, errors.New("database and enabled function are required")
	}
	runtime := &ToolRuntime{queries: dbsqlc.New(db), tools: make(map[string]compiledTool, len(tools)), enabled: enabled, now: time.Now}
	for _, tool := range tools {
		if tool.Name == "" || tool.Module == "" || tool.SchemaVersion < 1 || tool.Handler == nil {
			return nil, fmt.Errorf("invalid AI tool %q", tool.Name)
		}
		switch tool.Risk {
		case contracts.ToolRiskReadOnly, contracts.ToolRiskLowWrite, contracts.ToolRiskHighWrite:
		default:
			return nil, fmt.Errorf("AI tool %q has invalid risk %q", tool.Name, tool.Risk)
		}
		if _, exists := runtime.tools[tool.Name]; exists {
			return nil, fmt.Errorf("duplicate AI tool %q", tool.Name)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(tool.ParametersSchema))
		if err != nil {
			return nil, fmt.Errorf("parse AI tool %q schema: %w", tool.Name, err)
		}
		root, ok := document.(map[string]any)
		if !ok || root["type"] != "object" {
			return nil, fmt.Errorf("AI tool %q requires an object schema", tool.Name)
		}
		if err := validateStrictSchema(root, "$"); err != nil {
			return nil, fmt.Errorf("AI tool %q schema is not strict: %w", tool.Name, err)
		}
		compiler := jsonschema.NewCompiler()
		compiler.AssertFormat()
		resource := "urn:workbench:tool:" + tool.Name
		if err := compiler.AddResource(resource, document); err != nil {
			return nil, fmt.Errorf("load AI tool %q schema: %w", tool.Name, err)
		}
		schema, err := compiler.Compile(resource)
		if err != nil {
			return nil, fmt.Errorf("compile AI tool %q schema: %w", tool.Name, err)
		}
		runtime.tools[tool.Name] = compiledTool{definition: tool, schema: schema}
	}
	return runtime, nil
}

func validateStrictSchema(node map[string]any, path string) error {
	if schemaIncludesType(node["type"], "object") {
		if node["additionalProperties"] != false {
			return fmt.Errorf("%s requires additionalProperties=false", path)
		}
		properties, _ := node["properties"].(map[string]any)
		requiredValues, _ := node["required"].([]any)
		required := make(map[string]struct{}, len(requiredValues))
		for _, value := range requiredValues {
			name, ok := value.(string)
			if !ok {
				return fmt.Errorf("%s required entries must be strings", path)
			}
			required[name] = struct{}{}
		}
		for name := range properties {
			if _, ok := required[name]; !ok {
				return fmt.Errorf("%s property %q must be required (use a nullable type for optional values)", path, name)
			}
		}
	}
	if properties, ok := node["properties"].(map[string]any); ok {
		for name, raw := range properties {
			child, ok := raw.(map[string]any)
			if ok {
				if err := validateStrictSchema(child, path+"."+name); err != nil {
					return err
				}
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		if err := validateStrictSchema(items, path+"[]"); err != nil {
			return err
		}
	}
	for _, keyword := range []string{"anyOf", "oneOf", "allOf"} {
		branches, _ := node[keyword].([]any)
		for index, raw := range branches {
			branch, ok := raw.(map[string]any)
			if ok {
				if err := validateStrictSchema(branch, fmt.Sprintf("%s.%s[%d]", path, keyword, index)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func schemaIncludesType(value any, expected string) bool {
	if value == expected {
		return true
	}
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func (r *ToolRuntime) AvailableTools() []contracts.AITool {
	result := make([]contracts.AITool, 0, len(r.tools))
	for _, tool := range r.tools {
		if r.enabled(tool.definition.Module) {
			result = append(result, tool.definition)
		}
	}
	slices.SortFunc(result, func(a, b contracts.AITool) int { return strings.Compare(a.Name, b.Name) })
	return result
}

func (r *ToolRuntime) Execute(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
	tool, exists := r.tools[call.Name]
	if !exists {
		return contracts.ToolResult{}, fmt.Errorf("unknown AI tool %q", call.Name)
	}
	if !r.enabled(tool.definition.Module) {
		return contracts.ToolResult{}, fmt.Errorf("AI tool %q belongs to a disabled module", call.Name)
	}
	if tool.definition.Risk != contracts.ToolRiskReadOnly && call.IdempotencyKey == "" {
		return contracts.ToolResult{}, errors.New("write tool requires an idempotency key")
	}
	if tool.definition.Risk == contracts.ToolRiskHighWrite && call.ConfirmedBy == "" {
		return contracts.ToolResult{}, errors.New("high-risk tool requires explicit confirmation")
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(call.Arguments))
	if err != nil {
		return contracts.ToolResult{}, fmt.Errorf("tool arguments are invalid JSON: %w", err)
	}
	if err := tool.schema.Validate(instance); err != nil {
		return contracts.ToolResult{}, fmt.Errorf("tool arguments failed schema validation: %w", err)
	}

	auditID, err := identity.New()
	if err != nil {
		return contracts.ToolResult{}, err
	}
	redacted := redact(call.Arguments)
	rows, err := r.queries.InsertAIToolCall(ctx, dbsqlc.InsertAIToolCallParams{
		ID: auditID, ConversationID: call.ConversationID, MessageID: call.MessageID,
		ToolName: call.Name, Module: string(tool.definition.Module), Risk: string(tool.definition.Risk),
		ArgumentsJson: redacted, IdempotencyKey: call.IdempotencyKey,
		ConfirmationAt: confirmationTime(call), CreatedAt: r.now().UTC().UnixMilli(),
	})
	if err != nil {
		return contracts.ToolResult{}, fmt.Errorf("start tool audit: %w", err)
	}
	if rows == 0 {
		return r.existingResult(ctx, call.Name, call.IdempotencyKey)
	}

	toolResult, handlerErr := tool.definition.Handler(ctx, call)
	status := "succeeded"
	errorMessage := ""
	var resultJSON sql.NullString
	if handlerErr != nil {
		status = "failed"
		errorMessage = truncateToolError(handlerErr)
	} else {
		if len(toolResult.Content) == 0 {
			toolResult.Content = json.RawMessage(`null`)
		}
		if !json.Valid(toolResult.Content) {
			handlerErr = errors.New("tool returned invalid JSON")
			status = "failed"
			errorMessage = handlerErr.Error()
		} else {
			resultJSON = sql.NullString{String: string(toolResult.Content), Valid: true}
		}
	}
	auditErr := r.queries.FinishAIToolCall(context.WithoutCancel(ctx), dbsqlc.FinishAIToolCallParams{
		Status: status, ResultJson: resultJSON, Error: errorMessage,
		CompletedAt: sql.NullInt64{Int64: r.now().UTC().UnixMilli(), Valid: true}, ID: auditID,
	})
	if auditErr != nil {
		return contracts.ToolResult{}, fmt.Errorf("finish tool audit: %w", auditErr)
	}
	return toolResult, handlerErr
}

func (r *ToolRuntime) existingResult(ctx context.Context, name, key string) (contracts.ToolResult, error) {
	row, err := r.queries.GetAIToolCallResult(ctx, dbsqlc.GetAIToolCallResultParams{
		ToolName: name, IdempotencyKey: sql.NullString{String: key, Valid: true},
	})
	if err != nil {
		return contracts.ToolResult{}, err
	}
	if row.Status == "succeeded" && row.ResultJson.Valid {
		return contracts.ToolResult{Content: json.RawMessage(row.ResultJson.String)}, nil
	}
	if row.Error.Valid {
		return contracts.ToolResult{}, fmt.Errorf("previous tool call failed: %s", row.Error.String)
	}
	return contracts.ToolResult{}, errors.New("tool call with this idempotency key is already in progress")
}

func confirmationTime(call contracts.ToolCall) sql.NullInt64 {
	if call.ConfirmedBy == "" {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: time.Now().UTC().UnixMilli(), Valid: true}
}

func redact(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return `{}`
	}
	redactValue(value)
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func redactValue(value any) {
	object, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, nested := range object {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") {
			object[key] = "[REDACTED]"
			continue
		}
		redactValue(nested)
	}
}

func truncateToolError(err error) string {
	message := err.Error()
	if len(message) > 2000 {
		return message[:2000]
	}
	return message
}
