package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"workbench/internal/contracts"
)

type Gateway struct {
	mu       sync.RWMutex
	provider contracts.AIProvider
	runtime  *ToolRuntime
}

func NewGateway(provider contracts.AIProvider, runtime *ToolRuntime) (*Gateway, error) {
	if runtime == nil {
		return nil, errors.New("tool runtime is required")
	}
	return &Gateway{provider: provider, runtime: runtime}, nil
}

func (g *Gateway) SetProvider(provider contracts.AIProvider) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.provider = provider
}

func (g *Gateway) snapshot() contracts.AIProvider {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.provider
}

func (g *Gateway) Available() bool { return g.snapshot() != nil }

func (g *Gateway) Generate(ctx context.Context, request contracts.GenerateRequest) (contracts.GenerateResult, error) {
	if _, err := contracts.RequireUserID(ctx); err != nil {
		return contracts.GenerateResult{}, err
	}
	provider := g.snapshot()
	if provider == nil {
		return contracts.GenerateResult{}, ErrUnavailable
	}
	var executions []contracts.ToolExecution
	var usage contracts.AIUsage
	for round := 0; round <= 4; round++ {
		request.Tools = g.roundTools(round)
		result, err := provider.Generate(ctx, request)
		if err != nil {
			return contracts.GenerateResult{}, err
		}
		usage.InputTokens += result.Usage.InputTokens
		usage.OutputTokens += result.Usage.OutputTokens
		if len(result.ToolCalls) == 0 {
			result.ToolExecutions = executions
			result.Usage = usage
			return result, nil
		}
		if round == 4 {
			break
		}
		if err := g.executeTools(ctx, &request, result, func(execution contracts.ToolExecution) {
			executions = append(executions, execution)
		}); err != nil {
			return contracts.GenerateResult{}, err
		}
	}
	return contracts.GenerateResult{}, errors.New("AI exceeded maximum tool-call rounds")
}

func (g *Gateway) Stream(ctx context.Context, request contracts.GenerateRequest) (contracts.AIStream, error) {
	if _, err := contracts.RequireUserID(ctx); err != nil {
		return nil, err
	}
	provider := g.snapshot()
	if provider == nil {
		return nil, ErrUnavailable
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream := &gatewayStream{events: make(chan contracts.AIStreamEvent, 32), cancel: cancel}
	go g.runStream(streamCtx, provider, request, stream.events)
	return stream, nil
}

func (g *Gateway) runStream(ctx context.Context, provider contracts.AIProvider, request contracts.GenerateRequest, events chan<- contracts.AIStreamEvent) {
	defer close(events)
	var executions []contracts.ToolExecution
	var usage contracts.AIUsage
	for round := 0; round <= 4; round++ {
		request.Tools = g.roundTools(round)
		upstream, err := provider.Stream(ctx, request)
		if err != nil {
			sendStreamError(ctx, events, err)
			return
		}
		if upstream == nil {
			sendStreamError(ctx, events, errors.New("AI provider returned a nil stream"))
			return
		}
		var completed *contracts.GenerateResult
		for event := range upstream.Events() {
			if event.Err != nil {
				_ = upstream.Close()
				sendStreamError(ctx, events, event.Err)
				return
			}
			if event.Text != "" {
				select {
				case events <- contracts.AIStreamEvent{Type: event.Type, Text: event.Text}:
				case <-ctx.Done():
					_ = upstream.Close()
					return
				}
			}
			if event.Done {
				completed = event.Result
			}
		}
		_ = upstream.Close()
		if completed == nil {
			sendStreamError(ctx, events, errors.New("AI stream ended without a result"))
			return
		}
		if len(completed.ToolCalls) == 0 {
			completed.ToolExecutions = executions
			usage.InputTokens += completed.Usage.InputTokens
			usage.OutputTokens += completed.Usage.OutputTokens
			completed.Usage = usage
			select {
			case events <- contracts.AIStreamEvent{Type: "response.completed", Result: completed, Done: true}:
			case <-ctx.Done():
			}
			return
		}

		usage.InputTokens += completed.Usage.InputTokens
		usage.OutputTokens += completed.Usage.OutputTokens
		if round == 4 {
			break
		}
		if err := g.executeTools(ctx, &request, *completed, func(execution contracts.ToolExecution) {
			executions = append(executions, execution)
			select {
			case events <- contracts.AIStreamEvent{Type: "tool.completed", Tool: &execution}:
			case <-ctx.Done():
			}
		}); err != nil {
			sendStreamError(ctx, events, err)
			return
		}
	}
	sendStreamError(ctx, events, errors.New("AI exceeded maximum tool-call rounds"))
}

func (g *Gateway) roundTools(round int) []contracts.AITool {
	if round == 4 {
		return nil
	}
	return g.runtime.AvailableTools()
}

func (g *Gateway) executeTools(ctx context.Context, request *contracts.GenerateRequest, result contracts.GenerateResult, report func(contracts.ToolExecution)) error {
	request.Items = append(request.Items, result.OutputItems...)
	for _, call := range result.ToolCalls {
		call.ConversationID, call.MessageID = request.ConversationID, request.MessageID
		// Model output is not a user confirmation, even if it names the user.
		call.ConfirmedBy = ""
		if call.IdempotencyKey == "" {
			call.IdempotencyKey = result.ResponseID + ":" + call.ID
		}
		execution := contracts.ToolExecution{CallID: call.ID, Name: call.Name, Label: call.Name, Risk: contracts.ToolRiskReadOnly, Status: "succeeded"}
		if tool, ok := g.runtime.tools[call.Name]; ok {
			execution.Label, execution.Risk = tool.definition.Description, tool.definition.Risk
		}
		toolResult, err := g.runtime.Execute(ctx, call)
		if err != nil {
			execution.Status, execution.Error = "failed", truncateToolError(err)
		}
		report(execution)
		if err != nil {
			return fmt.Errorf("execute tool %q: %w", call.Name, err)
		}
		output, _ := json.Marshal(struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		}{Type: "function_call_output", CallID: call.ID, Output: string(toolResult.Content)})
		request.Items = append(request.Items, output)
	}
	return nil
}

func sendStreamError(ctx context.Context, events chan<- contracts.AIStreamEvent, err error) {
	select {
	case events <- contracts.AIStreamEvent{Type: "error", Err: err, Done: true}:
	case <-ctx.Done():
	}
}

type gatewayStream struct {
	events chan contracts.AIStreamEvent
	cancel context.CancelFunc
	once   sync.Once
}

func (s *gatewayStream) Events() <-chan contracts.AIStreamEvent { return s.events }

func (s *gatewayStream) Close() error {
	s.once.Do(s.cancel)
	return nil
}

func TextMessage(role, content string) contracts.AIMessage {
	encoded, _ := json.Marshal(content)
	return contracts.AIMessage{Role: role, Content: encoded}
}
