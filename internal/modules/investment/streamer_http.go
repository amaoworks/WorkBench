package investment

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"workbench/internal/foundation/httpapi"
)

var wsUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Host, r.Host)
}}

func (m *Module) serveStreamer(w http.ResponseWriter, r *http.Request) {
	if _, err := m.ensureAccessToken(r.Context()); err != nil {
		httpapi.Error(w, http.StatusConflict, "schwab_disconnected", err.Error())
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	client := &streamerClient{conn: conn, send: make(chan []byte, 64)}
	if err := m.streamer.add(r.Context(), client); err != nil {
		m.logger.Warn("Schwab stream connection failed", "errorType", fmt.Sprintf("%T", err))
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "Schwab connection unavailable"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	}
	go client.writePump()
	defer m.streamer.remove(client)
	conn.SetReadLimit(4096)
	// Browser connections only receive events. Commands use the CSRF-protected HTTP endpoint.
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (m *Module) streamerCommand(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Service   string `json:"service"`
		Command   string `json:"command"`
		Keys      string `json:"keys"`
		Fields    string `json:"fields"`
		RequestID any    `json:"requestId"`
	}
	if httpapi.Decode(w, r, &input, 64*1024) != nil {
		httpapi.Error(w, 400, "invalid_request", "无效的 streamer 命令")
		return
	}
	service, command := strings.ToUpper(input.Service), strings.ToUpper(input.Command)
	switch service {
	case "LEVELONE_EQUITIES", "LEVELONE_OPTIONS", "LEVELONE_FUTURES", "LEVELONE_FUTURES_OPTIONS", "LEVELONE_FOREX":
	default:
		httpapi.Error(w, 400, "invalid_request", "不支持的行情服务")
		return
	}
	if command != "ADD" || strings.TrimSpace(input.Keys) == "" {
		httpapi.Error(w, 400, "invalid_request", "请使用 ADD 并提供品种代码")
		return
	}
	if _, err := m.ensureAccessToken(r.Context()); err != nil {
		httpapi.Error(w, 409, "schwab_disconnected", err.Error())
		return
	}
	if err := m.streamer.sendCommand(r.Context(), service, input.Keys, input.Fields); err != nil {
		httpapi.Error(w, 503, "schwab_ws_disconnected", err.Error())
		return
	}
	httpapi.Write(w, 200, map[string]bool{"success": true})
}
