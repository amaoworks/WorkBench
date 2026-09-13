package modules

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"workbench/internal/contracts"
)

type fakeRemote struct {
	mu            sync.Mutex
	id            contracts.ModuleID
	name          string
	version       string
	token         string
	instanceID    string
	generation    int64
	enabled       bool
	registration  string
	config        map[string]any
	counter       int
	running       bool
	delay         time.Duration
	hold          chan struct{}
	stale         bool
	fault         bool
	pages         []contracts.ExternalPage
	settingsEntry string
	protocol      int
	capabilities  []string
	seenCookie    string
	seenAuth      string
	redirect      string
	wsClients     []*websocket.Conn
	stop          chan struct{}
	server        *httptest.Server
	down          atomic.Bool
	wsHold        chan struct{}
	wsHeld        atomic.Bool
}

func newFakeRemote(token string) *fakeRemote {
	return startFakeRemote(token, nil)
}

func startFakeRemote(token string, connState func(net.Conn, http.ConnState)) *fakeRemote {
	f := &fakeRemote{
		id: "demo_external", name: "外部示例", version: "0.1.0", token: token,
		instanceID: "instance-1", config: map[string]any{"label": "hello"},
		pages:         []contracts.ExternalPage{{Key: "demo_external.overview", Label: "外部示例", Entry: "/ui/index.html", Order: 30}},
		settingsEntry: "/settings/index.html",
		protocol:      1,
		capabilities:  []string{"pages", "settings", "lifecycle"},
		stop:          make(chan struct{}),
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	if connState != nil {
		server.Config.ConnState = connState
	}
	server.Start()
	f.server = server
	return f
}

func (f *fakeRemote) URL() string { return f.server.URL }

func (f *fakeRemote) Close() {
	close(f.stop)
	f.mu.Lock()
	for _, conn := range f.wsClients {
		_ = conn.Close()
	}
	f.mu.Unlock()
	f.server.Close()
}

func (f *fakeRemote) setDown(down bool) { f.down.Store(down) }

func (f *fakeRemote) serve(w http.ResponseWriter, r *http.Request) {
	if f.down.Load() {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
			}
		}
		return
	}
	f.mu.Lock()
	f.seenCookie = r.Header.Get("Cookie")
	f.seenAuth = r.Header.Get("Authorization")
	delay, hold, fault := f.delay, f.hold, f.fault
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		http.Error(w, `{"code":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if delay > 0 && strings.HasPrefix(r.URL.Path, "/_workbench/state") {
		time.Sleep(delay)
	}
	if hold != nil && r.URL.Path == "/_workbench/state" {
		<-hold
	}
	if fault && r.URL.Path == "/_workbench/state" {
		http.Error(w, `{"code":"fault"}`, http.StatusInternalServerError)
		return
	}
	switch {
	case r.URL.Path == "/_workbench/manifest" && r.Method == http.MethodGet:
		f.writeJSON(w, f.manifest())
	case r.URL.Path == "/_workbench/status" && r.Method == http.MethodGet:
		f.writeJSON(w, f.status())
	case r.URL.Path == "/_workbench/state" && r.Method == http.MethodPut:
		f.handleState(w, r)
	case r.URL.Path == "/_workbench/config" && r.Method == http.MethodGet:
		f.writeJSON(w, f.currentConfig())
	case r.URL.Path == "/_workbench/config" && r.Method == http.MethodPut:
		f.handleConfig(w, r)
	case strings.HasPrefix(r.URL.Path, "/ui/"):
		if f.redirect != "" {
			w.Header().Set("Location", f.redirect)
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><body>ui:"+r.URL.Path+"</body></html>")
	case strings.HasPrefix(r.URL.Path, "/settings/"):
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><body>settings</body></html>")
	case r.URL.Path == "/api/counter":
		f.writeJSON(w, f.counterState())
	case r.URL.Path == "/api/stream":
		f.handleWS(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeRemote) manifest() contracts.ExternalManifest {
	f.mu.Lock()
	defer f.mu.Unlock()
	settings := &contracts.ExternalSettingsEntry{Entry: f.settingsEntry}
	return contracts.ExternalManifest{
		ID: f.id, Name: f.name, Version: f.version, ProtocolVersion: f.protocol,
		Icon: "module.default", Capabilities: append([]string(nil), f.capabilities...),
		Pages: append([]contracts.ExternalPage(nil), f.pages...), Settings: settings,
	}
}

func (f *fakeRemote) status() contracts.ExternalStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	health := contracts.HealthReady
	if !f.enabled {
		health = contracts.HealthReady
	}
	generation := f.generation
	if f.stale && generation > 0 {
		generation--
	}
	return contracts.ExternalStatus{
		RegistrationID: f.registration, Generation: generation, Enabled: f.enabled,
		InstanceID: f.instanceID, Health: health,
	}
}

func (f *fakeRemote) handleState(w http.ResponseWriter, r *http.Request) {
	var input contracts.ExternalControlState
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, `{"code":"invalid"}`, http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	if f.registration != "" && input.RegistrationID != f.registration {
		if f.enabled {
			f.mu.Unlock()
			http.Error(w, `{"code":"registration"}`, http.StatusConflict)
			return
		}
		f.registration = input.RegistrationID
		f.generation = input.Generation
		f.enabled = input.Enabled
		f.running = input.Enabled
		f.mu.Unlock()
		f.writeJSON(w, f.status())
		return
	}
	if input.Generation < f.generation {
		f.mu.Unlock()
		http.Error(w, `{"code":"stale_generation"}`, http.StatusConflict)
		return
	}
	if input.Generation == f.generation && input.Enabled != f.enabled {
		f.mu.Unlock()
		http.Error(w, `{"code":"generation_conflict"}`, http.StatusConflict)
		return
	}
	f.registration = input.RegistrationID
	f.generation = input.Generation
	f.enabled = input.Enabled
	f.running = input.Enabled
	if input.Enabled {
		f.counter++
	}
	f.mu.Unlock()
	f.writeJSON(w, f.status())
}

func (f *fakeRemote) handleConfig(w http.ResponseWriter, r *http.Request) {
	var payload map[string]any
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, `{"code":"invalid"}`, http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	if f.config == nil {
		f.config = map[string]any{}
	}
	for key, value := range payload {
		f.config[key] = value
	}
	f.mu.Unlock()
	f.writeJSON(w, f.currentConfig())
}

func (f *fakeRemote) currentConfig() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]any{}
	for key, value := range f.config {
		out[key] = value
	}
	return out
}

func (f *fakeRemote) counterState() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return map[string]any{"value": f.counter, "running": f.running}
}

func (f *fakeRemote) handleWS(w http.ResponseWriter, r *http.Request) {
	if hold := f.wsHold; hold != nil {
		f.wsHeld.Store(true)
		<-hold
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.wsClients = append(f.wsClients, conn)
	f.mu.Unlock()
	_ = conn.WriteJSON(f.counterState())
	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

func (f *fakeRemote) writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (f *fakeRemote) setDelay(d time.Duration) {
	f.mu.Lock()
	f.delay = d
	f.mu.Unlock()
}

func (f *fakeRemote) runningNow() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}
