package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workbench/internal/foundation/auth"
)

type userClient struct {
	token   string
	cookies []*http.Cookie
}

func newMultiUserApp(t *testing.T, configs ...Config) *App {
	t.Helper()
	cfg := Config{
		ListenAddress: "127.0.0.1:8080", DataPath: filepath.Join(t.TempDir(), "data.db"),
		ShutdownGrace: time.Second,
	}
	if len(configs) != 0 {
		cfg = configs[0]
	}
	cfg.AuthMode, cfg.Password = auth.ModePassword, "Adminpass1!"
	a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	if _, err := a.DB().Exec("UPDATE auth_users SET must_change_password = 0 WHERE id = 'owner'"); err != nil {
		t.Fatal(err)
	}
	return a
}

func loginUser(t *testing.T, a *App, username, password string) userClient {
	t.Helper()
	token, cookies := csrf(t, a.Handler())
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	result := request(t, a.Handler(), http.MethodPost, "/api/auth/login", body, token, cookies)
	if result.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", username, result.Code, result.Body.String())
	}
	return userClient{token: token, cookies: append(cookies, result.Result().Cookies()...)}
}

func createUser(t *testing.T, a *App, admin userClient, username string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "displayName": username, "role": "user", "password": "Userpass1!"})
	result := request(t, a.Handler(), http.MethodPost, "/api/admin/users", body, admin.token, admin.cookies)
	if result.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", result.Code, result.Body.String())
	}
	var created struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &created); err != nil || created.User.ID == "" {
		t.Fatalf("invalid created user: %s", result.Body.String())
	}
	if _, err := a.DB().Exec("UPDATE auth_users SET must_change_password = 0 WHERE id = ?", created.User.ID); err != nil {
		t.Fatal(err)
	}
	return created.User.ID
}

func TestMultiUserAdministrationAndPersonalSettings(t *testing.T) {
	assertMultiUserAdministrationAndPersonalSettings(t, newMultiUserApp(t))
}

func TestPostgresMultiUserAdministrationAndPersonalSettings(t *testing.T) {
	assertMultiUserAdministrationAndPersonalSettings(t, newMultiUserApp(t, postgresTestConfig(t)))
}

func assertMultiUserAdministrationAndPersonalSettings(t *testing.T, a *App) {
	t.Helper()
	admin := loginUser(t, a, "admin", "Adminpass1!")
	aliceID := createUser(t, a, admin, "alice")
	createUser(t, a, admin, "bob")
	alice := loginUser(t, a, "alice", "Userpass1!")
	bob := loginUser(t, a, "bob", "Userpass1!")

	adminRoutes := []struct{ method, path, body string }{
		{"GET", "/api/admin/users", ""},
		{"POST", "/api/admin/users", `{}`},
		{"PATCH", "/api/admin/users/owner", `{"role":"user"}`},
		{"POST", "/api/admin/users/owner/password", `{}`},
		{"PUT", "/api/modules/todo/enabled", `{"enabled":false}`},
		{"PUT", "/api/settings/ai", `{"enabled":false}`},
		{"POST", "/api/settings/ai/test", `{}`},
		{"PUT", "/api/settings/logging", `{"level":"debug"}`},
		{"POST", "/api/system/backup", `{}`},
		{"POST", "/api/auth/signing-key/rotate", `{}`},
		{"GET", "/api/settings/telegram", ""},
		{"PUT", "/api/modules/investment/futu", `{}`},
		{"POST", "/api/modules/investment/futu/disconnect", `{}`},
		{"PUT", "/api/modules/investment/overnight", `{"enabled":false}`},
		{"PUT", "/api/modules/investment/schwab", `{}`},
	}
	for _, route := range adminRoutes {
		result := request(t, a.Handler(), route.method, route.path, []byte(route.body), alice.token, alice.cookies)
		if result.Code != http.StatusForbidden {
			t.Errorf("non-admin %s %s = %d %s", route.method, route.path, result.Code, result.Body.String())
		}
	}
	for _, client := range []userClient{alice, bob} {
		result := request(t, a.Handler(), "GET", "/api/settings", nil, "", client.cookies)
		if result.Code != 200 {
			t.Fatalf("personal settings: %d %s", result.Code, result.Body.String())
		}
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(result.Body.Bytes(), &settings); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"ai", "logging", "deployment"} {
			if _, ok := settings[key]; ok {
				t.Errorf("ordinary settings leaked %s", key)
			}
		}
	}
	changed := request(t, a.Handler(), "PUT", "/api/settings/appearance", []byte(`{"theme":"dark","motion":"reduced","palette":"claude"}`), alice.token, alice.cookies)
	if changed.Code != 200 {
		t.Fatalf("save appearance: %s", changed.Body.String())
	}
	for _, tc := range []struct {
		client userClient
		theme  string
	}{{alice, "dark"}, {bob, "system"}, {admin, "system"}} {
		result := request(t, a.Handler(), "GET", "/api/settings", nil, "", tc.client.cookies)
		var settings struct {
			Appearance Appearance `json:"appearance"`
		}
		if json.Unmarshal(result.Body.Bytes(), &settings) != nil || settings.Appearance.Theme != tc.theme {
			t.Fatalf("personal appearance isolation: %s, want %s", result.Body.String(), tc.theme)
		}
	}

	blocked := request(t, a.Handler(), "PATCH", "/api/admin/users/owner", []byte(`{"role":"user"}`), admin.token, admin.cookies)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("last administrator demotion: %d %s", blocked.Code, blocked.Body.String())
	}
	disabled := request(t, a.Handler(), "PATCH", "/api/admin/users/"+aliceID, []byte(`{"status":"disabled"}`), admin.token, admin.cookies)
	if disabled.Code != 200 {
		t.Fatalf("disable user: %d %s", disabled.Code, disabled.Body.String())
	}
	for _, tc := range []struct {
		client userClient
		status int
	}{{alice, 401}, {bob, 200}, {admin, 200}} {
		result := request(t, a.Handler(), "GET", "/api/modules/todo/tasks", nil, "", tc.client.cookies)
		if result.Code != tc.status {
			t.Errorf("disabled-user isolation: %d %s, want %d", result.Code, result.Body.String(), tc.status)
		}
	}
}

