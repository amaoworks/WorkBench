package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"workbench/internal/contracts"
)

func TestSelectedOpenAIModelStaysStableAcrossToolRounds(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			database := openAIDatabase(t)
			var writes atomic.Int32
			runtime, err := NewToolRuntime(database.SQL(), []contracts.AITool{continuationWriteTool(&writes)}, func(contracts.ModuleID) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var request struct {
					Model string          `json:"model"`
					Input json.RawMessage `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				calls++
				if request.Model != "chosen" {
					t.Errorf("round %d model=%q", calls, request.Model)
				}
				output := `[{"id":"msg","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"done","annotations":[]}]}]`
				if calls == 1 {
					output = `[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"test.write","arguments":"{\"step\":1}"}]`
				} else if !strings.Contains(string(request.Input), "function_call_output") {
					t.Errorf("tool context missing: %s", request.Input)
				}
				body := fmt.Sprintf(`{"id":"resp_%d","object":"response","created_at":1,"status":"completed","model":"chosen","output":%s}`, calls, output)
				contentType := "application/json"
				if streaming {
					contentType = "text/event-stream"
					body = fmt.Sprintf("event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":%s}\n\ndata: [DONE]\n\n", body)
				}
				return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			provider, err := NewOpenAIProvider(OpenAIConfig{APIKey: "key", HTTPClient: client, Profiles: map[string]Profile{"default": {Model: "original"}, "model:chosen": {Model: "chosen"}}})
			if err != nil {
				t.Fatal(err)
			}
			models, defaultModel := provider.Models()
			if strings.Join(models, ",") != "chosen,original" || defaultModel != "original" {
				t.Fatalf("catalog=%v default=%s", models, defaultModel)
			}
			gateway, err := NewGateway(provider, runtime)
			if err != nil {
				t.Fatal(err)
			}
			request := contracts.GenerateRequest{Profile: "model:chosen", Messages: []contracts.AIMessage{TextMessage("user", "write once")}}
			ctx := aiUserContext("user")
			if streaming {
				stream, err := gateway.Stream(ctx, request)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				for event := range stream.Events() {
					if event.Err != nil {
						t.Fatal(event.Err)
					}
				}
			} else {
				if _, err := gateway.Generate(ctx, request); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 || writes.Load() != 1 {
				t.Fatalf("calls=%d writes=%d", calls, writes.Load())
			}
		})
	}
}

func TestDefaultModelSupportsExplicitSelectionWithoutAlias(t *testing.T) {
	provider, err := NewOpenAIProvider(OpenAIConfig{APIKey: "key", Profiles: map[string]Profile{"default": {Model: "original"}}})
	if err != nil {
		t.Fatal(err)
	}
	params, err := provider.params(contracts.GenerateRequest{Profile: "model:original"})
	if err != nil || string(params.Model) != "original" {
		t.Fatalf("params=%+v err=%v", params, err)
	}
	if _, err := provider.Generate(context.Background(), contracts.GenerateRequest{Profile: "model:removed"}); err == nil {
		t.Fatal("unknown model accepted")
	}
}
