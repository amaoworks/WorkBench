package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"workbench/internal/capabilities/notifications"
	"workbench/internal/contracts"
	"workbench/internal/foundation/events"
	"workbench/internal/foundation/modules"
)

// Export real HTTP responses, never hand-written copies of response DTOs. The
// frontend test validates these with the very schemas used by production calls.
func TestFrontendContracts(t *testing.T) {
	a := newTestApp(t)
	token, cookies := csrf(t, a.Handler())
	handler := a.Handler()
	type sample struct {
		Name   string          `json:"name"`
		Schema string          `json:"schema"`
		Status int             `json:"status"`
		Body   json.RawMessage `json:"body"`
	}
	samples := []sample{}
	capture := func(name, schema, method, path, body string, status int) json.RawMessage {
		t.Helper()
		var input []byte
		if body != "" {
			input = []byte(body)
		}
		got := request(t, handler, method, path, input, token, cookies)
		if got.Code != status {
			t.Fatalf("%s: status %d, want %d: %s", name, got.Code, status, got.Body.String())
		}
		raw := append(json.RawMessage(nil), got.Body.Bytes()...)
		if !json.Valid(raw) {
			t.Fatalf("%s: invalid JSON: %s", name, raw)
		}
		samples = append(samples, sample{Name: name, Schema: schema, Status: status, Body: raw})
		return raw
	}
	capture("auth", "authStatus", "GET", "/api/auth/status", "", 200)
	capture("modules", "modules", "GET", "/api/modules", "", 200)
	capture("dashboard", "dashboard", "GET", "/api/dashboard", "", 200)
	capture("widgets", "widgets", "GET", "/api/dashboard/widgets", "", 200)
	capture("settings", "settings", "GET", "/api/settings", "", 200)
	capture("telegram", "telegram", "GET", "/api/settings/telegram", "", 200)
	capture("telegram.saved", "saved", "PUT", "/api/settings/telegram", `{"enabled":false,"botToken":"123:fixture-token","chatId":"12345"}`, 200)
	capture("telegram.configured", "telegram", "GET", "/api/settings/telegram", "", 200)
	capture("appearance.saved", "appearance", "PUT", "/api/settings/appearance", `{"theme":"dark","motion":"reduced"}`, 200)
	capture("logging.saved", "logging", "PUT", "/api/settings/logging", `{"level":"warn"}`, 200)
	capture("ai.status", "aiStatus", "GET", "/api/ai/status", "", 200)
	capture("tasks.empty", "tasks", "GET", "/api/modules/todo/tasks", "", 200)
	created := capture("task.created", "task", "POST", "/api/modules/todo/tasks", `{"title":"Contract fixture","dueAt":"2030-01-02T03:04:05Z"}`, 201)
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created, &task); err != nil {
		t.Fatal(err)
	}
	capture("task.completed", "task", "PATCH", "/api/modules/todo/tasks/"+task.ID, `{"completed":true}`, 200)
	capture("task.second", "task", "POST", "/api/modules/todo/tasks", `{"title":"Second task","dueAt":null}`, 201)
	capture("tasks.paginated", "tasks", "GET", "/api/modules/todo/tasks?limit=1", "", 200)
	capture("todo.summary", "summary", "GET", "/api/modules/todo/widget/summary", "", 200)
	capture("wallos", "wallos", "GET", "/api/modules/todo/wallos", "", 200)
	capture("wallos.saved", "saved", "PUT", "/api/modules/todo/wallos", `{"enabled":false,"baseUrl":"","apiKey":"","daysBefore":3,"reminderHour":9,"timeZone":"UTC"}`, 200)
	capture("wallos.sync", "wallosSync", "POST", "/api/modules/todo/wallos/sync", `{}`, 200)
	capture("schwab", "schwab", "GET", "/api/modules/investment/schwab", "", 200)
	capture("schwab.saved", "schwab", "PUT", "/api/modules/investment/schwab", `{"appKey":"fixture","appSecret":"fixture-secret","callbackUrl":"https://127.0.0.1:8080/oauth/schwab"}`, 200)
	capture("futu", "futu", "GET", "/api/modules/investment/futu", "", 200)
	capture("futu.saved", "futu", "PUT", "/api/modules/investment/futu", `{"enabled":false,"account":"","password":"","clearPassword":true}`, 200)
	capture("overnight", "overnight", "GET", "/api/modules/investment/overnight", "", 200)
	capture("overnight.saved", "overnight", "PUT", "/api/modules/investment/overnight", `{"enabled":false}`, 200)
	capture("monitor.empty", "priceMonitor", "GET", "/api/modules/investment/monitor", "", 200)
	ruleJSON := capture("monitor.rule.created", "priceRule", "POST", "/api/modules/investment/monitor/rules", `{"symbol":"AAPL","direction":"up","thresholdPercent":3,"enabled":true}`, 201)
	var rule struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(ruleJSON, &rule); err != nil {
		t.Fatal(err)
	}
	capture("monitor.rule.updated", "priceRule", "PUT", "/api/modules/investment/monitor/rules/"+rule.ID, `{"symbol":"AAPL","direction":"up","thresholdPercent":3,"enabled":false}`, 200)
	if _, err := a.DB().Exec(`UPDATE investment_price_rules SET price=103,previous_close=100,change_percent=3,quote_at=1789999200000,checked_at=1789999200000 WHERE id=?`, rule.ID); err != nil {
		t.Fatal(err)
	}
	capture("monitor.populated", "priceMonitor", "GET", "/api/modules/investment/monitor", "", 200)
	capture("monitor.history.empty", "priceHistory", "GET", "/api/modules/investment/monitor/history", "", 200)
	if _, err := a.DB().Exec(`INSERT INTO investment_price_triggers(id,rule_id,trading_date,symbol,direction,threshold_bps,price,previous_close,change_percent,quote_at,triggered_at,notification_id) VALUES('trigger',?,'2026-09-22','AAPL','up',300,103,100,3,1789999200000,1789999200000,'fixture')`, rule.ID); err != nil {
		t.Fatal(err)
	}
	capture("monitor.history.populated", "priceHistory", "GET", "/api/modules/investment/monitor/history", "", 200)
	capture("notifications.empty", "notifications", "GET", "/api/notifications", "", 200)
	service, err := notifications.NewService(a.DB(), events.NewStore(a.DB()))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	note, err := service.Create(context.Background(), contracts.NewNotification{
		SourceModule: "todo", Severity: contracts.NotificationInfo, Title: "Fixture", Content: "Contract notification",
		ActionLabel: "Open", ActionRoute: "/todo", IdempotencyKey: "contract-fixture", ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	capture("notifications.populated", "notifications", "GET", "/api/notifications", "", 200)
	if err := service.MarkRead(context.Background(), []string{note.ID}); err != nil {
		t.Fatal(err)
	}
	capture("notifications.read", "notifications", "GET", "/api/notifications", "", 200)
	capture("notifications.count", "unreadCount", "GET", "/api/notifications/unread-count", "", 200)
	capture("module.disabled", "module", "PUT", "/api/modules/todo/enabled", `{"enabled":false}`, 200)
	capture("module.status", "module", "GET", "/api/modules/todo/status", "", 200)
	capture("module.gated", "error", "GET", "/api/modules/todo/tasks", "", 503)
	capture("module.enabled", "module", "PUT", "/api/modules/todo/enabled", `{"enabled":true}`, 200)
	capture("task.invalid", "error", "POST", "/api/modules/todo/tasks", `{"title":""}`, 400)
	capture("module.unknown", "error", "PUT", "/api/modules/unknown/enabled", `{"enabled":true}`, 404)
	capture("backup", "backup", "POST", "/api/system/backup", `{}`, 201)
	// Exercise the real platform handler with an independently registered lifecycle
	// module, including failure and retry response shapes.
	fixture := &contractLifecycleModule{}
	registry, err := modules.Initialize(context.Background(), a.database, []contracts.Module{fixture})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	platform := &App{registry: registry, logger: a.logger}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/modules/{id}/enabled", platform.setModuleEnabled)
	mux.HandleFunc("GET /api/modules/{id}/status", platform.getModuleStatus)
	handler = mux
	fixture.fail = true
	capture("module.lifecycle.error", "error", "PUT", "/api/modules/fixture/enabled", `{"enabled":true}`, 503)
	capture("module.lifecycle.failed", "module", "GET", "/api/modules/fixture/status", "", 200)
	fixture.fail = false
	capture("module.lifecycle.retry", "module", "PUT", "/api/modules/fixture/enabled", `{"enabled":true}`, 200)
	if output := os.Getenv("WORKBENCH_CONTRACT_OUTPUT"); output != "" {
		raw, err := json.Marshal(samples)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

// Deliberately has no investment-specific behavior or resources.
type contractLifecycleModule struct{ fail bool }

func (*contractLifecycleModule) Manifest() contracts.ModuleManifest {
	return contracts.ModuleManifest{ID: "fixture", Name: "Fixture", Version: "1", ContractVersion: 1}
}
func (*contractLifecycleModule) Migrations() contracts.MigrationSet       { return contracts.MigrationSet{} }
func (*contractLifecycleModule) Register(contracts.ModuleRegistrar) error { return nil }
func (m *contractLifecycleModule) OnEnabledChanged(context.Context, bool) error {
	if m.fail {
		return errors.New("fixture lifecycle failure")
	}
	return nil
}
func (*contractLifecycleModule) Close() {}
