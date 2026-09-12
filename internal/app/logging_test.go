package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"
	"workbench/internal/foundation/auth"
)

func newLoggingApp(t *testing.T, output io.Writer) *App {
	t.Helper()
	a, err := New(context.Background(), Config{
		ListenAddress: "127.0.0.1:8080", DataPath: filepath.Join(t.TempDir(), "data.db"),
		AuthMode: auth.ModeLocal, ShutdownGrace: time.Second, LogLevel: "info",
	}, slog.New(slog.NewJSONHandler(output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

type logRecords chan map[string]any

func (records logRecords) Write(data []byte) (int, error) {
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		return 0, err
	}
	records <- record
	return len(data), nil
}

func TestHTTPLoggingPreservesStreamingAndUpgrade(t *testing.T) {
	records := make(logRecords, 10)
	a := newLoggingApp(t, records)
	router := a.Handler().(*chi.Mux)
	router.Get("/test/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		<-r.Context().Done()
	})
	router.Get("/test/ws", func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, w.Header())
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("ready"))
	})
	server := httptest.NewServer(a.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/test/stream", nil)
	req.Host = "127.0.0.1:8080"
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, len("data: ready\n\n"))
	_, err = io.ReadFull(response.Body, data)
	response.Body.Close()
	if err != nil || string(data) != "data: ready\n\n" {
		t.Fatalf("SSE was buffered or corrupted: %q, %v", data, err)
	}
	assertStatus := func(want int) {
		t.Helper()
		select {
		case record := <-records:
			if record["status"] != float64(want) {
				t.Fatalf("unexpected access status: %v", record)
			}
		case <-ctx.Done():
			t.Fatal("missing access log")
		}
	}
	assertStatus(200)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, strings.Replace(server.URL, "http:", "ws:", 1)+"/test/ws", http.Header{"Host": {"127.0.0.1:8080"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil || string(payload) != "ready" {
		t.Fatalf("WebSocket upgrade broken: %q, %v", payload, err)
	}
	assertStatus(101)
}

func TestLoggingSettingsPersistAndChangeAllComponents(t *testing.T) {
	var output bytes.Buffer
	a := newLoggingApp(t, &output)
	child := a.logger.With("component", "test-background")
	token, cookies := csrf(t, a.Handler())
	for _, level := range []string{"debug", "info", "warn", "error"} {
		result := request(t, a.Handler(), "PUT", "/api/settings/logging", []byte(`{"level":"`+level+`"}`), token, cookies)
		if result.Code != 200 {
			t.Fatalf("save %s: %d %s", level, result.Code, result.Body.String())
		}
		output.Reset()
		child.Debug("debug-test")
		child.Info("info-test")
		child.Warn("warn-test")
		child.Error("error-test")
		if strings.Contains(output.String(), "debug-test") != (level == "debug") || strings.Contains(output.String(), "info-test") != (level == "debug" || level == "info") || strings.Contains(output.String(), "warn-test") != (level != "error") || !strings.Contains(output.String(), "error-test") {
			t.Fatalf("wrong filtering for %s: %s", level, output.String())
		}
	}
	for _, invalid := range []string{`{}`, `{"level":"trace"}`, `{"level":"off"}`, `{"level":""}`, `{"level":"info","extra":true}`, `{"level":null}`} {
		if result := request(t, a.Handler(), "PUT", "/api/settings/logging", []byte(invalid), token, cookies); result.Code != 400 || a.logLevel.Level() != slog.LevelError {
			t.Fatalf("invalid input changed threshold: %s", invalid)
		}
	}
	if result := request(t, a.Handler(), "PUT", "/api/settings/logging", []byte(`{"level":"info"}`), "", nil); result.Code != 400 || a.logLevel.Level() != slog.LevelError {
		t.Fatal("logging settings bypassed CSRF")
	}
	config := a.config
	config.LogLevel = "debug"
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), config, slog.New(slog.NewJSONHandler(&output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	got := request(t, restarted.Handler(), "GET", "/api/settings", nil, "", nil)
	if !strings.Contains(got.Body.String(), `"logging":{"level":"error"}`) || restarted.logLevel.Level() != slog.LevelError {
		t.Fatalf("saved level did not override startup default: %s", got.Body.String())
	}
	token, cookies = csrf(t, restarted.Handler())
	if err := restarted.DB().Close(); err != nil {
		t.Fatal(err)
	}
	failed := request(t, restarted.Handler(), "PUT", "/api/settings/logging", []byte(`{"level":"debug"}`), token, cookies)
	if failed.Code != 500 || restarted.logLevel.Level() != slog.LevelError || restarted.logging.Level != "error" {
		t.Fatal("failed persistence changed the live threshold")
	}
}

func TestHTTPLogLevelsPrivacyAndRecovery(t *testing.T) {
	var output bytes.Buffer
	a := newLoggingApp(t, &output)
	router := a.Handler().(*chi.Mux)
	router.Get("/test/accounts/{account}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	router.Get("/test/failure", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) })
	router.Get("/test/panic", func(http.ResponseWriter, *http.Request) { panic("private-panic-secret") })
	for _, tc := range []struct{ target, level, path string }{
		{"/api/settings", "INFO", "/api/settings"},
		{"/test/accounts/private-account-id?token=private-query-secret", "INFO", "/test/accounts/{account}"},
		{"/test/failure", "ERROR", "/test/failure"},
		{"/test/panic", "ERROR", "/test/panic"},
	} {
		output.Reset()
		response := request(t, a.Handler(), "GET", tc.target, nil, "", nil)
		lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
		var record map[string]any
		if err := json.Unmarshal(lines[len(lines)-1], &record); err != nil {
			t.Fatal(err)
		}
		if record["level"] != tc.level || record["path"] != tc.path || record["requestId"] != response.Header().Get("X-Request-ID") || record["status"] != float64(response.Code) {
			t.Fatalf("unexpected access log: %s", output.String())
		}
		if strings.Contains(output.String(), "private-") || strings.Contains(response.Body.String(), "private-panic") {
			t.Fatalf("sensitive value logged for %s", tc.path)
		}
	}
	output.Reset()
	request(t, a.Handler(), "GET", "/health/ready", nil, "", nil)
	if output.Len() != 0 {
		t.Fatal("successful health probe logged at INFO")
	}
	a.logLevel.Set(slog.LevelDebug)
	request(t, a.Handler(), "GET", "/health/ready", nil, "", nil)
	if !strings.Contains(output.String(), `"level":"DEBUG"`) {
		t.Fatal("health probe missing at DEBUG")
	}
	output.Reset()
	badHost := httptest.NewRequest("GET", "http://untrusted.example/private-path-secret?code=private-oauth-secret", nil)
	badHost.Header.Set("Authorization", "Bearer private-auth-secret")
	a.Handler().ServeHTTP(httptest.NewRecorder(), badHost)
	if !strings.Contains(output.String(), `"level":"WARN"`) || strings.Contains(output.String(), "private-") {
		t.Fatalf("host rejection logging: %s", output.String())
	}
}
