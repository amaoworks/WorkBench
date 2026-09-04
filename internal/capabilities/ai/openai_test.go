package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"workbench/internal/contracts"
)

func TestOpenAIProviderUsesResponsesWithRemoteStorageDisabled(t *testing.T) {
	var requestBody map[string]any
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		body := `{
  "id":"resp_test","object":"response","created_at":1,"status":"completed",
  "model":"test-model","output":[{
    "id":"msg_test","type":"message","status":"completed","role":"assistant",
    "content":[{"type":"output_text","text":"hello","annotations":[]}]
  }],
  "usage":{"input_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":3}
}`
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r,
		}, nil
	})}

	provider, err := NewOpenAIProvider(OpenAIConfig{
		APIKey: "test-key", BaseURL: "https://api.test/v1", HTTPClient: client,
		Profiles: map[string]Profile{"default": {Model: "test-model", MaxOutputTokens: 128}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Generate(context.Background(), contracts.GenerateRequest{
		Profile: "default", Messages: []contracts.AIMessage{TextMessage("user", "hello")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ResponseID != "resp_test" || result.Text != "hello" {
		t.Fatalf("result = %+v", result)
	}
	if stored, ok := requestBody["store"].(bool); !ok || stored {
		t.Fatalf("store = %#v, want false", requestBody["store"])
	}
	if requestBody["model"] != "test-model" {
		t.Fatalf("model = %#v", requestBody["model"])
	}
}

func TestOpenAIProviderStreamsTextAndCompletedResult(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":1,"item_id":"msg_test","output_index":0,"content_index":0,"delta":"hel"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","sequence_number":2,"item_id":"msg_test","output_index":0,"content_index":0,"delta":"lo"}

event: response.completed
data: {"type":"response.completed","sequence_number":3,"response":{"id":"resp_stream","object":"response","created_at":1,"status":"completed","model":"test-model","output":[{"id":"msg_test","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"hello","annotations":[]}]}],"usage":{"input_tokens":2,"input_tokens_details":{"cached_tokens":0},"output_tokens":1,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":3}}}

data: [DONE]

`
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r,
		}, nil
	})}
	provider, err := NewOpenAIProvider(OpenAIConfig{
		APIKey: "test-key", BaseURL: "https://api.test/v1", HTTPClient: client,
		Profiles: map[string]Profile{"default": {Model: "test-model", MaxOutputTokens: 128}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Stream(context.Background(), contracts.GenerateRequest{Profile: "default"})
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
	if text != "hello" || completed == nil || completed.ResponseID != "resp_stream" || completed.Text != "hello" {
		t.Fatalf("stream text/result = %q/%+v", text, completed)
	}
}

func TestOpenAIProviderAppliesRequestTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	provider, err := NewOpenAIProvider(OpenAIConfig{
		APIKey: "test-key", BaseURL: "https://api.test/v1", HTTPClient: client, Timeout: 10 * time.Millisecond,
		Profiles: map[string]Profile{"default": {Model: "test-model"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = provider.Generate(context.Background(), contracts.GenerateRequest{Profile: "default"})
	if err == nil {
		t.Fatal("Generate succeeded after its request timeout")
	}
	if time.Since(started) > time.Second {
		t.Fatalf("request timeout took too long: %v", time.Since(started))
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
