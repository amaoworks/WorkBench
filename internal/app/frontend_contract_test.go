package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	captureNoContent := func(name, method, path string, status int) {
		t.Helper()
		got := request(t, handler, method, path, nil, token, cookies)
		if got.Code != status || got.Body.Len() != 0 {
			t.Fatalf("%s: status/body %d/%q, want %d/empty", name, got.Code, got.Body.String(), status)
		}
	}
	capture("auth", "authStatus", "GET", "/api/auth/status", "", 200)
	now := time.Now().UTC()
	if _, err := a.DB().Exec(`
		INSERT INTO auth_device_sessions(id,device_name,platform,credential_version,created_at,last_seen_at,expires_at)
		VALUES(?,?,?,?,?,?,?)`, "contract-device", "Fixture iPhone", "ios", 0, now.Add(-time.Hour).UnixMilli(), now.Add(-time.Minute).UnixMilli(), now.Add(24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	capture("auth.device-sessions", "deviceSessions", "GET", "/api/auth/sessions", "", 200)
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
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"model-a"},{"id":"model-b"}]}`))
	}))
	defer modelServer.Close()
	modelInput, _ := json.Marshal(map[string]string{"baseUrl": modelServer.URL, "apiKey": "fixture-key"})
	capture("ai.models", "aiModels", "POST", "/api/settings/ai/models", string(modelInput), 200)
	conversationTime := now.Add(-2 * time.Hour).UnixMilli()
	if _, err := a.DB().Exec(`
		INSERT INTO ai_conversations(id,title,created_at,updated_at) VALUES
		('contract-conversation','Fixture conversation',?,?),
		('contract-conversation-second','Second fixture',?,?)`,
		conversationTime, conversationTime+2, conversationTime+1, conversationTime+1); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB().Exec(`
		INSERT INTO ai_messages(id,conversation_id,role,content_json,created_at) VALUES
		('contract-message-1','contract-conversation','user','"First fixture message"',?),
		('contract-message-2','contract-conversation','assistant','"Second fixture message"',?),
		('contract-message-3','contract-conversation','user','"Third fixture message"',?)`,
		conversationTime, conversationTime+1, conversationTime+2); err != nil {
		t.Fatal(err)
	}
	capture("ai.conversations", "conversations", "GET", "/api/conversations?limit=1", "", 200)
	capture("ai.messages", "conversationMessages", "GET", "/api/conversations/contract-conversation/messages?limit=2", "", 200)
	capture("ai.conversation.renamed", "saved", "PATCH", "/api/conversations/contract-conversation", `{"title":"Renamed fixture"}`, 200)
	captureNoContent("ai.conversation.archived", "DELETE", "/api/conversations/contract-conversation", 204)
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
	capture("tasks.counts", "taskCounts", "GET", "/api/modules/todo/tasks/counts?tz=UTC", "", 200)
	capture("task.priority", "task", "POST", "/api/modules/todo/tasks", `{"title":"Priority task","priority":"high","category":"work"}`, 201)
	capture("tasks.filtered", "tasks", "GET", "/api/modules/todo/tasks?view=later&priority=high&category=work&tz=UTC", "", 200)
	capture("todo.summary", "summary", "GET", "/api/modules/todo/widget/summary", "", 200)
	if _, err := a.DB().Exec(`UPDATE todo_tasks SET due_at=?, completed_at=NULL WHERE id=?`, time.Now().Add(-time.Hour).UnixMilli(), task.ID); err != nil {
		t.Fatal(err)
	}
	capture("todo.today", "todoToday", "GET", "/api/modules/todo/widget/today?tz=Asia/Shanghai", "", 200)
	capture("wallos", "wallos", "GET", "/api/modules/todo/wallos", "", 200)
	capture("wallos.saved", "saved", "PUT", "/api/modules/todo/wallos", `{"enabled":false,"baseUrl":"","apiKey":"","daysBefore":3,"reminderHour":9,"timeZone":"UTC"}`, 200)
	capture("wallos.sync", "wallosSync", "POST", "/api/modules/todo/wallos/sync", `{}`, 200)
	capture("schwab", "schwab", "GET", "/api/modules/investment/schwab", "", 200)
	capture("market-data.none", "marketDataStatus", "GET", "/api/modules/investment/market-data", "", 200)
	if _, err := a.DB().Exec(`UPDATE investment_futu SET enabled=1, overnight_enabled=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	capture("market-data.futu", "marketDataStatus", "GET", "/api/modules/investment/market-data", "", 200)
	capture("overview.quotes.futu-empty", "investmentQuotes", "GET", "/api/modules/investment/overview/quotes", "", 200)
	if _, err := a.DB().Exec(`UPDATE investment_futu SET enabled=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	capture("schwab.saved", "schwab", "PUT", "/api/modules/investment/schwab", `{"appKey":"fixture","appSecret":"fixture-secret","callbackUrl":"https://127.0.0.1:8080/oauth/schwab"}`, 200)
	capture("overview.snapshot.disconnected", "investmentSnapshot", "GET", "/api/modules/investment/overview/snapshot", "", 200)
	capture("overview.quotes.disconnected", "investmentQuotes", "GET", "/api/modules/investment/overview/quotes", "", 200)
	capture("positions.disconnected", "investmentPositions", "GET", "/api/modules/investment/positions", "", 200)
	capture("orders.disconnected", "investmentOrders", "GET", "/api/modules/investment/orders?days=7&tz=UTC", "", 200)
	capture("networth.disconnected", "investmentNetworth", "GET", "/api/modules/investment/networth?range=all", "", 200)
	capturePortfolioContracts(t, a, capture)
	capture("futu", "futu", "GET", "/api/modules/investment/futu", "", 200)
	capture("futu.saved", "futu", "PUT", "/api/modules/investment/futu", `{"enabled":false,"account":"","password":"","clearPassword":true}`, 200)
	capture("overnight", "overnight", "GET", "/api/modules/investment/overnight", "", 200)
	capture("overnight.saved", "overnight", "PUT", "/api/modules/investment/overnight", `{"enabled":false}`, 200)
	capture("preferences.empty", "investmentPreferences", "GET", "/api/modules/investment/preferences", "", 200)
	capture("preferences.saved", "investmentPreferenceSaved", "PUT", "/api/modules/investment/preferences", `{"name":"view","revision":0,"writer":"contract","sequence":1,"value":{"tab":"monitor","expanded":false}}`, 200)
	capture("preferences.view", "investmentPreferences", "GET", "/api/modules/investment/preferences", "", 200)
	capture("preferences.deleted", "investmentPreferenceSaved", "PUT", "/api/modules/investment/preferences", `{"name":"view","revision":0,"writer":"contract","sequence":2,"value":null}`, 200)
	capture("preferences.tombstone", "investmentPreferences", "GET", "/api/modules/investment/preferences", "", 200)
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
	if _, err := a.DB().Exec(`UPDATE investment_price_triggers SET triggered_at=? WHERE id='trigger'`, time.Now().Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	capture("overview.alerts", "investmentAlerts", "GET", "/api/modules/investment/overview/alerts", "", 200)
	capture("notifications.empty", "notifications", "GET", "/api/notifications", "", 200)
	service, err := notifications.NewService(a.DB(), events.NewStore(a.DB()))
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	ownerCtx := contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: contracts.LegacyUserID, Role: contracts.RoleAdmin})
	note, err := service.Create(ownerCtx, contracts.NewNotification{
		SourceModule: "todo", Severity: contracts.NotificationInfo, Title: "Fixture", Content: "Contract notification",
		ActionLabel: "Open", ActionRoute: "/todo", IdempotencyKey: "contract-fixture", ExpiresAt: &expires,
	})
	if err != nil {
		t.Fatal(err)
	}
	capture("notifications.populated", "notifications", "GET", "/api/notifications", "", 200)
	if _, err := service.Create(ownerCtx, contracts.NewNotification{
		SourceModule: "news", Severity: contracts.NotificationInfo, Title: "News fixture", Content: "News contract",
		IdempotencyKey: "news-contract-fixture",
	}); err != nil {
		t.Fatal(err)
	}
	capture("notifications.filtered", "notifications", "GET", "/api/notifications?source=todo&unread=true&limit=1", "", 200)
	capture("notifications.source-count", "unreadCount", "GET", "/api/notifications/unread-count?source=news", "", 200)
	if err := service.MarkRead(ownerCtx, []string{note.ID}); err != nil {
		t.Fatal(err)
	}
	capture("notifications.read", "notifications", "GET", "/api/notifications", "", 200)
	capture("notifications.count", "unreadCount", "GET", "/api/notifications/unread-count", "", 200)
	capture("dashboard.data", "dashboard", "GET", "/api/dashboard?include=data", "", 200)
	captureNewsContracts(t, a, capture)
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

	// Exercise user-management and ordinary-user responses through real password authentication.
	passwordApp := newMultiUserApp(t)
	handler = passwordApp.Handler()
	token, cookies = csrf(t, handler)
	capture("auth.login", "loginResponse", "POST", "/api/auth/login", `{"username":"admin","password":"Adminpass1!"}`, 200)
	admin := loginUser(t, passwordApp, "admin", "Adminpass1!")
	token, cookies = admin.token, admin.cookies
	createdUser := capture("users.created", "createdUser", "POST", "/api/admin/users", `{"username":"contract-user","displayName":"Contract user","role":"user","password":"Contract-User-123!"}`, 201)
	var managed struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(createdUser, &managed); err != nil || managed.User.ID == "" {
		t.Fatalf("invalid created user fixture: %s", createdUser)
	}
	capture("users.list", "managedUsers", "GET", "/api/admin/users", "", 200)
	capture("users.updated", "updatedUser", "PATCH", "/api/admin/users/"+managed.User.ID, `{"displayName":"Updated contract user"}`, 200)
	capture("users.password", "resetUserPassword", "POST", "/api/admin/users/"+managed.User.ID+"/password", `{"password":"Contract-User-123!"}`, 200)
	capture("telegram.mapping.saved", "saved", "PUT", "/api/settings/telegram/recipients/"+managed.User.ID, `{"enabled":false,"chatId":"12345"}`, 200)
	capture("telegram.mapping", "telegramRecipient", "GET", "/api/settings/telegram/recipients/"+managed.User.ID, "", 200)
	if _, err := passwordApp.DB().Exec("UPDATE auth_users SET must_change_password=0 WHERE id=?", managed.User.ID); err != nil {
		t.Fatal(err)
	}
	ordinary := loginUser(t, passwordApp, "contract-user", "Contract-User-123!")
	token, cookies = ordinary.token, ordinary.cookies
	capture("auth.ordinary", "authStatus", "GET", "/api/auth/status", "", 200)
	capture("settings.ordinary", "settings", "GET", "/api/settings", "", 200)
	capture("schwab.ordinary", "schwab", "GET", "/api/modules/investment/schwab", "", 200)
	capture("telegram.recipient", "telegramRecipient", "GET", "/api/settings/telegram/recipient", "", 200)
	capture("telegram.recipient.saved", "saved", "PUT", "/api/settings/telegram/recipient", `{"enabled":false}`, 200)
	capture("users.forbidden", "error", "GET", "/api/admin/users", "", 403)
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
