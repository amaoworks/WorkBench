package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
	"workbench/internal/capabilities/notifications"
	"workbench/internal/contracts"
	"workbench/internal/foundation/auth"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/events"
	"workbench/internal/foundation/modules"
	"workbench/internal/modules/todo"

	"workbench/internal/capabilities/dashboard"
)

func TestDashboardLayoutIndependentOfModuleAndSurvivesRestart(t *testing.T) {
	a := newTestApp(t)
	token, cookies := csrf(t, a.Handler())
	layout := []byte(`{"items":[{"id":"todo.summary","visible":false,"size":"large","order":1},{"id":"investment.overview","visible":true,"size":"small","order":0}]}`)
	if got := request(t, a.Handler(), "PUT", "/api/dashboard/layout", layout, "", nil); got.Code != 400 {
		t.Fatalf("missing CSRF: %d", got.Code)
	}
	if got := request(t, a.Handler(), "PUT", "/api/dashboard/layout", layout, token, cookies); got.Code != 200 {
		t.Fatalf("save: %d %s", got.Code, got.Body.String())
	}
	for _, invalid := range []string{
		`{"items":[]}`,
		`{"items":[{"id":"todo.summary","visible":true,"size":"huge","order":0},{"id":"investment.overview","visible":true,"size":"small","order":1}]}`,
		`{"items":[{"id":"todo.summary","visible":true,"size":"small","order":0},{"id":"todo.summary","visible":true,"size":"small","order":1}]}`,
		`{"items":[{"id":"unknown.card","visible":true,"size":"small","order":0},{"id":"investment.overview","visible":true,"size":"small","order":1}]}`,
		`{"items":[{"id":"todo.summary","size":"small","order":0},{"id":"investment.overview","visible":true,"size":"small","order":1}]}`,
	} {
		if got := request(t, a.Handler(), "PUT", "/api/dashboard/layout", []byte(invalid), token, cookies); got.Code != 400 {
			t.Fatalf("invalid layout accepted: %s", invalid)
		}
	}
	got := request(t, a.Handler(), "GET", "/api/dashboard", nil, "", cookies)
	if got.Code != 200 || bytes.Contains(got.Body.Bytes(), []byte("todo.summary")) || !bytes.Contains(got.Body.Bytes(), []byte("investment.overview")) {
		t.Fatalf("visible cards: %s", got.Body.String())
	}
	if got := request(t, a.Handler(), "GET", "/api/modules/todo/tasks", nil, "", cookies); got.Code != 200 {
		t.Fatal("hiding card disabled business")
	}
	if got := request(t, a.Handler(), "PUT", "/api/modules/investment/enabled", []byte(`{"enabled":false}`), token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if got := request(t, a.Handler(), "GET", "/api/dashboard", nil, "", cookies); !bytes.Contains(got.Body.Bytes(), []byte(`"widgets":[]`)) {
		t.Fatalf("disabled widget visible: %s", got.Body.String())
	}
	cfg := a.config
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), cfg, a.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	token, cookies = csrf(t, restarted.Handler())
	if restarted.registry.IsEnabled("investment") {
		t.Fatal("disabled module state lost")
	}
	catalog := request(t, restarted.Handler(), "GET", "/api/dashboard/widgets", nil, "", cookies)
	var body struct {
		Widgets []dashboard.Widget `json:"widgets"`
	}
	if err := json.Unmarshal(catalog.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Widgets) != 2 || body.Widgets[0].ID != "investment.overview" || body.Widgets[0].Enabled || body.Widgets[0].Size != "small" || body.Widgets[1].Visible || body.Widgets[1].Size != "large" {
		t.Fatalf("lost preferences: %+v", body)
	}
	if got := request(t, restarted.Handler(), "PUT", "/api/modules/investment/enabled", []byte(`{"enabled":true}`), token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	visible := request(t, restarted.Handler(), "GET", "/api/dashboard", nil, "", cookies)
	if bytes.Contains(visible.Body.Bytes(), []byte("todo.summary")) || !bytes.Contains(visible.Body.Bytes(), []byte("investment.overview")) {
		t.Fatal("reenabling lost preferences")
	}
	if got := request(t, restarted.Handler(), "DELETE", "/api/dashboard/layout", []byte(`{}`), token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	defaults := request(t, restarted.Handler(), "GET", "/api/dashboard", nil, "", cookies)
	if !bytes.Contains(defaults.Body.Bytes(), []byte("todo.summary")) {
		t.Fatal("reset did not restore defaults")
	}
}

func TestInvestmentUsesLiveAIAndPreservesDataAcrossDisableAndRestart(t *testing.T) {
	a := newTestApp(t)
	token, cookies := csrf(t, a.Handler())
	post := func(path string) *httptest.ResponseRecorder {
		return request(t, a.Handler(), "POST", path, []byte(`{}`), token, cookies)
	}
	if got := post("/api/modules/investment/summary"); got.Code != 409 {
		t.Fatalf("no quotes: %d", got.Code)
	}
	if got := post("/api/modules/investment/sync"); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if got := post("/api/modules/investment/summary"); got.Code != 503 {
		t.Fatalf("AI disabled: %d %s", got.Code, got.Body.String())
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !bytes.Contains(raw, []byte("DEMO-A")) || bytes.Contains(raw, []byte(`"tools":[{`)) {
			t.Errorf("unexpected business AI request: %s", raw)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_test","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"模拟行情摘要"}]}]}`)
	}))
	defer upstream.Close()
	raw, _ := json.Marshal(map[string]any{"enabled": true, "baseUrl": upstream.URL + "/v1", "model": "test-model", "apiKey": "test-key"})
	if got := request(t, a.Handler(), "PUT", "/api/settings/ai", raw, token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if got := post("/api/modules/investment/summary"); got.Code != 200 || !bytes.Contains(got.Body.Bytes(), []byte("模拟行情摘要")) {
		t.Fatalf("AI summary: %d %s", got.Code, got.Body.String())
	}
	// Pending module events pause while disabled, then reach the common notification service.
	if got := request(t, a.Handler(), "PUT", "/api/modules/investment/enabled", []byte(`{"enabled":false}`), token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if err := a.dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := a.DB().QueryRow("SELECT COUNT(*) FROM notifications WHERE source_module = 'investment'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("disabled consumer ran: %d %v", count, err)
	}
	if got := post("/api/modules/investment/sync"); got.Code != 503 {
		t.Fatal("disabled route ran")
	}
	cfg := a.config
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), cfg, a.logger)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	token, cookies = csrf(t, restarted.Handler())
	if got := request(t, restarted.Handler(), "PUT", "/api/modules/investment/enabled", []byte(`{"enabled":true}`), token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if err := restarted.dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.DB().QueryRow("SELECT COUNT(*) FROM notifications WHERE source_module = 'investment'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("notification count: %d %v", count, err)
	}
	got := request(t, restarted.Handler(), "GET", "/api/modules/investment/overview", nil, "", cookies)
	if got.Code != 200 || !bytes.Contains(got.Body.Bytes(), []byte("模拟行情摘要")) || !bytes.Contains(got.Body.Bytes(), []byte("DEMO-A")) {
		t.Fatalf("data lost: %s", got.Body.String())
	}
	raw, _ = json.Marshal(map[string]any{"enabled": false, "baseUrl": upstream.URL + "/v1", "model": "test-model"})
	if got := request(t, restarted.Handler(), "PUT", "/api/settings/ai", raw, token, cookies); got.Code != 200 {
		t.Fatal(got.Body.String())
	}
	if got := request(t, restarted.Handler(), "POST", "/api/modules/investment/summary", []byte(`{}`), token, cookies); got.Code != 503 {
		t.Fatal("new business AI request ignored disabled settings")
	}
}

// Simulate an older workspace that has only Todo, then install the second compiled business.
func TestUpgradeTodoOnlyWorkspacePreservesDataAndAddsDefaultWidget(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "data.db")
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateCore(ctx); err != nil {
		t.Fatal(err)
	}
	store := events.NewStore(db.SQL())
	notices, err := notifications.NewService(db.SQL(), store)
	if err != nil {
		t.Fatal(err)
	}
	module, err := todo.New(db.SQL(), store, notices)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := modules.Initialize(ctx, db, []contracts.Module{module})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := module.Create(ctx, todo.CreateTask{Title: "Old workspace task"}); err != nil {
		t.Fatal(err)
	}
	if err := registry.SetEnabled(ctx, "todo", false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO workspace_settings(section,value) VALUES('dashboard','[{"id":"todo.summary","visible":false,"size":"large","order":0}]')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	app, err := New(ctx, Config{ListenAddress: "127.0.0.1:8080", DataPath: path, AuthMode: auth.ModeLocal, ShutdownGrace: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if app.registry.IsEnabled("todo") || !app.registry.IsEnabled("investment") {
		t.Fatal("module state mismatch after upgrade")
	}
	var title string
	if err := app.DB().QueryRow("SELECT title FROM todo_tasks").Scan(&title); err != nil || title != "Old workspace task" {
		t.Fatalf("lost old business: %q %v", title, err)
	}
	catalog := request(t, app.Handler(), "GET", "/api/dashboard/widgets", nil, "", nil)
	var body struct {
		Widgets []dashboard.Widget `json:"widgets"`
	}
	if err := json.Unmarshal(catalog.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Widgets) != 2 || body.Widgets[0].Visible || body.Widgets[0].Size != "large" || !body.Widgets[1].Visible || body.Widgets[1].ID != "investment.overview" {
		t.Fatalf("upgrade layout: %+v", body)
	}
	// Online backup must include both the new schema and the original settings.
	token, cookies := csrf(t, app.Handler())
	backup := request(t, app.Handler(), "POST", "/api/system/backup", []byte(`{}`), token, cookies)
	var result struct {
		File string `json:"file"`
	}
	if backup.Code != 201 || json.Unmarshal(backup.Body.Bytes(), &result) != nil {
		t.Fatal(backup.Body.String())
	}
	cfg := app.config
	cfg.DataPath = filepath.Join(filepath.Dir(path), "backups", result.File)
	restored, err := New(ctx, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got := request(t, restored.Handler(), "GET", "/api/dashboard/widgets", nil, "", nil)
	if !bytes.Equal(got.Body.Bytes(), catalog.Body.Bytes()) {
		t.Fatalf("backup layout changed: %s", got.Body.String())
	}
}
