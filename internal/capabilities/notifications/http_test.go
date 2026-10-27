package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"workbench/internal/contracts"
)

func TestHTTPFiltersListAndUnreadCountBySource(t *testing.T) {
	service, _ := openService(t)
	for _, source := range []contracts.ModuleID{"todo", "news"} {
		if _, err := service.Create(context.Background(), contracts.NewNotification{
			SourceModule: source, Severity: contracts.NotificationInfo, Title: string(source),
			IdempotencyKey: "http:" + string(source),
		}); err != nil {
			t.Fatal(err)
		}
	}
	handler := NewHTTPHandler(service, nil)

	listRecorder := httptest.NewRecorder()
	handler.List(listRecorder, httptest.NewRequest(http.MethodGet, "/api/notifications?source=todo&unread=true&limit=1", nil))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRecorder.Code, listRecorder.Body.String())
	}
	var page contracts.NotificationPage
	if err := json.NewDecoder(listRecorder.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].SourceModule != "todo" {
		t.Fatalf("filtered HTTP page = %+v", page)
	}

	countRecorder := httptest.NewRecorder()
	handler.UnreadCount(countRecorder, httptest.NewRequest(http.MethodGet, "/api/notifications/unread-count?source=news", nil))
	if countRecorder.Code != http.StatusOK {
		t.Fatalf("count status = %d, body = %s", countRecorder.Code, countRecorder.Body.String())
	}
	var count struct {
		Count int64 `json:"count"`
	}
	if err := json.NewDecoder(countRecorder.Body).Decode(&count); err != nil {
		t.Fatal(err)
	}
	if count.Count != 1 {
		t.Fatalf("source unread count = %d, want 1", count.Count)
	}
}
