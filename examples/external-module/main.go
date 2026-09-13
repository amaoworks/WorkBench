package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed ui/index.html settings/index.html
var assets embed.FS

type persisted struct {
	RegistrationID string         `json:"registrationId"`
	Generation     int64          `json:"generation"`
	Enabled        bool           `json:"enabled"`
	Config         map[string]any `json:"config"`
}

type module struct {
	mu             sync.Mutex
	id             string
	name           string
	version        string
	token          string
	title          string
	instanceID     string
	registrationID string
	generation     int64
	enabled        bool
	config         map[string]any
	counter        int
	dataFile       string
	delay          time.Duration
	stale          bool
	fault          bool
	stop           chan struct{}
	clients        map[*websocket.Conn]struct{}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	listen := flag.String("listen", "127.0.0.1:8091", "HTTP listen address")
	token := flag.String("token", "example-token", "service token expected from Workbench")
	data := flag.String("data", filepath.Join(os.TempDir(), "workbench-example-external.json"), "persisted state path")
	id := flag.String("id", "demo_external", "module id")
	name := flag.String("name", "外部示例", "module name")
	delay := flag.Duration("delay-state", 0, "delay PUT /_workbench/state (for timeout tests)")
	stale := flag.Bool("stale-generation", false, "return generation-1 from status/state")
	fault := flag.Bool("fault", false, "return 500 from PUT /_workbench/state")
	title := flag.String("title", "外部示例", "page title marker used by attach tests")
	flag.Parse()

	mod := &module{
		id: *id, name: *name, version: "0.1.0", token: *token, title: *title,
		instanceID: newID(), config: map[string]any{"label": "示例模块"},
		dataFile: *data, delay: *delay, stale: *stale, fault: *fault,
		stop: make(chan struct{}), clients: make(map[*websocket.Conn]struct{}),
	}
	if err := mod.load(); err != nil {
		return err
	}
	go mod.counterLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("/_workbench/manifest", mod.auth(mod.manifest))
	mux.HandleFunc("/_workbench/status", mod.auth(mod.status))
	mux.HandleFunc("/_workbench/state", mod.auth(mod.state))
	mux.HandleFunc("/_workbench/config", mod.auth(mod.configHandler))
	mux.HandleFunc("/ui/", mod.auth(mod.ui))
	mux.HandleFunc("/settings/", mod.auth(mod.settings))
	mux.HandleFunc("/api/counter", mod.auth(mod.counterHandler))
	mux.HandleFunc("/api/stream", mod.auth(mod.stream))

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Printf("listening on %s\n", listener.Addr().String())
	err = server.Serve(listener)
	close(mod.stop)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (m *module) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != m.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"code": "unauthorized", "message": "invalid service token"})
			return
		}
		next(w, r)
	}
}

func (m *module) manifest(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"id": m.id, "name": m.name, "version": m.version, "protocolVersion": 1,
		"icon": "module.default", "capabilities": []string{"pages", "settings", "lifecycle"},
		"pages":    []map[string]any{{"key": m.id + ".overview", "label": m.name, "entry": "/ui/index.html", "order": 30}},
		"settings": map[string]string{"entry": "/settings/index.html"},
	})
}

func (m *module) snapshot() (registration string, generation int64, enabled bool, counter int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	generation = m.generation
	if m.stale && generation > 0 {
		generation--
	}
	return m.registrationID, generation, m.enabled, m.counter
}

func (m *module) status(w http.ResponseWriter, _ *http.Request) {
	registration, generation, enabled, _ := m.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"registrationId": registration, "generation": generation, "enabled": enabled,
		"instanceId": m.instanceID, "health": "ready",
	})
}

