package conversation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"workbench/internal/capabilities/ai"
	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

type conversationProvider struct{}

func (conversationProvider) Generate(context.Context, contracts.GenerateRequest) (contracts.GenerateResult, error) {
	return contracts.GenerateResult{ResponseID: "response-sync", Text: "sync answer"}, nil
}

func (conversationProvider) Stream(context.Context, contracts.GenerateRequest) (contracts.AIStream, error) {
	result := contracts.GenerateResult{ResponseID: "response-stream", Text: "streamed answer"}
	events := make(chan contracts.AIStreamEvent, 3)
	events <- contracts.AIStreamEvent{Type: "response.output_text.delta", Text: "streamed "}
	events <- contracts.AIStreamEvent{Type: "response.output_text.delta", Text: "answer"}
	events <- contracts.AIStreamEvent{Type: "response.completed", Result: &result, Done: true}
	close(events)
	return conversationStream{events: events}, nil
}

type capturingConversationProvider struct{ request contracts.GenerateRequest }

func (p *capturingConversationProvider) Generate(_ context.Context, request contracts.GenerateRequest) (contracts.GenerateResult, error) {
	p.request = request
	return contracts.GenerateResult{ResponseID: "response-follow-up", Text: "follow-up answer"}, nil
}

func (*capturingConversationProvider) Stream(context.Context, contracts.GenerateRequest) (contracts.AIStream, error) {
	return nil, nil
}

type conversationStream struct {
	events <-chan contracts.AIStreamEvent
}

func (s conversationStream) Events() <-chan contracts.AIStreamEvent { return s.events }
func (conversationStream) Close() error                             { return nil }

func openConversationService(t *testing.T) (*Service, *workbenchdb.Database) {
	t.Helper()
	database, err := workbenchdb.Open(context.Background(), workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtime, err := ai.NewToolRuntime(database.SQL(), nil, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := ai.NewGateway(conversationProvider{}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(database.SQL(), gateway)
	if err != nil {
		t.Fatal(err)
	}
	return service, database
}

func TestStreamingHTTPPersistsCompletedConversation(t *testing.T) {
	service, database := openConversationService(t)
	handler := NewHTTPHandler(service)
	request := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(`{"message":"hello"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.Stream(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("stream status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{"event: chat.started", "event: chat.delta", "streamed ", "event: chat.completed", "streamed answer"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("stream body does not contain %q: %s", expected, body)
		}
	}
	var conversations, messages int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM ai_conversations").Scan(&conversations); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM ai_messages").Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if conversations != 1 || messages != 2 {
		t.Fatalf("persisted conversations/messages = %d/%d, want 1/2", conversations, messages)
	}
}

func TestConversationRemainsUsableWhenAIIsUnavailable(t *testing.T) {
	database, err := workbenchdb.Open(context.Background(), workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.MigrateCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	runtime, err := ai.NewToolRuntime(database.SQL(), nil, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := ai.NewGateway(nil, runtime)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(database.SQL(), gateway)
	if err != nil {
		t.Fatal(err)
	}
	if service.Available() {
		t.Fatal("service reported unavailable AI as available")
	}
	if _, err := service.Chat(context.Background(), ChatRequest{Message: "hello"}); err != ai.ErrUnavailable {
		t.Fatalf("Chat error = %v, want ErrUnavailable", err)
	}
	var conversations int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM ai_conversations").Scan(&conversations); err != nil {
		t.Fatal(err)
	}
	if conversations != 0 {
		t.Fatalf("unavailable request persisted %d conversations, want 0", conversations)
	}
}

func TestConversationHistorySurvivesDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	open := func(t *testing.T) *workbenchdb.Database {
		t.Helper()
		database, err := workbenchdb.Open(context.Background(), workbenchdb.Config{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := database.MigrateCore(context.Background()); err != nil {
			database.Close()
			t.Fatal(err)
		}
		return database
	}
	newService := func(t *testing.T, database *workbenchdb.Database, provider contracts.AIProvider) *Service {
		t.Helper()
		runtime, err := ai.NewToolRuntime(database.SQL(), nil, func(contracts.ModuleID) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		gateway, err := ai.NewGateway(provider, runtime)
		if err != nil {
			t.Fatal(err)
		}
		service, err := NewService(database.SQL(), gateway)
		if err != nil {
			t.Fatal(err)
		}
		return service
	}

	firstDatabase := open(t)
	first := newService(t, firstDatabase, conversationProvider{})
	initial, err := first.Chat(context.Background(), ChatRequest{Message: "first question"})
	if err != nil {
		t.Fatal(err)
	}
	if err := firstDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	secondDatabase := open(t)
	defer secondDatabase.Close()
	capture := &capturingConversationProvider{}
	second := newService(t, secondDatabase, capture)
	if _, err := second.Chat(context.Background(), ChatRequest{ConversationID: initial.ConversationID, Message: "second question"}); err != nil {
		t.Fatal(err)
	}
	if len(capture.request.Messages) != 3 {
		t.Fatalf("restored history message count = %d, want 3", len(capture.request.Messages))
	}
	wantRoles := []string{"user", "assistant", "user"}
	for index, role := range wantRoles {
		if capture.request.Messages[index].Role != role {
			t.Fatalf("restored history roles = %+v, want %v", capture.request.Messages, wantRoles)
		}
	}
}
