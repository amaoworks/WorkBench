package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/auth"
	dbsqlc "workbench/internal/foundation/database/sqlc"
)

func TestJWTApplicationAPIs(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			cfg := Config{ListenAddress: "127.0.0.1:8080", DataPath: filepath.Join(t.TempDir(), "data.db"), AuthMode: auth.ModePassword, ShutdownGrace: time.Second}
			if driver == "postgres" {
				cfg = postgresTestConfig(t)
				cfg.AuthMode = auth.ModePassword
			}
			cfg.Password = "Correct horse battery staple1"
			cfg.MobilePush = MobilePushDeployment{
				Enabled: true, APNsP8File: writeTestAPNsKey(t), APNsTeamID: "TEAM", APNsKeyID: "KEY", APNsBundleID: "com.example.app",
			}
			a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			call := func(method, path, body, token string, headers ...map[string]string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, bytes.NewBufferString(body))
				r.Header.Set("Content-Type", "application/json")
				if token != "" {
					r.Header.Set("Authorization", "Bearer "+token)
				}
				for _, values := range headers {
					for key, value := range values {
						r.Header.Set(key, value)
					}
				}
				w := httptest.NewRecorder()
				a.Handler().ServeHTTP(w, r)
				return w
			}
			type tokens struct {
				AccessToken        string `json:"accessToken"`
				RefreshToken       string `json:"refreshToken"`
				SessionID          string `json:"sessionId"`
				MustChangePassword bool   `json:"mustChangePassword"`
			}
			login := func(password string) tokens {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"password": password, "deviceName": "Integration phone", "platform": "ios"})
				w := call("POST", "/api/auth/token", string(body), "")
				var result tokens
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.AccessToken == "" {
					t.Fatalf("login: %d %s", w.Code, w.Body)
				}
				if len(w.Result().Cookies()) != 0 {
					t.Fatal("native login set cookies")
				}
				return result
			}
			initial := login(cfg.Password)
			if !initial.MustChangePassword {
				t.Fatal("first login must require change")
			}
			if w := call("GET", "/api/modules", "", initial.AccessToken); w.Code != 403 {
				t.Fatalf("first access: %d %s", w.Code, w.Body)
			}
			const changed = "Changed horse battery staple2!"
			body, _ := json.Marshal(map[string]string{"currentPassword": cfg.Password, "newPassword": changed})
			if w := call("PUT", "/api/settings/password", string(body), initial.AccessToken); w.Code != 200 {
				t.Fatalf("password: %d %s", w.Code, w.Body)
			}
			first := login(changed)
			second := login(changed)
			browserLoginBody, _ := json.Marshal(map[string]string{"password": changed})
			browserLogin := call("POST", "/api/auth/login", string(browserLoginBody), first.AccessToken)
			var browserLoginError struct {
				Code string `json:"code"`
			}
			if browserLogin.Code != 400 || json.Unmarshal(browserLogin.Body.Bytes(), &browserLoginError) != nil || browserLoginError.Code != "invalid_token_request" || len(browserLogin.Result().Cookies()) != 0 {
				t.Fatalf("browser login accepted Bearer: %d %s", browserLogin.Code, browserLogin.Body)
			}
			if first.MustChangePassword {
				t.Fatal("changed password still restricted")
			}
			pushToken := `{"platform":"ios","pushToken":"integration-old-token"}`
			updatedPushToken := `{"platform":"ios","pushToken":"integration-new-token"}`
			for _, registration := range []struct{ method, body string }{{"POST", pushToken}, {"PUT", updatedPushToken}, {"DELETE", pushToken}} {
				if w := call(registration.method, "/api/mobile/push-token", registration.body, first.AccessToken); w.Code != 204 || w.Body.Len() != 0 {
					t.Fatalf("push token %s: %d %s", registration.method, w.Code, w.Body)
				}
			}
			activePushTokens := func() []dbsqlc.ListActiveMobilePushTokensRow {
				t.Helper()
				now := time.Now().UnixMilli()
				targets, err := dbsqlc.New(a.DB()).ListActiveMobilePushTokens(context.Background(), dbsqlc.ListActiveMobilePushTokensParams{
					Now: now, NotificationCreatedAt: now,
				})
				if err != nil {
					t.Fatal(err)
				}
				return targets
			}
			if targets := activePushTokens(); len(targets) != 1 || targets[0].Token != "integration-new-token" {
				t.Fatalf("push token replacement lost: %+v", targets)
			}
			if w := call("DELETE", "/api/mobile/push-token", updatedPushToken, first.AccessToken); w.Code != 204 {
				t.Fatalf("push token delete: %d %s", w.Code, w.Body)
			}
			if targets := activePushTokens(); len(targets) != 0 {
				t.Fatalf("deleted push token remains active: %+v", targets)
			}
			if w := call("POST", "/api/mobile/push-token", updatedPushToken, first.AccessToken); w.Code != 204 {
				t.Fatalf("push token register: %d %s", w.Code, w.Body)
			}
			if w := call("POST", "/api/modules/todo/tasks", `{"title":"Native task"}`, first.AccessToken); w.Code != 201 {
				t.Fatalf("native write without csrf: %d %s", w.Code, w.Body)
			}
			idempotency := map[string]string{"Idempotency-Key": "native-integration-request"}
			created := call("POST", "/api/modules/todo/tasks", `{"title":"Retry task"}`, first.AccessToken, idempotency)
			repeated := call("POST", "/api/modules/todo/tasks", `{"title":"Retry task"}`, first.AccessToken, idempotency)
			if created.Code != 201 || repeated.Code != 201 || created.Body.String() != repeated.Body.String() {
				t.Fatalf("idempotent create failed: %d/%d", created.Code, repeated.Code)
			}
			if w := call("POST", "/api/modules/todo/tasks", `{"title":"Different task"}`, first.AccessToken, idempotency); w.Code != 409 {
				t.Fatalf("idempotency conflict: %d %s", w.Code, w.Body)
			}
			var task struct {
				ID       string `json:"id"`
				Revision int64  `json:"revision"`
			}
			if json.Unmarshal(created.Body.Bytes(), &task) != nil || task.Revision != 1 {
				t.Fatal("missing task revision")
			}
			if w := call("PATCH", "/api/modules/todo/tasks/"+task.ID, `{"title":"Updated","revision":1}`, first.AccessToken); w.Code != 200 {
				t.Fatalf("update: %d %s", w.Code, w.Body)
			}
			if w := call("PATCH", "/api/modules/todo/tasks/"+task.ID, `{"title":"Stale","revision":1}`, first.AccessToken); w.Code != 409 {
				t.Fatalf("revision conflict: %d %s", w.Code, w.Body)
			}
			grant := call("POST", "/api/auth/browser-grant", "{}", first.AccessToken)
			var ticket struct {
				URLPath string `json:"urlPath"`
			}
			if grant.Code != 200 || json.Unmarshal(grant.Body.Bytes(), &ticket) != nil {
				t.Fatalf("browser grant: %d %s", grant.Code, grant.Body)
			}
			webview := call("GET", ticket.URLPath, "", "")
			if webview.Code != 303 || webview.Header().Get("Location") != "/investment/terminal" {
				t.Fatalf("browser consume: %d %s", webview.Code, webview.Body)
			}
			viewCookies := webview.Result().Cookies()
			if w := request(t, a.Handler(), "GET", "/api/modules", nil, "", viewCookies); w.Code != 200 {
				t.Fatalf("WebView access: %d %s", w.Code, w.Body)
			}
			if w := call("GET", ticket.URLPath, "", ""); w.Code != 401 {
				t.Fatal("WebView ticket replay succeeded")
			}
			csrfToken, cookies := csrf(t, a.Handler())
			browserBody, _ := json.Marshal(map[string]string{"password": changed})
			browser := request(t, a.Handler(), "POST", "/api/auth/login", browserBody, csrfToken, cookies)
			if browser.Code != 200 {
				t.Fatalf("browser login: %d %s", browser.Code, browser.Body)
			}
			cookies = append(cookies, browser.Result().Cookies()...)
			if w := request(t, a.Handler(), "GET", "/api/modules", nil, "", cookies); w.Code != 200 {
				t.Fatalf("browser access: %d %s", w.Code, w.Body)
			}
			w := call("GET", "/api/auth/sessions", "", first.AccessToken)
			var sessions struct {
				Items []struct {
					ID      string `json:"id"`
					Current bool   `json:"current"`
				} `json:"items"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &sessions) != nil || len(sessions.Items) != 4 {
				t.Fatalf("sessions: %d %s", w.Code, w.Body)
			}
			if w := call("DELETE", "/api/auth/sessions/"+second.SessionID, "", first.AccessToken); w.Code != 200 {
				t.Fatalf("revoke: %d %s", w.Code, w.Body)
			}
			if w := call("GET", "/api/modules", "", second.AccessToken); w.Code != 401 {
				t.Fatal("revoked device still works")
			}
			refresh, _ := json.Marshal(map[string]string{"refreshToken": first.RefreshToken})
			w = call("POST", "/api/auth/token/refresh", string(refresh), "")
			var renewed tokens
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &renewed) != nil {
				t.Fatalf("refresh: %d %s", w.Code, w.Body)
			}
			if w := call("POST", "/api/auth/logout-all", "{}", renewed.AccessToken); w.Code != 200 {
				t.Fatalf("logout: %d %s", w.Code, w.Body)
			}
			if w := request(t, a.Handler(), "GET", "/api/modules", nil, "", cookies); w.Code != 401 {
				t.Fatal("logout all left browser session")
			}
			if w := request(t, a.Handler(), "GET", "/api/modules", nil, "", viewCookies); w.Code != 401 {
				t.Fatal("logout all left WebView session")
			}
			if w := call("GET", "/api/modules", "", renewed.AccessToken); w.Code != 401 {
				t.Fatal("logout all left bearer session")
			}
			if targets := activePushTokens(); len(targets) != 0 {
				t.Fatalf("logout all left active push tokens: %+v", targets)
			}
			latestRefresh, _ := json.Marshal(map[string]string{"refreshToken": renewed.RefreshToken})
			if w := call("POST", "/api/auth/token/refresh", string(latestRefresh), ""); w.Code != 401 {
				t.Fatal("logout all left refresh")
			}
			if err := maintenanceJob(a.DB()).Handler(context.Background(), contracts.JobRun{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
