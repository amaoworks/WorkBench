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

	"workbench/internal/foundation/auth"
)

func TestSettingsPersistAndSwitchAIWithoutRestart(t *testing.T) {
	application := newTestApp(t)
	token, cookies := csrf(t, application.Handler())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
			w.WriteHeader(401)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "test-model" {
			t.Errorf("model = %q", body.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_test","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}`)
	}))
	defer upstream.Close()
	raw, _ := json.Marshal(map[string]any{"enabled": true, "baseUrl": upstream.URL + "/v1", "model": "test-model", "apiKey": "test-secret"})
	tested := request(t, application.Handler(), "POST", "/api/settings/ai/test", raw, token, cookies)
	if tested.Code != 200 {
		t.Fatalf("test: %d %s", tested.Code, tested.Body.String())
	}
	if application.gateway.Available() {
		t.Fatal("connection test persisted unsaved settings")
	}
	saved := request(t, application.Handler(), "PUT", "/api/settings/ai", raw, token, cookies)
	if saved.Code != 200 || !application.gateway.Available() {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	got := request(t, application.Handler(), "GET", "/api/settings", nil, "", cookies)
	if got.Code != 200 || bytes.Contains(got.Body.Bytes(), []byte("test-secret")) || !bytes.Contains(got.Body.Bytes(), []byte(`"hasApiKey":true`)) {
		t.Fatalf("secret-safe readback: %d %s", got.Code, got.Body.String())
	}
	if got.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("settings must not be cached")
	}
	appearance := request(t, application.Handler(), "PUT", "/api/settings/appearance", []byte(`{"theme":"dark","motion":"reduced"}`), token, cookies)
	if appearance.Code != 200 {
		t.Fatalf("appearance: %s", appearance.Body.String())
	}
	config := application.config
	config.OpenAIAPIKey = "environment-key-must-not-override"
	config.OpenAIModel = "environment-model"
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), config, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if restarted.aiSettings.APIKey != "test-secret" || restarted.aiSettings.Model != "test-model" || restarted.appearance.Theme != "dark" || restarted.appearance.Motion != "reduced" {
		t.Fatal("settings were not restored or environment overrode saved settings")
	}
	token, cookies = csrf(t, restarted.Handler())
	retained, _ := json.Marshal(map[string]any{"enabled": true, "baseUrl": upstream.URL + "/v1", "model": "test-model", "apiKey": ""})
	if result := request(t, restarted.Handler(), "PUT", "/api/settings/ai", retained, token, cookies); result.Code != 200 || restarted.aiSettings.APIKey != "test-secret" {
		t.Fatalf("blank key must retain existing: %s", result.Body.String())
	}
	changed := request(t, restarted.Handler(), "PUT", "/api/settings/ai", []byte(`{"enabled":true,"baseUrl":"https://elsewhere.invalid/v1","model":"test-model"}`), token, cookies)
	if changed.Code != 400 {
		t.Fatal("stored secret could be forwarded to a different host")
	}
	cleared, _ := json.Marshal(map[string]any{"enabled": false, "baseUrl": upstream.URL + "/v1", "model": "test-model", "clearApiKey": true})
	if result := request(t, restarted.Handler(), "PUT", "/api/settings/ai", cleared, token, cookies); result.Code != 200 || restarted.gateway.Available() || restarted.aiSettings.APIKey != "" {
		t.Fatalf("clear/disable: %s", result.Body.String())
	}
}

func TestSettingsValidationAndCSRF(t *testing.T) {
	application := newTestApp(t)
	token, cookies := csrf(t, application.Handler())
	cases := []struct{ path, body string }{
		{"/api/settings/ai", `{"enabled":true,"baseUrl":"file:///tmp/test","model":"test","apiKey":"key"}`},
		{"/api/settings/ai", `{"enabled":true,"baseUrl":"https://user:pass@example.com/v1","model":"test","apiKey":"key"}`},
		{"/api/settings/ai", `{"enabled":true,"model":"test"}`},
		{"/api/settings/ai", `{"enabled":false,"model":""}`},
		{"/api/settings/ai", `{"enabled":false,"model":"test","unexpected":1}`},
		{"/api/settings/appearance", `{"theme":"invalid","motion":"full"}`},
		{"/api/settings/appearance", `{"theme":"dark","motion":"invalid"}`},
		{"/api/settings/password", `{"currentPassword":"Oldpass1","newPassword":"Newpass1"}`},
	}
	for _, tc := range cases {
		result := request(t, application.Handler(), "PUT", tc.path, []byte(tc.body), token, cookies)
		if result.Code != 400 {
			t.Errorf("%s: %d %s", tc.path, result.Code, result.Body.String())
		}
	}
	blocked := request(t, application.Handler(), "PUT", "/api/settings/appearance", []byte(`{"theme":"dark","motion":"full"}`), "", nil)
	if blocked.Code != 400 {
		t.Fatalf("CSRF bypass: %d", blocked.Code)
	}
}

func TestAIConnectionErrorDoesNotExposeUpstreamSecrets(t *testing.T) {
	application := newTestApp(t)
	token, cookies := csrf(t, application.Handler())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"error":{"message":"secret-from-upstream","type":"invalid_request_error"}}`)
	}))
	defer upstream.Close()
	raw, _ := json.Marshal(map[string]any{"enabled": true, "baseUrl": upstream.URL, "model": "test", "apiKey": "test-secret"})
	result := request(t, application.Handler(), "POST", "/api/settings/ai/test", raw, token, cookies)
	if result.Code != 502 || strings.Contains(result.Body.String(), "secret") {
		t.Fatalf("unsafe test error: %d %s", result.Code, result.Body.String())
	}
}

