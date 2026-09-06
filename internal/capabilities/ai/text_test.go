package ai

import (
	"context"
	"errors"
	"strings"
	"testing"
	"workbench/internal/contracts"
)

type textProvider struct {
	request contracts.GenerateRequest
	fail    bool
	tools   bool
}

func (p *textProvider) Generate(_ context.Context, request contracts.GenerateRequest) (contracts.GenerateResult, error) {
	p.request = request
	if p.fail {
		return contracts.GenerateResult{}, errors.New("secret-api-key in upstream error")
	}
	if p.tools {
		return contracts.GenerateResult{Text: "text", ToolCalls: []contracts.ToolCall{{Name: "todo.create_task"}}}, nil
	}
	return contracts.GenerateResult{Text: "summary"}, nil
}
func (*textProvider) Stream(context.Context, contracts.GenerateRequest) (contracts.AIStream, error) {
	return nil, errors.New("unused")
}
func TestBusinessTextHasNoToolsAndRedactsProviderErrors(t *testing.T) {
	service := NewTextService()
	if _, err := service.GenerateText(context.Background(), contracts.TextRequest{}); !errors.Is(err, contracts.ErrAIUnavailable) {
		t.Fatal(err)
	}
	provider := &textProvider{}
	service.SetProvider(provider)
	text, err := service.GenerateText(context.Background(), contracts.TextRequest{Instruction: "describe", Input: "data"})
	if err != nil || text != "summary" || len(provider.request.Tools) != 0 || provider.request.Profile != "default" || len(provider.request.Messages) != 2 {
		t.Fatalf("request: %+v %v", provider.request, err)
	}
	provider.fail = true
	if _, err := service.GenerateText(context.Background(), contracts.TextRequest{}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unredacted error: %v", err)
	}
	provider.fail = false
	provider.tools = true
	if _, err := service.GenerateText(context.Background(), contracts.TextRequest{}); err == nil {
		t.Fatal("accepted unsolicited tool call")
	}
}
func TestBusinessTextSettingChangePreservesInflightCall(t *testing.T) {
	service := NewTextService()
	provider := &waitingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	service.SetProvider(provider)
	done := make(chan struct{})
	go func() {
		defer close(done)
		text, err := service.GenerateText(context.Background(), contracts.TextRequest{})
		if err != nil || text != "original provider" {
			t.Errorf("inflight: %s %v", text, err)
		}
	}()
	<-provider.entered
	service.SetProvider(nil)
	if _, err := service.GenerateText(context.Background(), contracts.TextRequest{}); !errors.Is(err, contracts.ErrAIUnavailable) {
		t.Fatal(err)
	}
	close(provider.release)
	<-done
}
