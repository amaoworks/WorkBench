package ai

import (
	"context"
	"errors"
	"strings"
	"sync"

	"workbench/internal/contracts"
)

// TextService snapshots the current provider per call, sharing live settings without exposing tools.
type TextService struct {
	mu       sync.RWMutex
	provider contracts.AIProvider
}

func NewTextService() *TextService { return &TextService{} }
func (s *TextService) SetProvider(provider contracts.AIProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provider = provider
}
func (s *TextService) GenerateText(ctx context.Context, input contracts.TextRequest) (string, error) {
	s.mu.RLock()
	provider := s.provider
	s.mu.RUnlock()
	if provider == nil {
		return "", contracts.ErrAIUnavailable
	}
	if input.Profile == "" {
		input.Profile = "default"
	}
	if len(input.Input) > 64000 || len(input.Instruction) > 8000 {
		return "", errors.New("AI input exceeds limit")
	}
	result, err := provider.Generate(ctx, contracts.GenerateRequest{
		Profile:  input.Profile,
		Messages: []contracts.AIMessage{TextMessage("system", input.Instruction), TextMessage("user", input.Input)},
	})
	if err != nil {
		return "", errors.New("AI generation failed")
	}
	if len(result.ToolCalls) != 0 || strings.TrimSpace(result.Text) == "" {
		return "", errors.New("AI returned no usable text")
	}
	return result.Text, nil
}
