package conversation

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"workbench/internal/capabilities/ai"
	"workbench/internal/contracts"
)

func TestConfiguredModelsStatusSwitchingAndInvalidRequests(t *testing.T) {
	service, database := openConversationService(t)
	var requests []struct {
		Model  string          `json:"model"`
		Input  json.RawMessage `json:"input"`
		Stream bool            `json:"stream"`
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model  string          `json:"model"`
			Input  json.RawMessage `json:"input"`
			Stream bool            `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		requests = append(requests, request)
		response := fmt.Sprintf(`{"id":"resp_test","object":"response","created_at":1,"status":"completed","model":%q,"output":[{"id":"msg_test","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[]}]}]}`, request.Model)
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":%s}\n\ndata: [DONE]\n\n", response)
		} else {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, response)
		}
	}))
	defer upstream.Close()
	provider, err := ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "secret-key", BaseURL: upstream.URL, Profiles: map[string]ai.Profile{
		"default": {Model: "model-b"}, "model:model-a": {Model: "model-a"}, "model:model-b": {Model: "model-b"}, "internal": {Model: "private-model"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	service.gateway.SetProvider(provider)
	handler := NewHTTPHandler(service)
	status := httptest.NewRecorder()
	handler.Status(status, conversationRequest(http.MethodGet, "/api/ai/status", nil))
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"models":["model-a","model-b"]`) || !strings.Contains(status.Body.String(), `"defaultModel":"model-b"`) || strings.Contains(status.Body.String(), "secret") || strings.Contains(status.Body.String(), "private-model") {
		t.Fatalf("status=%s", status.Body.String())
	}
	for _, streaming := range []bool{false, true} {
		for _, model := range []string{"removed", " ", strings.Repeat("x", 201)} {
			recorder := httptest.NewRecorder()
			body, _ := json.Marshal(ChatRequest{Message: "must not persist", Model: model})
			req := conversationRequest(http.MethodPost, "/api/chat", strings.NewReader(string(body)))
			if streaming {
				handler.Stream(recorder, req)
			} else {
				handler.Chat(recorder, req)
			}
			if recorder.Code != 400 || !strings.Contains(recorder.Body.String(), "invalid_request") {
				t.Fatalf("invalid model response=%d %s", recorder.Code, recorder.Body.String())
			}
		}
	}
	var count int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM ai_messages").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid request persisted messages=%d err=%v", count, err)
	}
	ctx := conversationUserContext(contracts.LegacyUserID)
	first, err := service.Chat(ctx, ChatRequest{Message: "first question", Model: "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	body, _ := json.Marshal(ChatRequest{ConversationID: first.ConversationID, Message: "second question", Model: "model-b"})
	handler.Stream(recorder, conversationRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(string(body))))
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "chat.completed") {
		t.Fatalf("stream=%d %s", recorder.Code, recorder.Body.String())
	}
	if len(requests) != 2 || requests[0].Model != "model-a" || requests[1].Model != "model-b" || !strings.Contains(string(requests[1].Input), "first question") || !strings.Contains(string(requests[1].Input), "answer") || !strings.Contains(string(requests[1].Input), "second question") {
		t.Fatalf("switch lost context: %+v", requests)
	}
	var model string
	if err := database.SQL().QueryRow("SELECT model FROM ai_messages WHERE id = ?", first.MessageID).Scan(&model); err != nil || model != "model-a" {
		t.Fatalf("stored model=%q err=%v", model, err)
	}
	// Removing a model takes effect before another turn can persist a user message.
	service.gateway.SetProvider(nil)
	service.gateway.SetProvider(mustDefaultModelProvider(t))
	if _, err := service.Chat(ctx, ChatRequest{ConversationID: first.ConversationID, Message: "removed model", Model: "model-a"}); err == nil {
		t.Fatal("removed model accepted")
	}
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM ai_messages").Scan(&count); err != nil || count != 4 {
		t.Fatalf("removed model persisted messages=%d err=%v", count, err)
	}
}

func TestTurnProviderSnapshotSurvivesSettingsChange(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		for _, selection := range []string{"default", "explicit", "legacy"} {
			t.Run(fmt.Sprintf("stream=%t/%s", streaming, selection), func(t *testing.T) {
				service, database := openConversationService(t)
				requestedModels := make(chan string, 4)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var request struct {
						Model  string `json:"model"`
						Stream bool   `json:"stream"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					requestedModels <- request.Model
					response := fmt.Sprintf(`{"id":"resp_snapshot","object":"response","created_at":1,"status":"completed","model":%q,"output":[{"id":"msg_snapshot","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"answer","annotations":[]}]}]}`, request.Model)
					if request.Stream {
						w.Header().Set("Content-Type", "text/event-stream")
						fmt.Fprintf(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"sequence_number\":1,\"response\":%s}\n\ndata: [DONE]\n\n", response)
					} else {
						w.Header().Set("Content-Type", "application/json")
						fmt.Fprint(w, response)
					}
				}))
				defer upstream.Close()
				provider := func(model string) *ai.OpenAIProvider {
					p, err := ai.NewOpenAIProvider(ai.OpenAIConfig{
						APIKey: "key", BaseURL: upstream.URL,
						Profiles: map[string]ai.Profile{
							"default": {Model: model}, "model:" + model: {Model: model}, "legacy": {Model: model},
						},
					})
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				service.gateway.SetProvider(provider("model-a"))
				replacement := provider("model-b")
				var switched sync.Once
				service.now = func() time.Time {
					// begin reads the clock after allowlist validation, before saving
					// the user message and resolving the model for its assistant reply.
					switched.Do(func() { service.gateway.SetProvider(replacement) })
					return time.Now()
				}
				request := ChatRequest{Message: "first question"}
				switch selection {
				case "explicit":
					request.Model = "model-a"
				case "legacy":
					request.Profile = "legacy"
				}
				ctx := conversationUserContext(contracts.LegacyUserID)
				runTurn := func(request ChatRequest) (ChatResponse, error) {
					if !streaming {
						return service.Chat(ctx, request)
					}
					turn, stream, err := service.stream(ctx, request)
					if err != nil {
						return ChatResponse{}, err
					}
					defer stream.Close()
					for event := range stream.Events() {
						if event.Err != nil {
							return ChatResponse{}, event.Err
						}
						if event.Done && event.Result != nil {
							return service.complete(ctx, turn, *event.Result)
						}
					}
					return ChatResponse{}, fmt.Errorf("stream ended without result")
				}
				first, err := runTurn(request)
				if err != nil {
					t.Fatal(err)
				}
				assertModel := func(response ChatResponse, want string) {
					t.Helper()
					select {
					case got := <-requestedModels:
						if got != want {
							t.Fatalf("upstream model=%q want %q", got, want)
						}
					default:
						t.Fatal("no upstream request")
					}
					var stored string
					if err := database.SQL().QueryRow("SELECT model FROM ai_messages WHERE id = ?", response.MessageID).Scan(&stored); err != nil || stored != want {
						t.Fatalf("stored model=%q want %q err=%v", stored, want, err)
					}
				}
				assertModel(first, "model-a")
				second, err := runTurn(ChatRequest{ConversationID: first.ConversationID, Message: "next question"})
				if err != nil {
					t.Fatal(err)
				}
				assertModel(second, "model-b")
				if _, err := runTurn(ChatRequest{ConversationID: first.ConversationID, Message: "removed model", Model: "model-a"}); !errors.Is(err, ErrInvalidRequest) {
					t.Fatalf("removed model error=%v", err)
				}
				var count int
				if err := database.SQL().QueryRow("SELECT COUNT(*) FROM ai_messages").Scan(&count); err != nil || count != 4 {
					t.Fatalf("message count=%d want 4 err=%v", count, err)
				}
				if len(requestedModels) != 0 {
					t.Fatal("removed model reached upstream")
				}
			})
		}
	}
}

func mustDefaultModelProvider(t *testing.T) *ai.OpenAIProvider {
	t.Helper()
	provider, err := ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "key", Profiles: map[string]ai.Profile{"default": {Model: "model-b"}}})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}
