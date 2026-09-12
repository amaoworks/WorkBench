package investment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"workbench/internal/foundation/httpapi"
)

type streamerInfo struct {
	StreamerSocketURL      string `json:"streamerSocketUrl"`
	SchwabClientCustomerID string `json:"schwabClientCustomerId"`
	SchwabClientCorrelID   string `json:"schwabClientCorrelId"`
	SchwabClientChannel    string `json:"schwabClientChannel"`
	SchwabClientFunctionID string `json:"schwabClientFunctionId"`
}

type streamerClient struct {
	conn *websocket.Conn
	send chan []byte
}
type streamer struct {
	module     *Module
	connectMu  sync.Mutex
	mu         sync.Mutex
	schwab     *websocket.Conn
	info       streamerInfo
	generation uint64
	closed     bool
	clients    map[*streamerClient]struct{}
	pending    map[string]chan error
	services   map[string]bool
	sequence   uint64
}

func newStreamer(module *Module) *streamer {
	return &streamer{module: module, clients: make(map[*streamerClient]struct{}), pending: make(map[string]chan error), services: make(map[string]bool), sequence: 2}
}

var wsUpgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Host, r.Host)
}}

func (s *streamer) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}
func (s *streamer) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.closeLocked()
}
func (s *streamer) closeLocked() {
	s.generation++ // Invalidates a LOGIN that is still in flight, too.
	if s.schwab != nil {
		_ = s.schwab.Close()
		s.schwab = nil
	}
	s.info = streamerInfo{}
	clear(s.services)
	for id, done := range s.pending {
		done <- errors.New("行情连接已断开，请重试")
		delete(s.pending, id)
	}
	for client := range s.clients {
		s.removeLocked(client)
	}
}
func (s *streamer) removeLocked(client *streamerClient) {
	if _, ok := s.clients[client]; ok {
		delete(s.clients, client)
		close(client.send)
	}
	_ = client.conn.Close()
}

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
func (c *streamerClient) writePump() {
	defer c.conn.Close()
	for message := range c.send {
		_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			return
		}
	}
}
func (s *streamer) add(ctx context.Context, client *streamerClient) error {
	if err := s.ensure(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schwab == nil || s.closed {
		return errors.New("行情连接已断开")
	}
	s.clients[client] = struct{}{}
	client.send <- []byte(`{"stream":{"status":"ready"}}`)
	return nil
}
func (s *streamer) remove(client *streamerClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, registered := s.clients[client]; !registered {
		// reset already removed this client. Its delayed read loop must not
		// invalidate a newer connection that is still being established.
		_ = client.conn.Close()
		return
	}
	s.removeLocked(client)
	if len(s.clients) == 0 {
		s.closeLocked()
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

func streamRequest(info streamerInfo, id, service, command string, parameters map[string]string) map[string]any {
	return map[string]any{"requests": []map[string]any{{
		"requestid": id, "service": service, "command": command,
		"SchwabClientCustomerId": info.SchwabClientCustomerID, "SchwabClientCorrelId": info.SchwabClientCorrelID,
		"parameters": parameters,
	}}}
}

type streamResponse struct {
	Response []struct {
		RequestID string `json:"requestid"`
		Service   string `json:"service"`
		Command   string `json:"command"`
		Content   struct {
			Code    int    `json:"code"`
			Message string `json:"msg"`
		} `json:"content"`
	} `json:"response"`
}

// Only publish the connection after LOGIN and account activity subscription are acknowledged.
func (s *streamer) ensure(ctx context.Context) error {
	s.connectMu.Lock()
	defer s.connectMu.Unlock()
	s.mu.Lock()
	connected, closed := s.schwab != nil, s.closed
	s.mu.Unlock()
	if closed {
		return errors.New("投资已关闭")
	}
	if connected {
		return nil
	}
	info, token, generation, err := s.module.streamerCredentials(ctx)
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, info.StreamerSocketURL, http.Header{"Origin": {s.module.schwabAPI}})
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = conn.Close()
		}
	}()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	conn.SetReadLimit(4 << 20)
	deadline := time.Now().Add(15 * time.Second)
	_ = conn.SetReadDeadline(deadline)
	_ = conn.SetWriteDeadline(deadline)
	if err := conn.WriteJSON(streamRequest(info, "1", "ADMIN", "LOGIN", map[string]string{
		"Authorization": token, "SchwabClientChannel": firstNonEmpty(info.SchwabClientChannel, "N9"),
		"SchwabClientFunctionId": firstNonEmpty(info.SchwabClientFunctionID, "APIAPP"),
	})); err != nil {
		return err
	}
	if err := awaitStreamResponse(conn, "1"); err != nil {
		return err
	}
	if err := conn.WriteJSON(streamRequest(info, "2", "ACCT_ACTIVITY", "SUBS", map[string]string{"keys": "Account Activity", "fields": "0,1,2,3"})); err != nil {
		return err
	}
	if err := awaitStreamResponse(conn, "2"); err != nil {
		return err
	}
	if !stop() || ctx.Err() != nil {
		return ctx.Err()
	}
	_ = conn.SetReadDeadline(time.Time{})
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.generation != generation {
		return errors.New("Schwab 配置已变更，请重新连接")
	}
	s.schwab, s.info = conn, info
	s.module.logger.Info("Schwab stream connected")
	keep = true
	go s.readSchwab(conn)
	return nil
}
func awaitStreamResponse(conn *websocket.Conn, id string) error {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var message streamResponse
		if err := json.Unmarshal(raw, &message); err != nil {
			return err
		}
		for _, response := range message.Response {
			if response.RequestID == id {
				if response.Content.Code != 0 {
					return fmt.Errorf("Schwab %s 失败（%d）", response.Command, response.Content.Code)
				}
				return nil
			}
		}
	}
}

