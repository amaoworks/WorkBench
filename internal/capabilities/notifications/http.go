package notifications

import (
	"net/http"
	"strconv"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
)

type HTTPHandler struct {
	service *Service
	hub     *Hub
}

func NewHTTPHandler(service *Service, hub *Hub) *HTTPHandler {
	return &HTTPHandler{service: service, hub: hub}
}

func (h *HTTPHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	page, err := h.service.List(r.Context(), contracts.NotificationQuery{
		UnreadOnly: r.URL.Query().Get("unread") == "true",
		Limit:      limit,
		Cursor:     r.URL.Query().Get("cursor"),
	})
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "notification_list_failed", err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, page)
}

func (h *HTTPHandler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	count, err := h.service.UnreadCount(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "notification_count_failed", "could not count notifications")
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]int64{"count": count})
}

func (h *HTTPHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := httpapi.Decode(w, r, &input, 16*1024); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid notification ids")
		return
	}
	if err := h.service.MarkRead(r.Context(), input.IDs); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "notification_update_failed", err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *HTTPHandler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	if err := h.service.MarkAllRead(r.Context(), time.Now().UTC()); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "notification_update_failed", "could not update notifications")
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *HTTPHandler) Archive(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []string `json:"ids"`
	}
	if err := httpapi.Decode(w, r, &input, 16*1024); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "invalid notification ids")
		return
	}
	if err := h.service.Archive(r.Context(), input.IDs); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "notification_update_failed", err.Error())
		return
	}
	httpapi.Write(w, http.StatusOK, map[string]bool{"updated": true})
}

func (h *HTTPHandler) Stream(w http.ResponseWriter, r *http.Request) {
	h.hub.ServeHTTP(w, r)
}