func TestPasswordChangeInvalidatesSessionsAndPersists(t *testing.T) {
	cfg := Config{ListenAddress: "127.0.0.1:8080", DataPath: filepath.Join(t.TempDir(), "data.db"), AuthMode: auth.ModePassword, Password: "Oldpass1", ShutdownGrace: time.Second}
	application, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	token, cookies := csrf(t, application.Handler())
	unauthenticated := request(t, application.Handler(), "GET", "/api/settings", nil, "", cookies)
	if unauthenticated.Code != 401 {
		t.Fatal("settings accessible without login")
	}
	login := request(t, application.Handler(), "POST", "/api/auth/login", []byte(`{"password":"Oldpass1"}`), token, cookies)
	if login.Code != 200 {
		t.Fatalf("login: %s", login.Body.String())
	}
	cookies = append(cookies, login.Result().Cookies()...)
	wrong := request(t, application.Handler(), "PUT", "/api/settings/password", []byte(`{"currentPassword":"wrong","newPassword":"Newpass1"}`), token, cookies)
	if wrong.Code != 403 {
		t.Fatal("wrong current password accepted")
	}
	short := request(t, application.Handler(), "PUT", "/api/settings/password", []byte(`{"currentPassword":"Oldpass1","newPassword":"Abcde1!"}`), token, cookies)
	if short.Code != 400 {
		t.Fatal("seven-character password accepted")
	}
	// Simulate an in-flight request later restoring an old session into the store.
	var oldToken string
	var oldData []byte
	var expiry int64
	if err := application.DB().QueryRow("SELECT token, data, expiry FROM sessions LIMIT 1").Scan(&oldToken, &oldData, &expiry); err != nil {
		t.Fatal(err)
	}
	changed := request(t, application.Handler(), "PUT", "/api/settings/password", []byte(`{"currentPassword":"Oldpass1","newPassword":"Newpass1"}`), token, cookies)
	if changed.Code != 200 {
		t.Fatalf("change: %d %s", changed.Code, changed.Body.String())
	}
	if _, err := application.DB().Exec("INSERT INTO sessions(token, data, expiry) VALUES (?, ?, ?)", oldToken, oldData, expiry); err != nil {
		t.Fatal(err)
	}
	stale := request(t, application.Handler(), "GET", "/api/settings", nil, "", cookies)
	if stale.Code != 401 {
		t.Fatal("stale session remained authenticated")
	}
	status := request(t, application.Handler(), "GET", "/api/auth/status", nil, "", cookies)
	if !bytes.Contains(status.Body.Bytes(), []byte(`"authenticated":false`)) {
		t.Fatal("status reports stale session authenticated")
	}
	if ok, _ := application.auth.Authenticate(context.Background(), "Oldpass1"); ok {
		t.Fatal("old password still works")
	}
	if ok, err := application.auth.Authenticate(context.Background(), "Newpass1"); !ok || err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
	if err := application.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.Password = ""
	restarted, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if ok, err := restarted.auth.Authenticate(context.Background(), "Newpass1"); !ok || err != nil {
		t.Fatalf("new password did not persist: %v", err)
	}
}
