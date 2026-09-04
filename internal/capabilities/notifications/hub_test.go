package notifications

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type streamRecorder struct {
	header http.Header
	mu     sync.Mutex
	body   bytes.Buffer
	wrote  chan struct{}
}

func newStreamRecorder() *streamRecorder {
	return &streamRecorder{header: make(http.Header), wrote: make(chan struct{}, 1)}
}

func (r *streamRecorder) Header() http.Header { return r.header }
func (r *streamRecorder) WriteHeader(int)     {}
func (r *streamRecorder) Flush()              {}

func (r *streamRecorder) Write(value []byte) (int, error) {
	r.mu.Lock()
	n, err := r.body.Write(value)
	r.mu.Unlock()
	select {
	case r.wrote <- struct{}{}:
	default:
	}
	return n, err
}

func (r *streamRecorder) contains(value string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Contains(r.body.Bytes(), []byte(value))
}

func waitForStreamText(t *testing.T, recorder *streamRecorder, value string) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for !recorder.contains(value) {
		select {
		case <-recorder.wrote:
		case <-timer.C:
			t.Fatalf("SSE stream did not contain %q", value)
		}
	}
}

func TestHubStreamsEventsAndHeartbeat(t *testing.T) {
	hub := NewHub()
	hub.heartbeat = 5 * time.Millisecond
	recorder := newStreamRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/notifications/stream", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		hub.ServeHTTP(recorder, request)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		hub.mu.Lock()
		subscribers := len(hub.subscribers)
		hub.mu.Unlock()
		if subscribers == 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("SSE handler did not subscribe")
		}
		time.Sleep(time.Millisecond)
	}
	hub.Broadcast(StreamEvent{ID: "event-1", Type: "core.notification.created", Data: []byte(`{"id":"notice-1"}`)})
	waitForStreamText(t, recorder, "event: core.notification.created")
	waitForStreamText(t, recorder, ": heartbeat")
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not stop after disconnect")
	}
}