func TestMultiUserPrivateTodoAndOwnPasswordChange(t *testing.T) {
	assertMultiUserPrivateTodoAndOwnPasswordChange(t, newMultiUserApp(t))
}

func TestPostgresMultiUserPrivateTodoAndOwnPasswordChange(t *testing.T) {
	assertMultiUserPrivateTodoAndOwnPasswordChange(t, newMultiUserApp(t, postgresTestConfig(t)))
}

func assertMultiUserPrivateTodoAndOwnPasswordChange(t *testing.T, a *App) {
	t.Helper()
	admin := loginUser(t, a, "admin", "Adminpass1!")
	createUser(t, a, admin, "alice")
	createUser(t, a, admin, "bob")
	alice := loginUser(t, a, "alice", "Userpass1!")
	bob := loginUser(t, a, "bob", "Userpass1!")
	created := request(t, a.Handler(), "POST", "/api/modules/todo/tasks", []byte(`{"title":"Alice private task"}`), alice.token, alice.cookies)
	if created.Code != 201 {
		t.Fatalf("create task: %d %s", created.Code, created.Body.String())
	}
	var task struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &task); err != nil || task.ID == "" {
		t.Fatalf("task id: %s", created.Body.String())
	}
	for _, client := range []userClient{bob, admin} {
		listed := request(t, a.Handler(), "GET", "/api/modules/todo/tasks", nil, "", client.cookies)
		if listed.Code != 200 || strings.Contains(listed.Body.String(), "Alice private task") {
			t.Errorf("private list: %d %s", listed.Code, listed.Body.String())
		}
		deleted := request(t, a.Handler(), "DELETE", "/api/modules/todo/tasks/"+task.ID, nil, client.token, client.cookies)
		if deleted.Code != 404 {
			t.Errorf("cross-owner delete: %d %s", deleted.Code, deleted.Body.String())
		}
	}
	changed := request(t, a.Handler(), "PUT", "/api/settings/password", []byte(`{"currentPassword":"Userpass1!","newPassword":"Changedpass2!"}`), alice.token, alice.cookies)
	if changed.Code != 200 {
		t.Fatalf("change own password: %d %s", changed.Code, changed.Body.String())
	}
	for _, tc := range []struct {
		client userClient
		status int
	}{{alice, 401}, {bob, 200}, {admin, 200}} {
		result := request(t, a.Handler(), "GET", "/api/modules/todo/tasks", nil, "", tc.client.cookies)
		if result.Code != tc.status {
			t.Errorf("own-password session isolation: %d %s, want %d", result.Code, result.Body.String(), tc.status)
		}
	}
	alice = loginUser(t, a, "alice", "Changedpass2!")
	listed := request(t, a.Handler(), "GET", "/api/modules/todo/tasks", nil, "", alice.cookies)
	if listed.Code != 200 || !strings.Contains(listed.Body.String(), "Alice private task") {
		t.Fatalf("private task lost: %d %s", listed.Code, listed.Body.String())
	}
}