func (m *module) state(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"code": "method_not_allowed"})
		return
	}
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	if m.fault {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "fault"})
		return
	}
	var input struct {
		RegistrationID string `json:"registrationId"`
		Generation     int64  `json:"generation"`
		Enabled        bool   `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
		return
	}
	if err := m.applyControlState(input.RegistrationID, input.Generation, input.Enabled); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": err.Error()})
		return
	}
	_ = m.save()
	m.broadcast()
	m.status(w, r)
}

func (m *module) applyControlState(registrationID string, generation int64, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.registrationID != "" && registrationID != m.registrationID {
		if m.enabled {
			return errRegistrationConflict
		}
		m.registrationID = registrationID
		m.generation = generation
		m.enabled = enabled
		return nil
	}
	if m.registrationID == "" {
		m.registrationID = registrationID
	}
	if generation < m.generation {
		return errStaleGeneration
	}
	if generation == m.generation && enabled != m.enabled {
		return errGenerationConflict
	}
	m.generation = generation
	m.enabled = enabled
	return nil
}

var (
	errRegistrationConflict = errors.New("registration_conflict")
	errStaleGeneration      = errors.New("stale_generation")
	errGenerationConflict   = errors.New("generation_conflict")
)

func (m *module) configHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		m.mu.Lock()
		cfg := cloneMap(m.config)
		m.mu.Unlock()
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPut:
		var payload map[string]any
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request"})
			return
		}
		m.mu.Lock()
		if m.config == nil {
			m.config = map[string]any{}
		}
		if label, ok := payload["label"].(string); ok {
			m.config["label"] = label
		}
		cfg := cloneMap(m.config)
		m.mu.Unlock()
		_ = m.save()
		writeJSON(w, http.StatusOK, cfg)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"code": "method_not_allowed"})
	}
}

func (m *module) ui(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/ui/index.html" && r.URL.Path != "/ui/" {
		http.NotFound(w, r)
		return
	}
	raw, err := assets.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, "ui missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(bytesReplaceTitle(raw, m.title))
}

func (m *module) settings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"code": "method_not_allowed"})
		return
	}
	raw, err := assets.ReadFile("settings/index.html")
	if err != nil {
		http.Error(w, "settings missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(raw)
}

func (m *module) counterHandler(w http.ResponseWriter, _ *http.Request) {
	_, _, enabled, counter := m.snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"value": counter, "running": enabled})
}

func (m *module) stream(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	m.mu.Lock()
	m.clients[conn] = struct{}{}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.clients, conn)
		m.mu.Unlock()
		_ = conn.Close()
	}()
	_, _, enabled, counter := m.snapshot()
	_ = conn.WriteJSON(map[string]any{"value": counter, "running": enabled})
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (m *module) counterLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.mu.Lock()
			if m.enabled {
				m.counter++
			}
			m.mu.Unlock()
			m.broadcast()
		}
	}
}

func (m *module) broadcast() {
	_, _, enabled, counter := m.snapshot()
	payload := map[string]any{"value": counter, "running": enabled}
	m.mu.Lock()
	clients := make([]*websocket.Conn, 0, len(m.clients))
	for conn := range m.clients {
		clients = append(clients, conn)
	}
	m.mu.Unlock()
	for _, conn := range clients {
		_ = conn.WriteJSON(payload)
	}
}

func (m *module) load() error {
	raw, err := os.ReadFile(m.dataFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var saved persisted
	if err := json.Unmarshal(raw, &saved); err != nil {
		return err
	}
	m.mu.Lock()
	m.registrationID = saved.RegistrationID
	m.generation = saved.Generation
	m.enabled = saved.Enabled
	if saved.Config != nil {
		m.config = saved.Config
	}
	m.mu.Unlock()
	return nil
}

func (m *module) save() error {
	m.mu.Lock()
	saved := persisted{RegistrationID: m.registrationID, Generation: m.generation, Enabled: m.enabled, Config: cloneMap(m.config)}
	m.mu.Unlock()
	raw, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.dataFile), 0o700); err != nil {
		return err
	}
	return os.WriteFile(m.dataFile, raw, 0o600)
}

func bytesReplaceTitle(raw []byte, title string) []byte {
	return []byte(strings.Replace(string(raw), "外部示例", title, 1))
}

func cloneMap(in map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func newID() string {
	var value [8]byte
	_, _ = rand.Read(value[:])
	return hex.EncodeToString(value[:])
}
