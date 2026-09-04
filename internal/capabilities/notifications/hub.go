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
	subscribers map[chan StreamEvent]struct{}
	heartbeat   time.Duration
}

func NewHub() *Hub {
	return &Hub{subscribers: make(map[chan StreamEvent]struct{}), heartbeat: 20 * time.Second}
}

func (h *Hub) Consumer() contracts.EventConsumer {
	return contracts.EventConsumer{
		ID: "core.notification_sse", Module: "core",
		Topics:  []string{"core.notification.created", "core.notification.updated", "core.notification.archived"},
		Timeout: 5 * time.Second, MaxAttempts: 3,
		Handler: func(_ context.Context, event contracts.Event) error {
			h.Broadcast(StreamEvent{ID: string(event.ID), Type: event.Topic, Data: event.Payload})
			return nil
		},
	}
}

func (h *Hub) Broadcast(event StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for subscriber := range h.subscribers {
		select {
		case subscriber <- event:
		default:
			// The database remains authoritative. A slow client will re-query.
		}
	}
}

func (h *Hub) subscribe() (<-chan StreamEvent, func()) {
	stream := make(chan StreamEvent, 16)
	h.mu.Lock()
	h.subscribers[stream] = struct{}{}
	h.mu.Unlock()
	return stream, func() {
		h.mu.Lock()
		delete(h.subscribers, stream)
		close(stream)
		h.mu.Unlock()
	}
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	stream, unsubscribe := h.subscribe()
	defer unsubscribe()
	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case event := <-stream:
			_, _ = fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", event.ID, event.Type, event.Data)
			flusher.Flush()
		}
	}
}
