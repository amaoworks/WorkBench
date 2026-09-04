package conversation

import (
	"encoding/json"
	"fmt"
	"net/http"

	"workbench/internal/capabilities/ai"
	"workbench/internal/foundation/httpapi"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) *HTTPHandler { return &HTTPHandler{service: service} }

func (h *HTTPHandler) Status(w http.ResponseWriter, _ *http.Request) {
	httpapi.Write(w, http.StatusOK, map[string]bool{"available": h.service.Available()})
}

func (h *HTTPHandler) Chat(w http.ResponseWriter, r *http.Request) {
	var request ChatRequest
	if err := httpapi.Decode(w, r, &request, 64*1024); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid chat request")
		return
	}
	response, err := h.service.Chat(r.Context(), request)
	if err != nil {
		status := http.StatusInternalServerError
		code := "chat_failed"
		if err == ai.ErrUnavailable {
			status, code = http.StatusServiceUnavailable, "ai_unavailable"
		}
		httpapi.Error(w, status, code, err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, response)
}

func (h *HTTPHandler) Stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpapi.Error(w, http.StatusInternalServerError, "stream_unsupported", "streaming unsupported")
		return
	}
	var request ChatRequest
	if err := httpapi.Decode(w, r, &request, 64*1024); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid chat request")
		return
	}
	turn, stream, err := h.service.stream(r.Context(), request)
	if err != nil {
		status := http.StatusInternalServerError
		code := "chat_failed"
		if err == ai.ErrUnavailable {
			status, code = http.StatusServiceUnavailable, "ai_unavailable"
		}
		httpapi.Error(w, status, code, err.Error())
		return
	}
	defer stream.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	writeSSE(w, "chat.started", map[string]string{"conversationId": turn.conversationID, "messageId": turn.messageID})
	flusher.Flush()
	for event := range stream.Events() {
		if event.Err != nil {
			writeSSE(w, "chat.error", map[string]string{"message": event.Err.Error()})
			flusher.Flush()
			return
		}
		if event.Text != "" {
			writeSSE(w, "chat.delta", map[string]string{"text": event.Text})
			flusher.Flush()
		}
		if event.Done && event.Result != nil {
			response, err := h.service.complete(r.Context(), turn, *event.Result)
			if err != nil {
				writeSSE(w, "chat.error", map[string]string{"message": err.Error()})
				flusher.Flush()
				return
			}
			writeSSE(w, "chat.completed", response)
			flusher.Flush()
			return
		}
	}
	writeSSE(w, "chat.error", map[string]string{"message": "chat stream ended unexpectedly"})
	flusher.Flush()
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	payload, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}
