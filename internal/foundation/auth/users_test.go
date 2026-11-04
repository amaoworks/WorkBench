package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"workbench/internal/contracts"
)

func createTestUser(t *testing.T, h http.Handler, admin tokenResponse, username, role string) userView {
	t.Helper()
	w := nativeRequest(h, "POST", "/api/admin/users", map[string]string{"username": username, "displayName": username, "role": role, "password": testPassword}, admin.AccessToken)
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body)
	}
	var result struct {
		User            userView `json:"user"`
		InitialPassword string   `json:"initialPassword"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.User.ID == "" || result.InitialPassword != testPassword || !result.User.MustChangePassword {
		t.Fatalf("create response: %s, %v", w.Body, err)
	}
	return result.User
}

func loginUser(t *testing.T, h http.Handler, username, password string) tokenResponse {
	t.Helper()
	w := nativeRequest(h, "POST", "/api/auth/token", map[string]string{"username": username, "password": password, "deviceName": "device " + username, "platform": "other"}, "")
	if w.Code != 200 {
		t.Fatalf("login %s: %d %s", username, w.Code, w.Body)
	}
	var result tokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func changeTestUserPassword(t *testing.T, h http.Handler, login tokenResponse) {
	t.Helper()
	w := nativeRequest(h, "PUT", "/api/settings/password", map[string]string{"currentPassword": testPassword, "newPassword": "Changed-Pass123"}, login.AccessToken)
	if w.Code != 200 {
		t.Fatalf("change own password: %d %s", w.Code, w.Body)
	}
}

func TestUserManagementAndIsolation(t *testing.T) {
	s, h := tokenService(t, false)
	admin := nativeLogin(t, h)
	alice := createTestUser(t, h, admin, "alice", "user")
	bob := createTestUser(t, h, admin, "bob", "user")
	aInitial := loginUser(t, h, "alice", testPassword)
	bInitial := loginUser(t, h, "bob", testPassword)
	if w := nativeRequest(h, "GET", "/protected", nil, aInitial.AccessToken); w.Code != 403 || !strings.Contains(w.Body.String(), "password_change_required") {
		t.Fatalf("initial password gate: %d %s", w.Code, w.Body)
	}
	changeTestUserPassword(t, h, aInitial)
	if w := nativeRequest(h, "PUT", "/api/settings/password", map[string]string{"currentPassword": testPassword, "newPassword": "Changed-Pass123"}, bInitial.AccessToken); w.Code != 200 {
		t.Fatalf("Alice change revoked Bob: %d %s", w.Code, w.Body)
	}
	a := loginUser(t, h, "alice", "Changed-Pass123")
	b := loginUser(t, h, "bob", "Changed-Pass123")
	for _, tc := range []struct {
		token tokenResponse
		id    string
	}{{a, alice.ID}, {b, bob.ID}, {admin, contracts.LegacyUserID}} {
		claims, err := s.validateAccess(context.Background(), tc.token.AccessToken)
		if err != nil || claims.Subject != tc.id {
			t.Fatalf("subject = %v, %v", claims, err)
		}
	}
	if w := nativeRequest(h, "GET", "/api/admin/users", nil, a.AccessToken); w.Code != 403 {
		t.Fatalf("nonadmin list: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "POST", "/api/admin/users", map[string]string{"username": "bad"}, a.AccessToken); w.Code != 403 {
		t.Fatalf("nonadmin create: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "PATCH", "/api/admin/users/"+bob.ID, map[string]string{"role": "admin"}, a.AccessToken); w.Code != 403 {
		t.Fatalf("nonadmin promote: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "POST", "/api/admin/users/"+bob.ID+"/password", map[string]string{}, a.AccessToken); w.Code != 403 {
		t.Fatalf("nonadmin reset: %d %s", w.Code, w.Body)
	}
	w := nativeRequest(h, "GET", "/api/auth/sessions", nil, a.AccessToken)
	if w.Code != 200 || strings.Contains(w.Body.String(), b.SessionID) || !strings.Contains(w.Body.String(), a.SessionID) {
		t.Fatalf("session isolation: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "DELETE", "/api/auth/sessions/"+b.SessionID, nil, a.AccessToken); w.Code != 404 {
		t.Fatalf("cross-user device revoke: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "POST", "/api/auth/logout-all", nil, a.AccessToken); w.Code != 200 {
		t.Fatalf("logout all: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "GET", "/protected", nil, a.AccessToken); w.Code != 401 {
		t.Fatalf("own logout did not revoke: %d %s", w.Code, w.Body)
	}
	for _, token := range []string{admin.AccessToken, b.AccessToken} {
		if w := nativeRequest(h, "GET", "/protected", nil, token); w.Code != 204 {
			t.Fatalf("other user logged out: %d %s", w.Code, w.Body)
		}
	}
	if w := nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": b.RefreshToken}, ""); w.Code != 200 {
		t.Fatalf("other user's refresh revoked: %d %s", w.Code, w.Body)
	}
	w = nativeRequest(h, "GET", "/api/admin/users", nil, admin.AccessToken)
	if w.Code != 200 || strings.Contains(w.Body.String(), "password_hash") || strings.Contains(w.Body.String(), "initialPassword") || !strings.Contains(w.Body.String(), "alice") {
		t.Fatalf("safe users list: %d %s", w.Code, w.Body)
	}
}

func TestDisableResetAndCurrentRoles(t *testing.T) {
	s, h := tokenService(t, false)
	admin := nativeLogin(t, h)
	u := createTestUser(t, h, admin, "alice", "user")
	changeTestUserPassword(t, h, loginUser(t, h, "alice", testPassword))
	a := loginUser(t, h, "alice", "Changed-Pass123")
	if w := nativeRequest(h, "PATCH", "/api/admin/users/"+u.ID, map[string]string{"role": "admin"}, admin.AccessToken); w.Code != 200 {
		t.Fatalf("promote: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "GET", "/api/admin/users", nil, a.AccessToken); w.Code != 200 {
		t.Fatalf("role not refreshed: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "PATCH", "/api/admin/users/"+u.ID, map[string]string{"role": "user"}, admin.AccessToken); w.Code != 200 {
		t.Fatalf("demote: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "GET", "/api/admin/users", nil, a.AccessToken); w.Code != 403 {
		t.Fatalf("stale role accepted: %d %s", w.Code, w.Body)
	}
	grant := nativeRequest(h, "POST", "/api/auth/browser-grant", map[string]any{}, a.AccessToken)
	var ticket struct {
		URLPath string `json:"urlPath"`
	}
	_ = json.Unmarshal(grant.Body.Bytes(), &ticket)
	if grant.Code != 200 {
		t.Fatal(grant.Body)
	}
	if w := nativeRequest(h, "PATCH", "/api/admin/users/"+u.ID, map[string]string{"status": "disabled"}, admin.AccessToken); w.Code != 200 {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	if active, err := s.IsUserActive(context.Background(), u.ID); active || err != nil {
		t.Fatalf("active disabled: %v %v", active, err)
	}
	ids, err := s.ActiveUserIDs(context.Background())
	if err != nil || len(ids) != 1 || ids[0] != contracts.LegacyUserID {
		t.Fatalf("active directory: %v %v", ids, err)
	}
	for _, w := range []*httptest.ResponseRecorder{
		nativeRequest(h, "GET", "/protected", nil, a.AccessToken),
		nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": a.RefreshToken}, ""),
		nativeRequest(h, "GET", ticket.URLPath, nil, ""),
		nativeRequest(h, "POST", "/api/auth/token", map[string]string{"username": "alice", "password": "Changed-Pass123", "deviceName": "d", "platform": "other"}, ""),
	} {
		if w.Code != 401 {
			t.Fatalf("disabled user accepted: %d %s", w.Code, w.Body)
		}
	}
	if w := nativeRequest(h, "PATCH", "/api/admin/users/"+u.ID, map[string]string{"status": "active"}, admin.AccessToken); w.Code != 200 {
		t.Fatalf("enable: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "GET", "/protected", nil, a.AccessToken); w.Code != 401 {
		t.Fatal("reenabling revived old token")
	}
	a = loginUser(t, h, "alice", "Changed-Pass123")
	reset := nativeRequest(h, "POST", "/api/admin/users/"+u.ID+"/password", map[string]string{}, admin.AccessToken)
	var password struct {
		InitialPassword string `json:"initialPassword"`
	}
	_ = json.Unmarshal(reset.Body.Bytes(), &password)
	if reset.Code != 200 || password.InitialPassword == "" {
		t.Fatalf("reset: %d %s", reset.Code, reset.Body)
	}
	if w := nativeRequest(h, "GET", "/protected", nil, a.AccessToken); w.Code != 401 {
		t.Fatal("reset left old session active")
	}
	s.passwordLimit.now = func() time.Time { return time.Now().Add(time.Minute) }
	next := loginUser(t, h, "alice", password.InitialPassword)
	if !next.MustChangePassword {
		t.Fatal("reset did not force password change")
	}
	if w := nativeRequest(h, "GET", "/protected", nil, admin.AccessToken); w.Code != 204 {
		t.Fatal("reset revoked admin session")
	}
}

func TestLastActiveAdminAndConcurrentDemotion(t *testing.T) {
	_, h := tokenService(t, false)
	admin := nativeLogin(t, h)
	for _, input := range []map[string]string{{"role": "user"}, {"status": "disabled"}} {
		w := nativeRequest(h, "PATCH", "/api/admin/users/owner", input, admin.AccessToken)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "last_admin") {
			t.Fatalf("last admin: %d %s", w.Code, w.Body)
		}
	}
	other := createTestUser(t, h, admin, "second", "admin")
	changeTestUserPassword(t, h, loginUser(t, h, "second", testPassword))
	second := loginUser(t, h, "second", "Changed-Pass123")
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for _, tc := range []struct{ id, token string }{{"owner", admin.AccessToken}, {other.ID, second.AccessToken}} {
		wg.Go(func() {
			results <- nativeRequest(h, "PATCH", "/api/admin/users/"+tc.id, map[string]string{"role": "user"}, tc.token).Code
		})
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for status := range results {
		counts[status]++
	}
	if counts[200] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent demotion statuses: %v", counts)
	}
}

func TestLocalModeCannotBypassMultiUser(t *testing.T) {
	db := openAuthDatabase(t)
	local, err := New(context.Background(), db.SQL(), Config{Mode: ModeLocal, ListenAddress: "127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	var principal contracts.Principal
	local.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, _ = contracts.CurrentPrincipal(r.Context())
		w.WriteHeader(204)
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if principal.UserID != "owner" || principal.Role != contracts.RoleAdmin || principal.SessionID != "local" {
		t.Fatalf("local principal: %+v", principal)
	}
	req := httptest.NewRequest("POST", "/api/admin/users", strings.NewReader(`{"username":"alice"}`))
	req = req.WithContext(contracts.WithPrincipal(req.Context(), principal))
	w := httptest.NewRecorder()
	local.CreateUserHandler(w, req)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "password_mode_required") {
		t.Fatalf("local user create: %d %s", w.Code, w.Body)
	}
	if _, err := db.SQL().Exec(`INSERT INTO auth_users(id,username,display_name,password_hash,role,status,credential_version,must_change_password,created_at,updated_at) VALUES ('disabled','disabled','Disabled','','user','disabled',1,0,1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), db.SQL(), Config{Mode: ModeLocal, ListenAddress: "127.0.0.1:8080"}); err == nil {
		t.Fatal("local startup bypassed disabled multiuser")
	}
	w = httptest.NewRecorder()
	local.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 401 {
		t.Fatalf("running local bypassed multiuser: %d", w.Code)
	}
}

func TestUsernameRequiredForWebAndNativeLogin(t *testing.T) {
	s, h := tokenService(t, false)
	if w := nativeRequest(h, "POST", "/api/auth/token", map[string]string{"password": testPassword, "deviceName": "d", "platform": "other"}, ""); w.Code != 401 {
		t.Fatalf("native omitted username: %d %s", w.Code, w.Body)
	}
	web := s.LoadAndSave(http.HandlerFunc(s.LoginHandler))
	for _, body := range []string{`{"password":"` + testPassword + `"}`, `{"username":"missing","password":"` + testPassword + `"}`} {
		w := httptest.NewRecorder()
		web.ServeHTTP(w, httptest.NewRequest("POST", "/api/auth/login", bytes.NewBufferString(body)))
		if w.Code != 401 {
			t.Fatalf("web invalid username: %d %s", w.Code, w.Body)
		}
	}
}
