package ai

import (
	"context"
	"errors"
	"sync"
	"testing"

	"workbench/internal/contracts"
)

type waitingProvider struct {
	entered chan struct{}
	release chan struct{}
}

func (p *waitingProvider) Generate(ctx context.Context, _ contracts.GenerateRequest) (contracts.GenerateResult, error) {
	close(p.entered)
	select {
	case <-p.release:
		return contracts.GenerateResult{Text: "original provider"}, nil
	case <-ctx.Done():
		return contracts.GenerateResult{}, ctx.Err()
	}
}
func (p *waitingProvider) Stream(context.Context, contracts.GenerateRequest) (contracts.AIStream, error) {
	return nil, errors.New("not used")
}

func TestProviderUpdateDoesNotInterruptInFlightRequest(t *testing.T) {
	database := openAIDatabase(t)
	runtime, err := NewToolRuntime(database.SQL(), nil, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	provider := &waitingProvider{entered: make(chan struct{}), release: make(chan struct{})}
	gateway, err := NewGateway(provider, runtime)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		result, err := gateway.Generate(context.Background(), contracts.GenerateRequest{})
		if err != nil || result.Text != "original provider" {
			t.Errorf("in-flight request changed provider: %+v %v", result, err)
		}
	}()
	<-provider.entered
	gateway.SetProvider(nil)
	if gateway.Available() {
		t.Fatal("provider not disabled")
	}
	if _, err := gateway.Generate(context.Background(), contracts.GenerateRequest{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("new request: %v", err)
	}
	close(provider.release)
	<-done
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			gateway.SetProvider(provider)
			_ = gateway.Available()
			gateway.SetProvider(nil)
		}()
	}
	wg.Wait()
}
