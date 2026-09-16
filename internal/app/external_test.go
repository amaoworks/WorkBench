package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"workbench/internal/foundation/modules"
)

type appFakeRemote struct {
	mu           sync.Mutex
	token        string
	id           string
	blocked      atomic.Bool
	delay        time.Duration
	enabled      bool
	gen          int64
	config       string
	registration string
	server       *httptest.Server
}

func newAppFake(t *testing.T, token string) *appFakeRemote {
	t.Helper()
	f := &appFakeRemote{token: token, id: "demo_external", config: `{"label":"init"}`}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *appFakeRemote) serve(w http.ResponseWriter, r *http.Request) {
	if f.blocked.Load() && r.URL.Path != "/_workbench/manifest" {
		time.Sleep(3 * time.Second)
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.mu.Lock()
	delay := f.delay
	id := f.id
	f.mu.Unlock()
	if delay > 0 && r.URL.Path == "/_workbench/state" {
		time.Sleep(delay)
	}
	switch r.URL.Path {
	case "/_workbench/manifest":
		_, _ = io.WriteString(w, `{
			"id":"`+id+`","name":"外部示例","version":"0.1.0","protocolVersion":1,
			"icon":"module.default","capabilities":["pages","settings","lifecycle"],
			"pages":[{"key":"`+id+`.overview","label":"外部示例","entry":"/ui/index.html","order":30}],
			"settings":{"entry":"/settings/index.html"}
		}`)
	case "/_workbench/status":
		f.mu.Lock()
		enabled := "false"
		if f.enabled {
			enabled = "true"
		}
		reg := f.registration
		gen := f.gen
		f.mu.Unlock()
		if reg == "" {
			reg = "pending"
		}
		_, _ = io.WriteString(w, `{"registrationId":"`+reg+`","generation":`+itoa(gen)+`,"enabled":`+enabled+`,"instanceId":"i1","health":"ready"}`)
	case "/_workbench/state":
		var body struct {
			RegistrationID string `json:"registrationId"`
			Generation     int64  `json:"generation"`
			Enabled        bool   `json:"enabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.gen, f.enabled, f.registration = body.Generation, body.Enabled, body.RegistrationID
		enabled := "false"
		if f.enabled {
			enabled = "true"
		}
		reg := f.registration
		gen := f.gen
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"registrationId":"`+reg+`","generation":`+itoa(gen)+`,"enabled":`+enabled+`,"instanceId":"i1","health":"ready"}`)
	case "/_workbench/config":
		if r.Method == http.MethodPut {
			raw, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			if len(raw) > 0 {
				f.config = string(raw)
			}
			cfg := f.config
			f.mu.Unlock()
			_, _ = io.WriteString(w, cfg)
			return
		}
		f.mu.Lock()
		cfg := f.config
		f.mu.Unlock()
		_, _ = io.WriteString(w, cfg)
	case "/ui/index.html":
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html>ui-ok</html>")
	case "/settings/index.html":
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html>settings-ok</html>")
	case "/api/counter":
		io.WriteString(w, `{"value":1,"running":true}`)
	default:
		http.NotFound(w, r)
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestExternalModuleAttachEnableConfigAndRestart(t *testing.T) {
	application := newTestApp(t)
	application.registry.SetTimeouts(modules.Timeouts{Control: 300 * time.Millisecond, ProbeInterval: time.Hour, RetryMax: time.Second, MaxProbes: 2})
	token, cookies := csrf(t, application.Handler())
	remote := newAppFake(t, "attach-secret-token")

	created, err := application.registry.Attach(context.Background(), modules.ConnectionInput{BaseURL: remote.server.URL, ServiceToken: "attach-secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	createdJSON, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(createdJSON, []byte("attach-secret-token")) {
		t.Fatal("token leaked on attach")
	}

	remote.blocked.Store(true)
	start := time.Now()
	listed := request(t, application.Handler(), http.MethodGet, "/api/modules", nil, "", cookies)
	if time.Since(start) > 400*time.Millisecond {
		t.Fatal("GET /api/modules blocked on remote")
	}
	if listed.Code != 200 || !bytes.Contains(listed.Body.Bytes(), []byte(`"kind":"external"`)) || !bytes.Contains(listed.Body.Bytes(), []byte(`/apps/demo_external/overview`)) {
		t.Fatalf("list = %s", listed.Body.String())
	}
	remote.blocked.Store(false)

	if got := request(t, application.Handler(), http.MethodPut, "/api/modules/demo_external/enabled", []byte(`{"enabled":true}`), token, cookies); got.Code != 200 {
		t.Fatalf("enable = %d %s", got.Code, got.Body.String())
	}
	if got := request(t, application.Handler(), http.MethodGet, "/modules/demo_external/ui/index.html", nil, "", cookies); got.Code != 200 || !bytes.Contains(got.Body.Bytes(), []byte("ui-ok")) {
		t.Fatalf("ui = %d %s", got.Code, got.Body.String())
	}

	if got := request(t, application.Handler(), http.MethodPut, "/api/modules/demo_external/enabled", []byte(`{"enabled":false}`), token, cookies); got.Code != 200 {
		t.Fatalf("disable = %d %s", got.Code, got.Body.String())
	}
	if got := request(t, application.Handler(), http.MethodGet, "/api/modules/demo_external/proxy/counter", nil, "", cookies); got.Code != 503 || !bytes.Contains(got.Body.Bytes(), []byte("module_disabled")) {
		t.Fatalf("disabled api = %d %s", got.Code, got.Body.String())
	}
	if got := request(t, application.Handler(), http.MethodGet, "/modules/demo_external/settings/index.html", nil, "", cookies); got.Code != 200 {
		t.Fatalf("settings while disabled = %d %s", got.Code, got.Body.String())
	}
	if got := request(t, application.Handler(), http.MethodPut, "/api/modules/demo_external/config", []byte(`{"label":"from-host"}`), token, cookies); got.Code != 200 || !bytes.Contains(got.Body.Bytes(), []byte("from-host")) {
		t.Fatalf("config = %d %s", got.Code, got.Body.String())
	}

	cfg := application.config
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	remote.server.Close()
	restarted, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	token, cookies = csrf(t, restarted.Handler())
	ready := request(t, restarted.Handler(), http.MethodGet, "/health/ready", nil, "", nil)
	if ready.Code != 200 {
		t.Fatalf("ready = %d", ready.Code)
	}
	if got := request(t, restarted.Handler(), http.MethodGet, "/api/modules/todo/tasks", nil, "", cookies); got.Code != 200 {
		t.Fatalf("todo after restart = %d", got.Code)
	}
	listed = request(t, restarted.Handler(), http.MethodGet, "/api/modules", nil, "", cookies)
	if !bytes.Contains(listed.Body.Bytes(), []byte(`"enabled":false`)) || !bytes.Contains(listed.Body.Bytes(), []byte("demo_external")) {
		t.Fatalf("persist = %s", listed.Body.String())
	}
	if got := request(t, restarted.Handler(), http.MethodGet, "/api/modules/demo_external/proxy/counter", nil, "", cookies); got.Code != 503 {
		t.Fatalf("stale health opened proxy = %d %s", got.Code, got.Body.String())
	}
}

func TestExternalAuthCSRFAndBuiltinGuard(t *testing.T) {
	application := newTestApp(t)
	remote := newAppFake(t, "tok")
	if got := request(t, application.Handler(), http.MethodPost, "/api/modules/external",
		[]byte(`{"baseUrl":"`+remote.server.URL+`","serviceToken":"tok"}`), "", nil); got.Code == http.StatusCreated {
		t.Fatal("attach without CSRF succeeded")
	}
	token, cookies := csrf(t, application.Handler())
	if got := request(t, application.Handler(), http.MethodPost, "/api/modules/external",
		[]byte(`{"baseUrl":"`+remote.server.URL+`","serviceToken":"tok"}`), token, cookies); got.Code != http.StatusNotFound {
		t.Fatalf("attach with CSRF status = %d %s", got.Code, got.Body.String())
	}
	listed := request(t, application.Handler(), http.MethodGet, "/api/modules", nil, "", cookies)
	if bytes.Contains(listed.Body.Bytes(), []byte("demo_external")) {
		t.Fatalf("valid CSRF POST added module: %s", listed.Body.String())
	}
	if got := request(t, application.Handler(), http.MethodPut, "/api/modules/todo/connection", []byte(`{"baseUrl":"http://127.0.0.1:9","serviceToken":"x"}`), token, cookies); got.Code == 200 {
		t.Fatal("connection update on builtin succeeded")
	}
	if got := request(t, application.Handler(), http.MethodDelete, "/api/modules/external/todo", nil, token, cookies); got.Code == http.StatusNoContent {
		t.Fatal("unregister builtin succeeded")
	}
}

func TestExternalEnablePendingDoesNotLookLikeSuccess(t *testing.T) {
	application := newTestApp(t)
	application.registry.SetTimeouts(modules.Timeouts{Control: 80 * time.Millisecond, ProbeInterval: time.Hour, RetryMax: 200 * time.Millisecond, MaxProbes: 2})
	token, cookies := csrf(t, application.Handler())
	remote := newAppFake(t, "tok")
	remote.mu.Lock()
	remote.delay = time.Second
	remote.mu.Unlock()
	created, err := application.registry.Attach(context.Background(), modules.ConnectionInput{BaseURL: remote.server.URL, ServiceToken: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	createdJSON, err := json.Marshal(created)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(createdJSON, []byte("tok")) {
		t.Fatal("token leaked on attach")
	}
	got := request(t, application.Handler(), http.MethodPut, "/api/modules/demo_external/enabled", []byte(`{"enabled":true}`), token, cookies)
	if got.Code != http.StatusAccepted {
		t.Fatalf("pending enable status = %d %s", got.Code, got.Body.String())
	}
	if !bytes.Contains(got.Body.Bytes(), []byte(`"pending":true`)) {
		t.Fatalf("pending body = %s", got.Body.String())
	}
}
