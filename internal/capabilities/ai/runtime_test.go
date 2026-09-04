package ai

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

func openAIDatabase(t *testing.T) *workbenchdb.Database {
	t.Helper()
	database, err := workbenchdb.Open(context.Background(), workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestToolRuntimeValidatesAndDeduplicatesWrites(t *testing.T) {
	database := openAIDatabase(t)
	var calls atomic.Int32
	tool := contracts.AITool{
		Name: "todo.create_task", Module: "todo", SchemaVersion: 1, Risk: contracts.ToolRiskLowWrite,
		ParametersSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
		Handler: func(_ context.Context, _ contracts.ToolCall) (contracts.ToolResult, error) {
			calls.Add(1)
			return contracts.ToolResult{Content: json.RawMessage(`{"id":"one"}`)}, nil
		},
	}
	runtime, err := NewToolRuntime(database.SQL(), []contracts.AITool{tool}, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	call := contracts.ToolCall{
		ID: "call_1", Name: tool.Name, Arguments: json.RawMessage(`{"title":"Test"}`), IdempotencyKey: "response:call_1",
	}
	first, err := runtime.Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.Execute(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Content) != string(second.Content) || calls.Load() != 1 {
		t.Fatalf("results = %s/%s, handler calls = %d", first.Content, second.Content, calls.Load())
	}
	call.IdempotencyKey = "response:call_2"
	call.Arguments = json.RawMessage(`{"unexpected":true}`)
	if _, err := runtime.Execute(context.Background(), call); err == nil {
		t.Fatal("runtime accepted arguments outside strict schema")
	}
}

func TestToolRuntimeHonorsModuleGateAndConfirmation(t *testing.T) {
	database := openAIDatabase(t)
	enabled := false
	tool := contracts.AITool{
		Name: "investment.place_order", Module: "investment", SchemaVersion: 1, Risk: contracts.ToolRiskHighWrite,
		ParametersSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Handler: func(context.Context, contracts.ToolCall) (contracts.ToolResult, error) {
			return contracts.ToolResult{Content: json.RawMessage(`{}`)}, nil
		},
	}
	runtime, err := NewToolRuntime(database.SQL(), []contracts.AITool{tool}, func(contracts.ModuleID) bool { return enabled })
	if err != nil {
		t.Fatal(err)
	}
	call := contracts.ToolCall{Name: tool.Name, Arguments: json.RawMessage(`{}`), IdempotencyKey: "one"}
	if _, err := runtime.Execute(context.Background(), call); err == nil {
		t.Fatal("disabled module tool executed")
	}
	enabled = true
	if _, err := runtime.Execute(context.Background(), call); err == nil {
		t.Fatal("high-risk tool executed without confirmation")
	}
	call.ConfirmedBy = "user"
	if _, err := runtime.Execute(context.Background(), call); err != nil {
		t.Fatalf("confirmed tool failed: %v", err)
	}
}

func TestToolRuntimeRejectsNonStrictSchemaAndUnknownRisk(t *testing.T) {
	database := openAIDatabase(t)
	handler := func(context.Context, contracts.ToolCall) (contracts.ToolResult, error) {
		return contracts.ToolResult{Content: json.RawMessage(`{}`)}, nil
	}
	_, err := NewToolRuntime(database.SQL(), []contracts.AITool{{
		Name: "todo.loose", Module: "todo", SchemaVersion: 1, Risk: contracts.ToolRiskReadOnly,
		ParametersSchema: json.RawMessage(`{"type":"object"}`), Handler: handler,
	}}, func(contracts.ModuleID) bool { return true })
	if err == nil {
		t.Fatal("runtime accepted a schema without additionalProperties=false")
	}
	_, err = NewToolRuntime(database.SQL(), []contracts.AITool{{
		Name: "todo.risky", Module: "todo", SchemaVersion: 1, Risk: contracts.ToolRisk("unknown"),
		ParametersSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), Handler: handler,
	}}, func(contracts.ModuleID) bool { return true })
	if err == nil {
		t.Fatal("runtime accepted an unknown risk level")
	}
}

func TestToolAuditRedactsNestedSecrets(t *testing.T) {
	redacted := redact(json.RawMessage(`{"password":"visible","nested":{"apiToken":"also-visible"},"title":"safe"}`))
	if strings.Contains(redacted, "visible") || strings.Contains(redacted, "also-visible") {
		t.Fatalf("redacted audit still contains a secret: %s", redacted)
	}
	if !strings.Contains(redacted, `"title":"safe"`) || strings.Count(redacted, "[REDACTED]") != 2 {
		t.Fatalf("unexpected redacted audit: %s", redacted)
	}
}

type fakeProvider struct {
	calls int
}

func (p *fakeProvider) Generate(_ context.Context, request contracts.GenerateRequest) (contracts.GenerateResult, error) {
	p.calls++
	if p.calls == 1 {
		return contracts.GenerateResult{
			ResponseID: "response_1",
			ToolCalls:  []contracts.ToolCall{{ID: "call_1", Name: "todo.create_task", Arguments: json.RawMessage(`{"title":"From AI"}`)}},
		}, nil
	}
	if request.PreviousResponseID != "response_1" || len(request.ToolOutputs) != 1 {
		return contracts.GenerateResult{}, context.Canceled
	}
	return contracts.GenerateResult{ResponseID: "response_2", Text: "Created"}, nil
}

func (p *fakeProvider) Stream(context.Context, contracts.GenerateRequest) (contracts.AIStream, error) {
	return nil, nil
}

func TestGatewayExecutesToolLoop(t *testing.T) {
	database := openAIDatabase(t)
	runtime, err := NewToolRuntime(database.SQL(), []contracts.AITool{{
		Name: "todo.create_task", Module: "todo", SchemaVersion: 1, Risk: contracts.ToolRiskLowWrite,
		ParametersSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
		Handler: func(context.Context, contracts.ToolCall) (contracts.ToolResult, error) {
			return contracts.ToolResult{Content: json.RawMessage(`{"id":"one"}`)}, nil
		},
	}}, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeProvider{}
	gateway, err := NewGateway(provider, runtime)
	if err != nil {
		t.Fatal(err)
	}
	result, err := gateway.Generate(context.Background(), contracts.GenerateRequest{
		Profile: "default", Messages: []contracts.AIMessage{TextMessage("user", "create a task")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Created" || provider.calls != 2 {
		t.Fatalf("result = %+v, provider calls = %d", result, provider.calls)
	}
}

type scriptedProvider struct {
	mu      sync.Mutex
	results []contracts.GenerateResult
	calls   int
}

func (p *scriptedProvider) Generate(context.Context, contracts.GenerateRequest) (contracts.GenerateResult, error) {
	return contracts.GenerateResult{}, errors.New("unexpected Generate call")
}

func (p *scriptedProvider) Stream(context.Context, contracts.GenerateRequest) (contracts.AIStream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.calls >= len(p.results) {
		return nil, errors.New("unexpected Stream call")
	}
	result := p.results[p.calls]
	p.calls++
	events := make(chan contracts.AIStreamEvent, 2)
	if result.Text != "" {
		events <- contracts.AIStreamEvent{Type: "response.output_text.delta", Text: result.Text}
	}
	events <- contracts.AIStreamEvent{Type: "response.completed", Result: &result, Done: true}
	close(events)
	return staticStream{events: events}, nil
}

type staticStream struct {
	events <-chan contracts.AIStreamEvent
}

func (s staticStream) Events() <-chan contracts.AIStreamEvent { return s.events }
func (staticStream) Close() error                             { return nil }

func TestGatewayStreamsAcrossToolCallRounds(t *testing.T) {
	database := openAIDatabase(t)
	var toolCalls atomic.Int32
	runtime, err := NewToolRuntime(database.SQL(), []contracts.AITool{{
		Name: "todo.create_task", Module: "todo", SchemaVersion: 1, Risk: contracts.ToolRiskLowWrite,
		ParametersSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"],"additionalProperties":false}`),
		Handler: func(context.Context, contracts.ToolCall) (contracts.ToolResult, error) {
			toolCalls.Add(1)
			return contracts.ToolResult{Content: json.RawMessage(`{"id":"one"}`)}, nil
		},
	}}, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{results: []contracts.GenerateResult{
		{ResponseID: "response_1", ToolCalls: []contracts.ToolCall{{ID: "call_1", Name: "todo.create_task", Arguments: json.RawMessage(`{"title":"From AI"}`)}}},
		{ResponseID: "response_2", Text: "Created"},
	}}
	gateway, err := NewGateway(provider, runtime)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := gateway.Stream(context.Background(), contracts.GenerateRequest{Profile: "default"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var text string
	var completed *contracts.GenerateResult
	for event := range stream.Events() {
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		text += event.Text
		if event.Done {
			completed = event.Result
		}
	}
	if text != "Created" || completed == nil || completed.ResponseID != "response_2" {
		t.Fatalf("stream text/result = %q/%+v", text, completed)
	}
	if provider.calls != 2 || toolCalls.Load() != 1 {
		t.Fatalf("provider/tool calls = %d/%d, want 2/1", provider.calls, toolCalls.Load())
	}
}
