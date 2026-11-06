package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"workbench/internal/contracts"
)

type completionProvider struct{ calls atomic.Int32 }

func (p *completionProvider) Generate(context.Context, contracts.GenerateRequest) (contracts.GenerateResult, error) {
	if p.calls.Add(1) > 1 {
		return contracts.GenerateResult{Text: "done"}, nil
	}
	return contracts.GenerateResult{
		ResponseID: "completion-response",
		ToolCalls: []contracts.ToolCall{
			{ID: "first", Name: "test.write", Arguments: json.RawMessage(`{"step":1}`)},
			{ID: "second", Name: "test.write", Arguments: json.RawMessage(`{"step":2}`)},
		},
	}, nil
}

func (p *completionProvider) Stream(ctx context.Context, request contracts.GenerateRequest) (contracts.AIStream, error) {
	result, err := p.Generate(ctx, request)
	if err != nil {
		return nil, err
	}
	events := make(chan contracts.AIStreamEvent, 1)
	events <- contracts.AIStreamEvent{Done: true, Result: &result}
	close(events)
	return staticStream{events: events}, nil
}

func TestToolCompletionCallbackStopsOnPersistenceFailure(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "generate", true: "stream"}[streaming], func(t *testing.T) {
			database := openAIDatabase(t)
			var writes atomic.Int32
			runtime, err := NewToolRuntime(database.SQL(), []contracts.AITool{continuationWriteTool(&writes)}, func(contracts.ModuleID) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			provider := &completionProvider{}
			gateway, err := NewGateway(provider, runtime)
			if err != nil {
				t.Fatal(err)
			}
			saveErr := errors.New("database unavailable while saving completed tool")
			var recorded atomic.Int32
			request := contracts.GenerateRequest{OnToolCompleted: func(_ context.Context, execution contracts.ToolExecution, result contracts.ToolResult) error {
				recorded.Add(1)
				if execution.CallID != "first" || execution.Status != "succeeded" || !strings.Contains(string(result.Content), `"written":true`) {
					t.Errorf("callback execution=%+v result=%s", execution, result.Content)
				}
				return saveErr
			}}
			if !streaming {
				_, err = gateway.Generate(aiUserContext("user"), request)
				if !errors.Is(err, saveErr) {
					t.Fatalf("generation error=%v, want persistence error", err)
				}
			} else {
				stream, err := gateway.Stream(aiUserContext("user"), request)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				var streamErr error
				for event := range stream.Events() {
					if event.Err != nil {
						streamErr = event.Err
					}
				}
				if !errors.Is(streamErr, saveErr) {
					t.Fatalf("stream error=%v, want persistence error", streamErr)
				}
			}
			if writes.Load() != 1 || recorded.Load() != 1 || provider.calls.Load() != 1 {
				t.Fatalf("writes/callback/provider calls=%d/%d/%d", writes.Load(), recorded.Load(), provider.calls.Load())
			}
		})
	}
}

func TestToolCompletionCallbackRunsAfterCancellationWithoutExecutingAnotherTool(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "generate", true: "stream"}[streaming], func(t *testing.T) {
			database := openAIDatabase(t)
			ctx, cancel := context.WithCancel(aiUserContext("user"))
			defer cancel()
			var writes, recorded atomic.Int32
			tool := continuationWriteTool(&writes)
			handler := tool.Handler
			tool.Handler = func(ctx context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
				result, err := handler(ctx, call)
				cancel()
				return result, err
			}
			runtime, err := NewToolRuntime(database.SQL(), []contracts.AITool{tool}, func(contracts.ModuleID) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			provider := &completionProvider{}
			gateway, err := NewGateway(provider, runtime)
			if err != nil {
				t.Fatal(err)
			}
			request := contracts.GenerateRequest{OnToolCompleted: func(callbackCtx context.Context, execution contracts.ToolExecution, result contracts.ToolResult) error {
				recorded.Add(1)
				if callbackCtx.Err() != context.Canceled || execution.Status != "succeeded" || !strings.Contains(string(result.Content), `"written":true`) {
					t.Errorf("canceled completion ctx=%v execution=%+v result=%s", callbackCtx.Err(), execution, result.Content)
				}
				return nil
			}}
			if streaming {
				stream, err := gateway.Stream(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				for range stream.Events() {
				}
			} else if _, err := gateway.Generate(ctx, request); !errors.Is(err, context.Canceled) {
				t.Fatalf("generation error=%v, want canceled", err)
			}
			if writes.Load() != 1 || recorded.Load() != 1 || provider.calls.Load() != 1 {
				t.Fatalf("writes/callback/provider calls=%d/%d/%d", writes.Load(), recorded.Load(), provider.calls.Load())
			}
		})
	}
}

func TestCanceledGenerationDoesNotStartProvider(t *testing.T) {
	database := openAIDatabase(t)
	runtime, err := NewToolRuntime(database.SQL(), nil, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	provider := &completionProvider{}
	gateway, err := NewGateway(provider, runtime)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(aiUserContext("user"))
	cancel()
	if _, err := gateway.Generate(ctx, contracts.GenerateRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("generation error=%v", err)
	}
	stream, err := gateway.Stream(ctx, contracts.GenerateRequest{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for range stream.Events() {
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("canceled request called provider %d times", provider.calls.Load())
	}
}
