package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/justinas/nosurf"

	"workbench/internal/contracts"
	"workbench/internal/foundation/auth"
	workbenchdb "workbench/internal/foundation/database"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	application, err := New(context.Background(), Config{
		ListenAddress: "127.0.0.1:8080",
		DataPath:      filepath.Join(t.TempDir(), "data.db"),
		AuthMode:      auth.ModeLocal,
		ShutdownGrace: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	return application
}

func request(t *testing.T, handler http.Handler, method, target string, body []byte, token string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Host = "127.0.0.1:8080"
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions {
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if token != "" {
		req.Header.Set("X-CSRF-Token", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func csrf(t *testing.T, handler http.Handler) (string, []*http.Cookie) {
	t.Helper()
	recorder := request(t, handler, http.MethodGet, "/api/auth/csrf", nil, "", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("csrf status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.Token == "" {
		t.Fatalf("invalid csrf response: %v, %s", err, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("csrf response did not set a cookie; headers = %v", recorder.Header())
	}
	if !nosurf.VerifyToken(cookies[0].Value, response.Token) {
		t.Fatalf("csrf response token does not match cookie")
	}
	return response.Token, cookies
}

func TestTodoHTTPVerticalSliceAndModuleGate(t *testing.T) {
	application := newTestApp(t)
	token, cookies := csrf(t, application.Handler())
	created := request(t, application.Handler(), http.MethodPost, "/api/modules/todo/tasks", []byte(`{"title":"Ship MVP"}`), token, cookies)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s, cookies = %v", created.Code, created.Body.String(), cookies)
	}
	listed := request(t, application.Handler(), http.MethodGet, "/api/modules/todo/tasks", nil, "", cookies)
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte("Ship MVP")) {
		t.Fatalf("list status = %d, body = %s", listed.Code, listed.Body.String())
	}

	disabled := request(t, application.Handler(), http.MethodPut, "/api/modules/todo/enabled", []byte(`{"enabled":false}`), token, cookies)
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable status = %d, body = %s", disabled.Code, disabled.Body.String())
	}
	blocked := request(t, application.Handler(), http.MethodGet, "/api/modules/todo/tasks", nil, "", cookies)
	if blocked.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled module status = %d, body = %s", blocked.Code, blocked.Body.String())
	}
	dashboard := request(t, application.Handler(), http.MethodGet, "/api/dashboard", nil, "", cookies)
	if dashboard.Code != http.StatusOK || bytes.Contains(dashboard.Body.Bytes(), []byte("todo.summary")) {
		t.Fatalf("disabled module remained on dashboard: status = %d, body = %s", dashboard.Code, dashboard.Body.String())
	}
	reenabled := request(t, application.Handler(), http.MethodPut, "/api/modules/todo/enabled", []byte(`{"enabled":true}`), token, cookies)
	if reenabled.Code != http.StatusOK {
		t.Fatalf("re-enable status = %d, body = %s", reenabled.Code, reenabled.Body.String())
	}
	restored := request(t, application.Handler(), http.MethodGet, "/api/modules/todo/tasks", nil, "", cookies)
	if restored.Code != http.StatusOK || !bytes.Contains(restored.Body.Bytes(), []byte("Ship MVP")) {
		t.Fatalf("re-enabled module lost history: status = %d, body = %s", restored.Code, restored.Body.String())
	}
}

func TestCSRFAndSPAFallbackBoundaries(t *testing.T) {
	application := newTestApp(t)
	withoutToken := request(t, application.Handler(), http.MethodPost, "/api/modules/todo/tasks", []byte(`{"title":"blocked"}`), "", nil)
	if withoutToken.Code != http.StatusBadRequest {
		t.Fatalf("CSRF status = %d, body = %s", withoutToken.Code, withoutToken.Body.String())
	}
	unknownAPI := request(t, application.Handler(), http.MethodGet, "/api/does-not-exist", nil, "", nil)
	if unknownAPI.Code != http.StatusNotFound {
		t.Fatalf("unknown API status = %d", unknownAPI.Code)
	}
	spa := request(t, application.Handler(), http.MethodGet, "/todo", nil, "", nil)
	if spa.Code != http.StatusOK || !bytes.Contains(spa.Body.Bytes(), []byte("<!doctype html>")) {
		t.Fatalf("SPA fallback status = %d, body = %s", spa.Code, spa.Body.String())
	}
	if spa.Header().Get("X-Request-ID") == "" {
		t.Fatal("response did not include a request ID")
	}
}

func TestOnlineBackupEndpointCreatesVerifiedDatabase(t *testing.T) {
	application := newTestApp(t)
	token, cookies := csrf(t, application.Handler())
	created := request(t, application.Handler(), http.MethodPost, "/api/modules/todo/tasks", []byte(`{"title":"Survives restore"}`), token, cookies)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.Code, created.Body.String())
	}
	recorder := request(t, application.Handler(), http.MethodPost, "/api/system/backup", []byte(`{}`), token, cookies)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("backup status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		File string `json:"file"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil || response.File == "" {
		t.Fatalf("invalid backup response: %v, %s", err, recorder.Body.String())
	}
	backupPath := filepath.Join(filepath.Dir(application.config.DataPath), "backups", response.File)
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}
	if err := workbenchdb.IntegrityCheck(context.Background(), backupPath); err != nil {
		t.Fatalf("backup integrity check failed: %v", err)
	}
	restored, err := New(context.Background(), Config{
		ListenAddress: "127.0.0.1:8080", DataPath: backupPath,
		AuthMode: auth.ModeLocal, ShutdownGrace: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("start restored backup: %v", err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	restoredList := request(t, restored.Handler(), http.MethodGet, "/api/modules/todo/tasks", nil, "", nil)
	if restoredList.Code != http.StatusOK || !bytes.Contains(restoredList.Body.Bytes(), []byte("Survives restore")) {
		t.Fatalf("restored list status = %d, body = %s", restoredList.Code, restoredList.Body.String())
	}
}

func TestMaintenanceJobRemovesExpiredSessionsAndOldHandledNotifications(t *testing.T) {
	application := newTestApp(t)
	old := time.Now().UTC().Add(-100 * 24 * time.Hour).UnixMilli()
	if _, err := application.DB().Exec(`INSERT INTO sessions(token, data, expiry) VALUES ('expired', X'01', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := application.DB().Exec(`
		INSERT INTO notifications(
			id, source_module, severity, title, content, idempotency_key, created_at, read_at
		) VALUES ('old-notice', 'core', 'info', 'old', 'old', 'old-notice', ?, ?)`, old, old); err != nil {
		t.Fatal(err)
	}
	job := maintenanceJob(application.DB())
	if err := job.Handler(context.Background(), contracts.JobRun{ID: "maintenance-test", JobID: job.ID}); err != nil {
		t.Fatal(err)
	}
	var sessions, notifications int
	if err := application.DB().QueryRow("SELECT COUNT(*) FROM sessions WHERE token = 'expired'").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := application.DB().QueryRow("SELECT COUNT(*) FROM notifications WHERE id = 'old-notice'").Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || notifications != 0 {
		t.Fatalf("expired sessions/old notifications = %d/%d, want 0/0", sessions, notifications)
	}
}
