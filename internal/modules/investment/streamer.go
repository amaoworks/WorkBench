package investment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
