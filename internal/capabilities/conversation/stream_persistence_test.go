package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"workbench/internal/capabilities/ai"
	"workbench/internal/contracts"
)

type persistenceProvider struct {
	calls atomic.Int32
	next  func(context.Context) (contracts.AIStream, error)
}

func (*persistenceProvider) Generate(context.Context, contracts.GenerateRequest) (contracts.GenerateResult, error) {
	return contracts.GenerateResult{}, errors.New("unexpected synchronous request")
}

func (p *persistenceProvider) Stream(ctx context.Context, _ contracts.GenerateRequest) (contracts.AIStream, error) {
	if p.calls.Add(1) != 1 {
		return p.next(ctx)
	}
	return persistenceResult(contracts.GenerateResult{
		ResponseID: "response-writes",
		ToolCalls: []contracts.ToolCall{
			{ID: "call-first", Name: "todo.create_task", Arguments: json.RawMessage(`{}`)},
			{ID: "call-second", Name: "todo.create_task", Arguments: json.RawMessage(`{}`)},
		},
	}), nil
}

func persistenceResult(result contracts.GenerateResult) contracts.AIStream {
	events := make(chan contracts.AIStreamEvent, 1)
	events <- contracts.AIStreamEvent{Done: true, Result: &result}
	close(events)
	return conversationStream{events: events}
}

