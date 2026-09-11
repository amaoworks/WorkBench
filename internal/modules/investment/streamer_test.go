package investment

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testStreamRequest struct {
	ID         string            `json:"requestid"`
	Service    string            `json:"service"`
	Command    string            `json:"command"`
	Parameters map[string]string `json:"parameters"`
}
type testStreamPeer struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (p *testStreamPeer) reply(request testStreamRequest, code int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.conn.WriteJSON(map[string]any{"response": []any{map[string]any{"requestid": request.ID, "service": request.Service, "command": request.Command, "content": map[string]any{"code": code}}}})
}

type streamFixture struct {
	module     *Module
	browserURL string
	peers      chan *testStreamPeer
	commands   chan testStreamRequest
}

func newStreamFixture(t *testing.T, beforeAck func(testStreamRequest) int) *streamFixture {
	t.Helper()
	f := &streamFixture{module: openInvestmentModule(t), peers: make(chan *testStreamPeer, 8), commands: make(chan testStreamRequest, 32)}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		peer := &testStreamPeer{conn: conn}
		f.peers <- peer
		for {
			var payload struct {
				Requests []testStreamRequest `json:"requests"`
			}
			if conn.ReadJSON(&payload) != nil {
				return
			}
			for _, request := range payload.Requests {
				f.commands <- request
				code := 0
				if beforeAck != nil {
					code = beforeAck(request)
				}
				if peer.reply(request, code) != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(upstream.Close)
	info, _ := json.Marshal(streamerInfo{StreamerSocketURL: "ws" + strings.TrimPrefix(upstream.URL, "http"), SchwabClientCustomerID: "customer-A", SchwabClientCorrelID: "correlation"})
	seedSchwab(t, f.module, schwabRecord{AppKey: "key-A", AppSecret: "secret-A", CallbackURL: "https://127.0.0.1/oauth/schwab", AccessToken: "token-A", RefreshToken: "refresh-A", TokenExpiresAt: time.Now().Add(time.Hour), StreamerInfo: string(info)})
	browserServer := httptest.NewServer(http.HandlerFunc(f.module.serveStreamer))
	t.Cleanup(browserServer.Close)
	f.browserURL = "ws" + strings.TrimPrefix(browserServer.URL, "http")
	return f
}
func (f *streamFixture) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	client, _, err := websocket.DefaultDialer.Dial(f.browserURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	_ = client.SetReadDeadline(time.Now().Add(3 * time.Second))
	return client
}
func readReady(t *testing.T, client *websocket.Conn) {
	t.Helper()
	_, raw, err := client.ReadMessage()
	if err != nil || !strings.Contains(string(raw), `"ready"`) {
		t.Fatalf("expected ready, got %s %v", raw, err)
	}
}
func readClosed(t *testing.T, client *websocket.Conn) {
	t.Helper()
	_, raw, err := client.ReadMessage()
	if err == nil {
		t.Fatalf("unexpected frame instead of closed connection: %s", raw)
	}
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		t.Fatal("browser connection left hanging")
	}
}
func nextPeer(t *testing.T, f *streamFixture) *testStreamPeer {
	t.Helper()
	select {
	case peer := <-f.peers:
		return peer
	case <-time.After(3 * time.Second):
		t.Fatal("missing upstream connection")
		return nil
	}
}

func TestStreamerDisconnectClosesBrowserAndReconnects(t *testing.T) {
	f := newStreamFixture(t, nil)
	client := f.dial(t)
	readReady(t, client)
	peer := nextPeer(t, f)
	_ = peer.conn.Close()
	readClosed(t, client)
	// The browser's reconnect starts a new, authenticated connection.
	next := f.dial(t)
	readReady(t, next)
	secondPeer := nextPeer(t, f)
	if secondPeer == peer {
		t.Fatal("reused disconnected peer")
	}
}

func TestStreamerCredentialsChangeClosesAuthenticatedConnection(t *testing.T) {
	for _, input := range []string{
		`{"appKey":"key-B","appSecret":"secret-B","callbackUrl":"https://127.0.0.1/oauth/schwab"}`,
		`{"appKey":"key-A","appSecret":"secret-B","callbackUrl":"https://127.0.0.1/oauth/schwab"}`,
		`{"appKey":"key-A","appSecret":"secret-A","callbackUrl":"https://127.0.0.1:8080/oauth/schwab"}`,
	} {
		t.Run(input, func(t *testing.T) {
			f := newStreamFixture(t, nil)
			client := f.dial(t)
			readReady(t, client)
			got := httptest.NewRecorder()
			f.module.saveSchwab(got, httptest.NewRequest("PUT", "/", strings.NewReader(input)))
			if got.Code != 200 {
				t.Fatal(got.Body.String())
			}
			readClosed(t, client)
			f.module.streamer.mu.Lock()
			defer f.module.streamer.mu.Unlock()
			if f.module.streamer.schwab != nil || len(f.module.streamer.clients) != 0 {
				t.Fatal("old stream survived credentials change")
			}
		})
	}
}

func TestStreamerDoesNotInstallOldLoginAfterCredentialsChange(t *testing.T) {
	loginStarted, releaseLogin := make(chan struct{}), make(chan struct{})
	f := newStreamFixture(t, func(request testStreamRequest) int {
		if request.Command == "LOGIN" {
			close(loginStarted)
			<-releaseLogin
		}
		return 0
	})
	client := f.dial(t)
	select {
	case <-loginStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("missing LOGIN")
	}
	got := httptest.NewRecorder()
	f.module.saveSchwab(got, httptest.NewRequest("PUT", "/", strings.NewReader(`{"appKey":"key-B","appSecret":"secret-B","callbackUrl":"https://127.0.0.1/oauth/schwab"}`)))
	close(releaseLogin)
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	readClosed(t, client)
	f.module.streamer.mu.Lock()
	defer f.module.streamer.mu.Unlock()
	if f.module.streamer.schwab != nil {
		t.Fatal("old LOGIN installed after reset")
	}
}

func TestStreamerRejectsFailedLogin(t *testing.T) {
	f := newStreamFixture(t, func(request testStreamRequest) int {
		if request.Command == "LOGIN" {
			return 3
		}
		return 0
	})
	client := f.dial(t)
	readClosed(t, client)
}

func TestStreamerWaitsForSubscriptionAckAndUsesSubsThenAdd(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := newStreamFixture(t, func(request testStreamRequest) int {
		if request.Service == "LEVELONE_EQUITIES" && request.Command == "SUBS" {
			close(started)
			<-release
		}
		return 0
	})
	client := f.dial(t)
	readReady(t, client)
	done := make(chan int, 1)
	send := func() int {
		got := httptest.NewRecorder()
		f.module.streamerCommand(got, httptest.NewRequest("POST", "/", strings.NewReader(`{"service":"LEVELONE_EQUITIES","command":"ADD","keys":"AAPL","fields":"0,1,3"}`)))
		return got.Code
	}
	go func() { done <- send() }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("missing command")
	}
	select {
	case <-done:
		t.Fatal("reported success before acknowledgment")
	default:
	}
	close(release)
	if code := <-done; code != 200 {
		t.Fatalf("command status %d", code)
	}
	if code := send(); code != 200 {
		t.Fatalf("second command status %d", code)
	}
	var commands []string
	for len(f.commands) > 0 {
		req := <-f.commands
		if req.Service == "LEVELONE_EQUITIES" {
			commands = append(commands, req.Command)
		}
	}
	if strings.Join(commands, ",") != "SUBS,ADD" {
		t.Fatalf("commands=%v", commands)
	}
}

