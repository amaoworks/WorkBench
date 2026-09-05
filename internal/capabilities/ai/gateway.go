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
	provider := g.snapshot()
	if provider == nil {
		return contracts.GenerateResult{}, ErrUnavailable
	}
	request.Tools = g.runtime.AvailableTools()
	for round := 0; round < 4; round++ {
		result, err := provider.Generate(ctx, request)
		if err != nil {
			return contracts.GenerateResult{}, err
		}
		if len(result.ToolCalls) == 0 {
			return result, nil
		}
		outputs := make([]contracts.ToolOutput, 0, len(result.ToolCalls))
		for _, call := range result.ToolCalls {
			call.ConversationID = request.ConversationID
			call.MessageID = request.MessageID
			if call.IdempotencyKey == "" {
				call.IdempotencyKey = result.ResponseID + ":" + call.ID
			}
			toolResult, err := g.runtime.Execute(ctx, call)
			if err != nil {
				return contracts.GenerateResult{}, fmt.Errorf("execute tool %q: %w", call.Name, err)
			}
			outputs = append(outputs, contracts.ToolOutput{CallID: call.ID, Content: toolResult.Content})
		}
		request = contracts.GenerateRequest{
			Profile: request.Profile, PreviousResponseID: result.ResponseID,
			ToolOutputs: outputs, Tools: g.runtime.AvailableTools(),
			ConversationID: request.ConversationID, MessageID: request.MessageID,
		}
	}
	return contracts.GenerateResult{}, errors.New("AI exceeded maximum tool-call rounds")
}

func (g *Gateway) Stream(ctx context.Context, request contracts.GenerateRequest) (contracts.AIStream, error) {
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
	request.Tools = g.runtime.AvailableTools()
	for round := 0; round < 4; round++ {
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
			select {
			case events <- contracts.AIStreamEvent{Type: "response.completed", Result: completed, Done: true}:
			case <-ctx.Done():
			}
			return
		}

		outputs := make([]contracts.ToolOutput, 0, len(completed.ToolCalls))
		for _, call := range completed.ToolCalls {
			call.ConversationID = request.ConversationID
			call.MessageID = request.MessageID
			if call.IdempotencyKey == "" {
				call.IdempotencyKey = completed.ResponseID + ":" + call.ID
			}
			toolResult, err := g.runtime.Execute(ctx, call)
			if err != nil {
				sendStreamError(ctx, events, fmt.Errorf("execute tool %q: %w", call.Name, err))
				return
			}
			outputs = append(outputs, contracts.ToolOutput{CallID: call.ID, Content: toolResult.Content})
		}
		request = contracts.GenerateRequest{
			Profile: request.Profile, PreviousResponseID: completed.ResponseID,
			ToolOutputs: outputs, Tools: g.runtime.AvailableTools(),
			ConversationID: request.ConversationID, MessageID: request.MessageID,
		}
	}
	sendStreamError(ctx, events, errors.New("AI exceeded maximum tool-call rounds"))
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