func configurePersistenceTool(t *testing.T, service *Service, provider contracts.AIProvider, handler contracts.ToolHandler) {
	t.Helper()
	runtime, err := ai.NewToolRuntime(service.db, []contracts.AITool{{
		Name: "todo.create_task", Module: "todo", SchemaVersion: 1, Description: "创建待办", Risk: contracts.ToolRiskLowWrite,
		ParametersSchema: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
		Handler:          handler,
	}}, func(contracts.ModuleID) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	service.gateway, err = ai.NewGateway(provider, runtime)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStreamingCancellationPersistsExecutedResultBeforeStopping(t *testing.T) {
	service, database := openConversationService(t)
	ctx, cancel := context.WithCancel(conversationUserContext("alice"))
	defer cancel()
	var writes atomic.Int32
	provider := &persistenceProvider{next: func(context.Context) (contracts.AIStream, error) {
		return nil, errors.New("must not start another provider round after cancellation")
	}}
	configurePersistenceTool(t, service, provider, func(toolCtx context.Context, _ contracts.ToolCall) (contracts.ToolResult, error) {
		if userID, _ := contracts.RequireUserID(toolCtx); userID != "alice" {
			t.Errorf("tool user = %q, want alice", userID)
		}
		writes.Add(1)
		// The action has committed, but the request disappears before the tool returns.
		cancel()
		return contracts.ToolResult{Content: json.RawMessage(`{"id":"task-committed","title":"Pay rent","version":1}`)}, nil
	})
	request := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(`{"message":"create tasks"}`)).WithContext(ctx)
	recorder := httptest.NewRecorder()
	NewHTTPHandler(service).Stream(recorder, request)
	if writes.Load() != 1 || provider.calls.Load() != 1 {
		t.Fatalf("writes/provider calls = %d/%d, stream=%s", writes.Load(), provider.calls.Load(), recorder.Body.String())
	}
	var conversationID, assistantID, saved string
	if err := database.SQL().QueryRow("SELECT conversation_id, id, content_json FROM ai_messages WHERE role = 'assistant'").Scan(&conversationID, &assistantID, &saved); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []string{"创建待办：已完成", "todo.create_task", "call-first", "task-committed", "Pay rent", "version"} {
		if !strings.Contains(saved, evidence) {
			t.Fatalf("saved result missing %q: %s", evidence, saved)
		}
	}
	if strings.Contains(saved, "call-second") {
		t.Fatalf("unexecuted tool was recorded: %s", saved)
	}
	page, err := service.messagePage(conversationUserContext("alice"), conversationID, 50, conversationCursor{})
	if err != nil || len(page.Messages) != 2 || page.Messages[1].ID != assistantID {
		t.Fatalf("history=%+v error=%v", page, err)
	}
	if _, err := service.messagePage(conversationUserContext("bob"), conversationID, 50, conversationCursor{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign result history error = %v", err)
	}

	// A subsequent turn sees the exact operation identity and result, not just the original request.
	capture := &capturingConversationProvider{}
	service.gateway.SetProvider(capture)
	if _, err := service.Chat(conversationUserContext("alice"), ChatRequest{ConversationID: conversationID, Message: "what happened?"}); err != nil {
		t.Fatal(err)
	}
	if len(capture.request.Messages) != 4 || capture.request.Messages[2].Role != "assistant" || !strings.Contains(string(capture.request.Messages[2].Content), "task-committed") {
		t.Fatalf("continuation missing committed operation: %+v", capture.request.Messages)
	}
}

func TestStreamingAbruptEndPreservesAllCompletedResults(t *testing.T) {
	service, database := openConversationService(t)
	provider := &persistenceProvider{next: func(context.Context) (contracts.AIStream, error) {
		events := make(chan contracts.AIStreamEvent)
		close(events)
		return conversationStream{events: events}, nil
	}}
	configurePersistenceTool(t, service, provider, func(_ context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
		content, _ := json.Marshal(map[string]string{"id": "task-" + call.ID})
		return contracts.ToolResult{Content: content}, nil
	})
	recorder := httptest.NewRecorder()
	NewHTTPHandler(service).Stream(recorder, conversationRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(`{"message":"create tasks"}`)))
	if strings.Count(recorder.Body.String(), "event: chat.tool") != 2 || !strings.Contains(recorder.Body.String(), "event: chat.error") {
		t.Fatalf("unexpected stream: %s", recorder.Body.String())
	}
	var count int
	var saved string
	if err := database.SQL().QueryRow("SELECT COUNT(*), content_json FROM ai_messages WHERE role = 'assistant' GROUP BY content_json").Scan(&count, &saved); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !strings.Contains(saved, "task-call-first") || !strings.Contains(saved, "task-call-second") {
		t.Fatalf("saved assistant count/content=%d/%s", count, saved)
	}
}

type persistenceRecorder struct {
	*httptest.ResponseRecorder
	onTool func()
}

func (r *persistenceRecorder) Flush() {
	r.ResponseRecorder.Flush()
	if r.onTool != nil && strings.Contains(r.Body.String(), "event: chat.tool") {
		callback := r.onTool
		r.onTool = nil
		callback()
	}
}

func TestStreamingFinalAnswerReplacesToolSummaryWithSameMessageID(t *testing.T) {
	service, database := openConversationService(t)
	provider := &persistenceProvider{next: func(context.Context) (contracts.AIStream, error) {
		return persistenceResult(contracts.GenerateResult{Text: "Both tasks created."}), nil
	}}
	configurePersistenceTool(t, service, provider, func(_ context.Context, call contracts.ToolCall) (contracts.ToolResult, error) {
		content, _ := json.Marshal(map[string]string{"id": "task-" + call.ID})
		return contracts.ToolResult{Content: content}, nil
	})
	var summaryID string
	recorder := &persistenceRecorder{ResponseRecorder: httptest.NewRecorder(), onTool: func() {
		var saved string
		if err := database.SQL().QueryRow("SELECT id, content_json FROM ai_messages WHERE role = 'assistant'").Scan(&summaryID, &saved); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(saved, "task-call-first") {
			t.Fatalf("tool SSE arrived before durable result: %s", saved)
		}
	}}
	NewHTTPHandler(service).Stream(recorder, conversationRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(`{"message":"create tasks"}`)))
	var started struct {
		ConversationID     string `json:"conversationId"`
		MessageID          string `json:"messageId"`
		AssistantMessageID string `json:"assistantMessageId"`
	}
	var completed ChatResponse
	for _, block := range strings.Split(recorder.Body.String(), "\n\n") {
		if strings.HasPrefix(block, "event: chat.started\ndata: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(block, "event: chat.started\ndata: ")), &started); err != nil {
				t.Fatal(err)
			}
		}
		if strings.HasPrefix(block, "event: chat.completed\ndata: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(block, "event: chat.completed\ndata: ")), &completed); err != nil {
				t.Fatal(err)
			}
		}
	}
	if summaryID == "" || started.AssistantMessageID != summaryID || completed.MessageID != summaryID || completed.Text != "Both tasks created." || len(completed.Tools) != 2 {
		t.Fatalf("summaryID=%q started=%+v completed=%+v stream=%s", summaryID, started, completed, recorder.Body.String())
	}
	page, err := service.messagePage(conversationUserContext(contracts.LegacyUserID), started.ConversationID, 50, conversationCursor{})
	if err != nil || len(page.Messages) != 2 || page.Messages[0].ID != started.MessageID || page.Messages[1].ID != summaryID || string(page.Messages[1].Content) != `"Both tasks created."` {
		t.Fatalf("final history=%+v error=%v", page, err)
	}
}

func TestToolResultRecorderPreservesOwnership(t *testing.T) {
	service, _ := openConversationService(t)
	turn, err := service.begin(conversationUserContext("bob"), ChatRequest{Message: "bob request"})
	if err != nil {
		t.Fatal(err)
	}
	record := service.toolRecorder(turn)
	if err := record(conversationUserContext("alice"), contracts.ToolExecution{Status: "succeeded"}, contracts.ToolResult{Content: json.RawMessage(`{"id":"private"}`)}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign tool persistence error=%v", err)
	}
	if err := record(context.Background(), contracts.ToolExecution{Status: "succeeded"}, contracts.ToolResult{}); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("anonymous tool persistence error=%v", err)
	}
	page, err := service.messagePage(conversationUserContext("bob"), turn.conversationID, 50, conversationCursor{})
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("foreign result changed history=%+v error=%v", page, err)
	}
}
