package conversation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"workbench/internal/contracts"
)

func conversationRouter(service *Service) http.Handler {
	handler := NewHTTPHandler(service)
	router := chi.NewRouter()
	router.Get("/api/conversations", handler.List)
	router.Get("/api/conversations/{id}/messages", handler.Messages)
	router.Patch("/api/conversations/{id}", handler.Rename)
	router.Delete("/api/conversations/{id}", handler.Archive)
	router.Post("/api/chat", handler.Chat)
	router.Post("/api/chat/stream", handler.Stream)
	router.Get("/api/chat/status", handler.Status)
	return router
}

func TestConversationPrivateHistoryAndMutations(t *testing.T) {
	service, database := openConversationService(t)
	alice, bob := conversationUserContext("alice"), conversationUserContext("bob")
	first, err := service.Chat(alice, ChatRequest{Message: "alice private message"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Chat(bob, ChatRequest{Message: "bob private message"})
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []struct {
		ctx context.Context
		id  string
	}{{alice, first.ConversationID}, {bob, second.ConversationID}} {
		page, err := service.listConversations(user.ctx, 1, conversationCursor{})
		if err != nil || len(page.Items) != 1 || page.Items[0].ID != user.id || page.NextCursor != nil {
			t.Fatalf("list=%+v error=%v", page, err)
		}
	}
	if _, err := service.messages(alice, second.ConversationID, 50); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign history error=%v", err)
	}
	if _, err := service.messagePage(alice, second.ConversationID, 50, conversationCursor{}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign page error=%v", err)
	}
	if err := service.rename(alice, second.ConversationID, "stolen"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign rename error=%v", err)
	}
	if err := service.archive(alice, second.ConversationID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign archive error=%v", err)
	}
	if _, err := service.Chat(alice, ChatRequest{ConversationID: second.ConversationID, Message: "inject"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign chat error=%v", err)
	}
	if _, _, err := service.stream(alice, ChatRequest{ConversationID: second.ConversationID, Message: "inject"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign stream error=%v", err)
	}
	turn, err := service.begin(bob, ChatRequest{ConversationID: second.ConversationID, Message: "follow up"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.complete(alice, turn, contracts.GenerateResult{Text: "injected answer"}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign completion error=%v", err)
	}
	if err := service.archive(alice, first.ConversationID); err != nil {
		t.Fatal(err)
	}
	page, err := service.listConversations(alice, 20, conversationCursor{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("archived list=%+v, error=%v", page, err)
	}
	var title string
	var archived sql.NullInt64
	var messageCount int
	if err := database.SQL().QueryRow("SELECT title, archived_at FROM ai_conversations WHERE id = ?", second.ConversationID).Scan(&title, &archived); err != nil {
		t.Fatal(err)
	}
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM ai_messages WHERE conversation_id = ?", second.ConversationID).Scan(&messageCount); err != nil {
		t.Fatal(err)
	}
	if title != "bob private message" || archived.Valid || messageCount != 3 {
		t.Fatalf("bob title/archive/messages=%q/%v/%d", title, archived, messageCount)
	}
}

func TestConversationHTTPRejectsForeignIDsAndAnonymousRequests(t *testing.T) {
	service, _ := openConversationService(t)
	response, err := service.Chat(conversationUserContext("bob"), ChatRequest{Message: "bob private"})
	if err != nil {
		t.Fatal(err)
	}
	router := conversationRouter(service)
	body, _ := json.Marshal(ChatRequest{ConversationID: response.ConversationID, Message: "inject"})
	requests := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/conversations/" + response.ConversationID + "/messages", ""},
		{http.MethodPatch, "/api/conversations/" + response.ConversationID, `{"title":"stolen"}`},
		{http.MethodDelete, "/api/conversations/" + response.ConversationID, ""},
		{http.MethodPost, "/api/chat", string(body)},
		{http.MethodPost, "/api/chat/stream", string(body)},
	}
	for _, test := range requests {
		t.Run(test.method+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)).WithContext(conversationUserContext("alice"))
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "bob private") {
				t.Fatalf("response=%d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	requests = append(requests,
		struct{ method, path, body string }{http.MethodGet, "/api/conversations", ""},
		struct{ method, path, body string }{http.MethodGet, "/api/chat/status", ""},
	)
	for _, test := range requests {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s %s response=%d %s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestConversationServiceRequiresPrincipal(t *testing.T) {
	service, _ := openConversationService(t)
	ctx := context.Background()
	checks := []func() error{
		func() error { _, err := service.Chat(ctx, ChatRequest{Message: "hello"}); return err },
		func() error { _, _, err := service.stream(ctx, ChatRequest{Message: "hello"}); return err },
		func() error { _, err := service.begin(ctx, ChatRequest{Message: "hello"}); return err },
		func() error { _, err := service.complete(ctx, chatTurn{}, contracts.GenerateResult{}); return err },
		func() error { _, err := service.listConversations(ctx, 20, conversationCursor{}); return err },
		func() error { _, err := service.messagePage(ctx, "other", 20, conversationCursor{}); return err },
		func() error { _, err := service.messages(ctx, "other", 20); return err },
		func() error { return service.rename(ctx, "other", "title") },
		func() error { return service.archive(ctx, "other") },
	}
	for index, check := range checks {
		if err := check(); !errors.Is(err, contracts.ErrUnauthenticated) {
			t.Errorf("check %d error=%v", index, err)
		}
	}
}
