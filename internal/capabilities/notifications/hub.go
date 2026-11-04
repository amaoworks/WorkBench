package notifications

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"workbench/internal/contracts"
)

type StreamEvent struct {
	ID   string
	Type string
	Data []byte
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[chan StreamEvent]string
	heartbeat   time.Duration
	users       contracts.UserDirectory
}

func NewHub() *Hub {
	return &Hub{subscribers: make(map[chan StreamEvent]string), heartbeat: 20 * time.Second}
}

func (h *Hub) SetUserDirectory(users contracts.UserDirectory) { h.users = users }

func (h *Hub) Consumer() contracts.EventConsumer {
	return contracts.EventConsumer{ID: "core.notification_sse", Module: "core",
		Topics: []string{"core.notification.created", "core.notification.updated", "core.notification.archived"}, Timeout: 5 * time.Second, MaxAttempts: 3,
		Handler: func(ctx context.Context, event contracts.Event) error {
			if event.UserID == "" || contracts.UserID(ctx) != event.UserID {
				return contracts.ErrUnauthenticated
			}
			h.Send(event.UserID, StreamEvent{ID: string(event.ID), Type: event.Topic, Data: event.Payload})
			return nil
		}}
}

// Send only addresses streams belonging to the event recipient.
func (h *Hub) Send(userID string, event StreamEvent) {
	if userID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for subscriber, recipient := range h.subscribers {
		if recipient != userID {
			continue
		}
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (h *Hub) subscribe(userID string) (<-chan StreamEvent, func()) {
	stream := make(chan StreamEvent, 16)
	h.mu.Lock()
	h.subscribers[stream] = userID
	h.mu.Unlock()
	return stream, func() { h.mu.Lock(); delete(h.subscribers, stream); close(stream); h.mu.Unlock() }
}

func (h *Hub) active(ctx context.Context, userID string) bool {
	if h.users == nil {
		return true
	}
	active, err := h.users.IsUserActive(ctx, userID)
	return err == nil && active
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userID, err := contracts.RequireUserID(r.Context())
	if err != nil || !h.active(r.Context(), userID) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	stream, unsubscribe := h.subscribe(userID)
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()
	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if !h.active(r.Context(), userID) {
				return
			}
			if _, err = fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case event := <-stream:
			if !h.active(r.Context(), userID) {
				return
			}
			if _, err = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