func (s *streamer) sendCommand(ctx context.Context, service, keys, fields string) error {
	// Serialise initial SUBS and subsequent ADD requests for the shared stream.
	s.connectMu.Lock()
	defer s.connectMu.Unlock()
	s.mu.Lock()
	if s.schwab == nil {
		s.mu.Unlock()
		return errors.New("请等待行情连接就绪")
	}
	conn := s.schwab
	command := "ADD"
	if !s.services[service] {
		command = "SUBS"
	}
	if fields == "" {
		fields = "0,1"
	}
	s.sequence++
	id := strconv.FormatUint(s.sequence, 10)
	done := make(chan error, 1)
	s.pending[id] = done
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := conn.WriteJSON(streamRequest(s.info, id, service, command, map[string]string{"keys": keys, "fields": fields}))
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	if err == nil {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case err = <-done:
		case <-ctx.Done():
			err = ctx.Err()
		case <-timer.C:
			err = errors.New("Schwab 订阅确认超时")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.schwab != conn {
		return errors.New("行情连接已变更，请重试")
	}
	if err != nil {
		s.closeLocked()
		return err
	}
	s.services[service] = true
	return nil
}
func (s *streamer) readSchwab(conn *websocket.Conn) {
	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.schwab == conn {
			s.module.logger.Warn("Schwab stream disconnected; clients must reconnect")
			s.closeLocked()
		}
		_ = conn.Close()
	}()
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var message streamResponse
		_ = json.Unmarshal(raw, &message)
		s.mu.Lock()
		if s.schwab != conn {
			s.mu.Unlock()
			return
		}
		for _, response := range message.Response {
			if done, ok := s.pending[response.RequestID]; ok {
				var result error
				if response.Content.Code != 0 {
					result = fmt.Errorf("Schwab %s 失败（%d）", response.Command, response.Content.Code)
				}
				done <- result
				delete(s.pending, response.RequestID)
			}
		}
		hadClients := len(s.clients) > 0
		for client := range s.clients {
			select {
			case client.send <- raw:
			default:
				s.module.logger.Warn("slow stream client disconnected")
				s.removeLocked(client) // Reconnect and reconcile instead of silently losing order events.
			}
		}
		if hadClients && len(s.clients) == 0 {
			s.closeLocked()
		}
		s.mu.Unlock()
	}
}
func (m *Module) streamerCredentials(ctx context.Context) (streamerInfo, string, uint64, error) {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	if err := m.refreshAccessTokenLocked(ctx); err != nil {
		return streamerInfo{}, "", 0, err
	}
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return streamerInfo{}, "", 0, err
	}
	token := rec.AccessToken
	if token == "" {
		return streamerInfo{}, "", 0, errors.New("尚未连接 Schwab")
	}
	m.streamer.mu.Lock()
	generation := m.streamer.generation
	m.streamer.mu.Unlock()
	if rec.StreamerInfo != "" {
		var cached streamerInfo
		if json.Unmarshal([]byte(rec.StreamerInfo), &cached) == nil && cached.StreamerSocketURL != "" {
			return cached, token, generation, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(m.schwabAPI, "/")+"/trader/v1/userPreference", nil)
	if err != nil {
		return streamerInfo{}, "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return streamerInfo{}, "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return streamerInfo{}, "", 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return streamerInfo{}, "", 0, errors.New(trimErrorBody(body))
	}
	var parsed struct {
		StreamerInfo []streamerInfo `json:"streamerInfo"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return streamerInfo{}, "", 0, err
	}
	if len(parsed.StreamerInfo) == 0 || parsed.StreamerInfo[0].StreamerSocketURL == "" {
		return streamerInfo{}, "", 0, errors.New("userPreference 未返回 streamerInfo")
	}
	info := parsed.StreamerInfo[0]
	raw, _ := json.Marshal(info)
	rec.StreamerInfo = string(raw)
	if err := m.storeSchwab(ctx, rec); err != nil {
		return streamerInfo{}, "", 0, err
	}
	return info, token, generation, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