func TestStreamerCommandRejectionIsNotSuccess(t *testing.T) {
	f := newStreamFixture(t, func(request testStreamRequest) int {
		if request.Service == "LEVELONE_EQUITIES" {
			return 11
		}
		return 0
	})
	client := f.dial(t)
	readReady(t, client)
	got := httptest.NewRecorder()
	f.module.streamerCommand(got, httptest.NewRequest("POST", "/", strings.NewReader(`{"service":"LEVELONE_EQUITIES","command":"ADD","keys":"AAPL"}`)))
	if got.Code != 503 {
		t.Fatalf("upstream rejection returned %d", got.Code)
	}
}

func TestStreamerShutdownClosesConnections(t *testing.T) {
	f := newStreamFixture(t, nil)
	client := f.dial(t)
	readReady(t, client)
	f.module.Close()
	readClosed(t, client)
	f.module.Close() // idempotent, including already upgraded connections
}

func TestOAuthReplacementClosesPreviousAccountStream(t *testing.T) {
	f := newStreamFixture(t, nil)
	client := f.dial(t)
	readReady(t, client)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/oauth/token" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "token-B", "refresh_token": "refresh-B", "expires_in": 1800})
	}))
	defer upstream.Close()
	f.module.schwabAPI = upstream.URL
	f.module.tokenMu.Lock()
	rec, err := f.module.loadSchwab(context.Background())
	if err == nil {
		rec.OAuthState = "state"
		rec.OAuthStateExpiresAt = time.Now().Add(time.Minute)
		err = f.module.storeSchwab(context.Background(), rec)
	}
	f.module.tokenMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	got := httptest.NewRecorder()
	f.module.oauthCallback(got, httptest.NewRequest("GET", "/oauth/schwab?code=code&state=state", nil))
	if got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	readClosed(t, client)
}
