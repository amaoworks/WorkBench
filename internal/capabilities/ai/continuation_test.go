package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"workbench/internal/contracts"
)

func TestOpenAIContinuationPreservesOrderedItemsAcrossToolRounds(t *testing.T) {
	database := openAIDatabase(t)
	var handlerCalls atomic.Int32
	tool := continuationWriteTool(&handlerCalls)
	runtime, err := NewToolRuntime(database.SQL(), []contracts.AITool{tool}, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	requests := []map[string]any{}
	responses := []string{
		continuationToolResponse("resp_1", "reasoning_1", "encrypted-one", "fc_1", "call_1", 1, 10, 1),
		continuationToolResponse("resp_2", "reasoning_2", "encrypted-two", "fc_2", "call_2", 2, 20, 2),
		continuationTextResponse("resp_3", "finished", 30, 3),
	}
	client := &http.Client{Transport: continuationRoundTrip(func(request *http.Request) (*http.Response, error) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		mu.Lock()
		index := len(requests)
		requests = append(requests, body)
		mu.Unlock()
		if index >= len(responses) {
			return nil, errors.New("unexpected continuation request")
		}
		return continuationHTTPResponse(request, http.StatusOK, "application/json", responses[index]), nil
	})}
	provider, err := NewOpenAIProvider(OpenAIConfig{
		APIKey: "test-key", BaseURL: "https://api.test/v1", HTTPClient: client,
		Profiles: map[string]Profile{"default": {Model: "test-model"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewGateway(provider, runtime)
	if err != nil {
		t.Fatal(err)
	}
	result, err := gateway.Generate(aiUserContext("user"), contracts.GenerateRequest{
		Profile: "default",
		Messages: []contracts.AIMessage{
			TextMessage("system", "keep system context"),
			TextMessage("user", "run both writes"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "finished" || handlerCalls.Load() != 2 {
		t.Fatalf("result=%+v handler calls=%d", result, handlerCalls.Load())
	}
	if result.Usage != (contracts.AIUsage{InputTokens: 60, OutputTokens: 6}) {
		t.Fatalf("usage=%+v", result.Usage)
	}
	if len(result.ToolExecutions) != 2 || result.ToolExecutions[0].CallID != "call_1" || result.ToolExecutions[1].CallID != "call_2" {
		t.Fatalf("tool executions=%+v", result.ToolExecutions)
	}

	mu.Lock()
	captured := append([]map[string]any(nil), requests...)
	mu.Unlock()
	if len(captured) != 3 {
		t.Fatalf("request count=%d", len(captured))
	}
	for index, request := range captured {
		if stored, ok := request["store"].(bool); !ok || stored {
			t.Fatalf("request %d store=%#v", index+1, request["store"])
		}
		if _, exists := request["previous_response_id"]; exists {
			t.Fatalf("request %d contains previous_response_id", index+1)
		}
		include, _ := request["include"].([]any)
		if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
			t.Fatalf("request %d include=%#v", index+1, request["include"])
		}
		input := continuationInput(t, request)
		assertContinuationMessage(t, input, 0, "system", "keep system context")
		assertContinuationMessage(t, input, 1, "user", "run both writes")
	}
	assertContinuationTypes(t, continuationInput(t, captured[0]), []string{"", ""})
	assertContinuationTypes(t, continuationInput(t, captured[1]), []string{"", "", "reasoning", "function_call", "function_call_output"})
	assertContinuationTypes(t, continuationInput(t, captured[2]), []string{"", "", "reasoning", "function_call", "function_call_output", "reasoning", "function_call", "function_call_output"})
	assertContinuationItem(t, continuationInput(t, captured[1]), 2, "encrypted_content", "encrypted-one")
	assertContinuationItem(t, continuationInput(t, captured[1]), 3, "call_id", "call_1")
	assertContinuationItem(t, continuationInput(t, captured[1]), 4, "call_id", "call_1")
	assertContinuationItem(t, continuationInput(t, captured[2]), 5, "encrypted_content", "encrypted-two")
	assertContinuationItem(t, continuationInput(t, captured[2]), 6, "call_id", "call_2")
	assertContinuationItem(t, continuationInput(t, captured[2]), 7, "call_id", "call_2")
}

func TestOpenAIStreamReportsSuccessfulWriteBeforeLaterUpstreamFailure(t *testing.T) {
	database := openAIDatabase(t)
	var handlerCalls atomic.Int32
	runtime, err := NewToolRuntime(database.SQL(), []contracts.AITool{continuationWriteTool(&handlerCalls)}, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	client := &http.Client{Transport: continuationRoundTrip(func(request *http.Request) (*http.Response, error) {
		if requests.Add(1) == 1 {
			body := "event: response.completed\n" +
				"data: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":" + continuationToolResponse("resp_stream_1", "reasoning_stream_1", "encrypted-stream", "fc_stream_1", "call_stream_1", 1, 4, 1) + "}\n\n" +
				"data: [DONE]\n\n"
			return continuationHTTPResponse(request, http.StatusOK, "text/event-stream", body), nil
		}
		return continuationHTTPResponse(request, http.StatusBadRequest, "application/json", `{"error":{"message":"later upstream failed","type":"invalid_request_error"}}`), nil
	})}
	provider, err := NewOpenAIProvider(OpenAIConfig{
		APIKey: "test-key", BaseURL: "https://api.test/v1", HTTPClient: client,
		Profiles: map[string]Profile{"default": {Model: "test-model"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := NewGateway(provider, runtime)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := gateway.Stream(aiUserContext("user"), contracts.GenerateRequest{
		Profile: "default", Messages: []contracts.AIMessage{TextMessage("user", "write once")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var events []contracts.AIStreamEvent
	for event := range stream.Events() {
		events = append(events, event)
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("handler calls=%d", handlerCalls.Load())
	}
	toolIndex, errorIndex := -1, -1
	for index, event := range events {
		if event.Tool != nil && event.Tool.Status == "succeeded" {
			toolIndex = index
			if event.Tool.CallID != "call_stream_1" || event.Tool.Risk != contracts.ToolRiskLowWrite {
				t.Fatalf("tool event=%+v", event.Tool)
			}
		}
		if event.Err != nil {
			errorIndex = index
			if !strings.Contains(event.Err.Error(), "later upstream failed") {
				t.Fatalf("stream error=%v", event.Err)
			}
		}
	}
	if toolIndex < 0 || errorIndex <= toolIndex {
		t.Fatalf("event order tool=%d error=%d events=%+v", toolIndex, errorIndex, events)
	}
}

func continuationWriteTool(calls *atomic.Int32) contracts.AITool {
	return contracts.AITool{
		Name: "test.write", Module: "test", SchemaVersion: 1, Description: "Test write", Risk: contracts.ToolRiskLowWrite,
		ParametersSchema: json.RawMessage(`{"type":"object","properties":{"step":{"type":"integer"}},"required":["step"],"additionalProperties":false}`),
		Handler: func(_ context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
			calls.Add(1)
			return contracts.ToolResult{Content: json.RawMessage(`{"written":true,"arguments":` + string(call.Arguments) + `}`)}, nil
		},
	}
}

func continuationToolResponse(responseID, reasoningID, encrypted, functionID, callID string, step, inputTokens, outputTokens int) string {
	body, _ := json.Marshal(map[string]any{
		"id": responseID, "object": "response", "created_at": 1, "status": "completed", "model": "test-model",
		"output": []any{
			map[string]any{"id": reasoningID, "type": "reasoning", "encrypted_content": encrypted, "summary": []any{}},
			map[string]any{"id": functionID, "type": "function_call", "status": "completed", "call_id": callID, "name": "test.write", "arguments": `{"step":` + strconv.Itoa(step) + `}`},
		},
		"usage": continuationUsage(inputTokens, outputTokens),
	})
	return string(body)
}

func continuationTextResponse(responseID, text string, inputTokens, outputTokens int) string {
	body, _ := json.Marshal(map[string]any{
		"id": responseID, "object": "response", "created_at": 1, "status": "completed", "model": "test-model",
		"output": []any{map[string]any{
			"id": "message_final", "type": "message", "status": "completed", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
		}},
		"usage": continuationUsage(inputTokens, outputTokens),
	})
	return string(body)
}

func continuationUsage(inputTokens, outputTokens int) map[string]any {
	return map[string]any{
		"input_tokens": inputTokens, "input_tokens_details": map[string]any{"cached_tokens": 0},
		"output_tokens": outputTokens, "output_tokens_details": map[string]any{"reasoning_tokens": 0},
		"total_tokens": inputTokens + outputTokens,
	}
}

func continuationHTTPResponse(request *http.Request, status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Header: http.Header{"Content-Type": []string{contentType}},
		Body: io.NopCloser(strings.NewReader(body)), Request: request,
	}
}

type continuationRoundTrip func(*http.Request) (*http.Response, error)

func (f continuationRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func continuationInput(t *testing.T, request map[string]any) []any {
	t.Helper()
	input, ok := request["input"].([]any)
	if !ok {
		t.Fatalf("input=%#v", request["input"])
	}
	return input
}

func assertContinuationTypes(t *testing.T, input []any, want []string) {
	t.Helper()
	if len(input) != len(want) {
		t.Fatalf("input length=%d want=%d input=%#v", len(input), len(want), input)
	}
	for index, expected := range want {
		item, ok := input[index].(map[string]any)
		actual, _ := item["type"].(string)
		if !ok || actual != expected {
			t.Fatalf("input[%d]=%#v want type %q", index, input[index], expected)
		}
	}
}

func assertContinuationMessage(t *testing.T, input []any, index int, role, content string) {
	t.Helper()
	item, ok := input[index].(map[string]any)
	if !ok || item["role"] != role || item["content"] != content {
		t.Fatalf("input[%d]=%#v want %s message %q", index, input[index], role, content)
	}
}

func assertContinuationItem(t *testing.T, input []any, index int, key, value string) {
	t.Helper()
	item, ok := input[index].(map[string]any)
	if !ok || item[key] != value {
		t.Fatalf("input[%d].%s=%#v want %q", index, key, item[key], value)
	}
}
