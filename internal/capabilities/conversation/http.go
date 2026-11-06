package conversation

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"workbench/internal/capabilities/ai"
	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
)

type HTTPHandler struct{ service *Service }

func NewHTTPHandler(service *Service) *HTTPHandler { return &HTTPHandler{service: service} }

func (h *HTTPHandler) Status(w http.ResponseWriter, r *http.Request) {
	if !authenticated(w, r) {
		return
	}
	models, defaultModel := h.service.gateway.Models()
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, struct {
		Available    bool     `json:"available"`
		Models       []string `json:"models"`
		DefaultModel string   `json:"defaultModel"`
	}{Available: h.service.Available(), Models: models, DefaultModel: defaultModel})
}

func (h *HTTPHandler) List(w http.ResponseWriter, r *http.Request) {
	if !authenticated(w, r) {
		return
	}
	limit, cursor, err := pageParams(r, 20)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	page, err := h.service.listConversations(r.Context(), limit, cursor)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "conversations_failed", "对话列表加载失败")
		return
	}
	response := struct {
		Items      []Conversation `json:"items"`
		NextCursor string         `json:"nextCursor,omitempty"`
	}{Items: page.Items}
	if page.NextCursor != nil {
		response.NextCursor = encodeCursor(*page.NextCursor)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, response)
}

func (h *HTTPHandler) Messages(w http.ResponseWriter, r *http.Request) {
	if !authenticated(w, r) {
		return
	}
	limit, cursor, err := pageParams(r, 50)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_pagination", "分页参数无效")
		return
	}
	page, err := h.service.messagePage(r.Context(), chi.URLParam(r, "id"), limit, cursor)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpapi.Error(w, http.StatusNotFound, "conversation_not_found", "对话不存在")
			return
		}
		httpapi.Error(w, http.StatusInternalServerError, "history_failed", "对话加载失败")
		return
	}
	response := struct {
		Messages   []Message `json:"messages"`
		NextCursor string    `json:"nextCursor,omitempty"`
	}{Messages: page.Messages}
	if page.NextCursor != nil {
		response.NextCursor = encodeCursor(*page.NextCursor)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, response)
}

func (h *HTTPHandler) Rename(w http.ResponseWriter, r *http.Request) {
	if !authenticated(w, r) {
		return
	}
	var request struct {
		Title string `json:"title"`
	}
	if err := httpapi.Decode(w, r, &request, 2*1024); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "标题无效")
		return
	}
	if err := h.service.rename(r.Context(), chi.URLParam(r, "id"), request.Title); err != nil {
		switch {
		case errors.Is(err, ErrInvalidRequest):
			httpapi.Error(w, http.StatusBadRequest, "invalid_request", "标题长度须为 1 至 120 个字符")
		case errors.Is(err, sql.ErrNoRows):
			httpapi.Error(w, http.StatusNotFound, "conversation_not_found", "对话不存在")
		default:
			httpapi.Error(w, http.StatusInternalServerError, "rename_failed", "标题保存失败")
		}
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]bool{"saved": true})
}

func (h *HTTPHandler) Archive(w http.ResponseWriter, r *http.Request) {
	if !authenticated(w, r) {
		return
	}
	if err := h.service.archive(r.Context(), chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpapi.Error(w, http.StatusNotFound, "conversation_not_found", "对话不存在")
			return
		}
		httpapi.Error(w, http.StatusInternalServerError, "archive_failed", "对话删除失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HTTPHandler) Chat(w http.ResponseWriter, r *http.Request) {
	if !authenticated(w, r) {
		return
	}
	var request ChatRequest
	if err := httpapi.Decode(w, r, &request, 64*1024); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid chat request")
		return
	}
	response, err := h.service.Chat(r.Context(), request)
	if err != nil {
		writeChatError(w, err)
		return
	}
	httpapi.Write(w, http.StatusOK, response)
}

func (h *HTTPHandler) Stream(w http.ResponseWriter, r *http.Request) {
	if !authenticated(w, r) {
		return
	}
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
		writeChatError(w, err)
		return
	}
	defer stream.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	writeSSE(w, "chat.started", map[string]string{
		"conversationId": turn.conversationID, "messageId": turn.messageID, "assistantMessageId": turn.assistantID,
	})
	flusher.Flush()
	for event := range stream.Events() {
		if event.Tool != nil {
			writeSSE(w, "chat.tool", event.Tool)
			flusher.Flush()
		}
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

func writeChatError(w http.ResponseWriter, err error) {
	status, code := http.StatusInternalServerError, "chat_failed"
	switch {
	case errors.Is(err, contracts.ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "unauthenticated"
	case errors.Is(err, sql.ErrNoRows):
		status, code = http.StatusNotFound, "conversation_not_found"
	case errors.Is(err, ai.ErrUnavailable):
		status, code = http.StatusServiceUnavailable, "ai_unavailable"
	case errors.Is(err, ErrInvalidRequest):
		status, code = http.StatusBadRequest, "invalid_request"
	}
	httpapi.Error(w, status, code, err.Error())
}

func authenticated(w http.ResponseWriter, r *http.Request) bool {
	if _, err := contracts.RequireUserID(r.Context()); err != nil {
		httpapi.Error(w, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return false
	}
	return true
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	payload, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
}

func pageParams(r *http.Request, defaultLimit int) (int, conversationCursor, error) {
	limit := defaultLimit
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, conversationCursor{}, ErrInvalidRequest
		}
		limit = parsed
	}
	cursor, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return 0, conversationCursor{}, err
	}
	return limit, cursor, nil
}

func encodeCursor(cursor conversationCursor) string {
	payload, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeCursor(value string) (conversationCursor, error) {
	if value == "" {
		return conversationCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return conversationCursor{}, ErrInvalidRequest
	}
	var cursor conversationCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Timestamp < 1 || cursor.ID == "" {
		return conversationCursor{}, ErrInvalidRequest
	}
	return cursor, nil
}
