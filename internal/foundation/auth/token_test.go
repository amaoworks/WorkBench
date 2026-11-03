package auth

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	dbsqlc "workbench/internal/foundation/database/sqlc"
)

func tokenService(t *testing.T, initial bool) (*Service, http.Handler) {
	t.Helper()
	db := openAuthDatabase(t)
	s, err := New(context.Background(), db.SQL(), Config{Mode: ModePassword, ListenAddress: "127.0.0.1:8080", InitialPassword: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if !initial {
		markInitialPasswordChanged(t, db)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/token", s.TokenHandler)
	mux.HandleFunc("POST /api/auth/token/refresh", s.RefreshHandler)
	mux.HandleFunc("POST /api/auth/login", s.LoginHandler)
	mux.HandleFunc("GET /api/auth/csrf", s.CSRFTokenHandler)
	mux.HandleFunc("GET /api/auth/browser-session", s.BrowserSessionHandler)
	mux.Handle("POST /api/auth/browser-grant", s.Require(http.HandlerFunc(s.BrowserGrantHandler)))
	mux.Handle("GET /api/auth/sessions", s.Require(http.HandlerFunc(s.SessionsHandler)))
	mux.Handle("DELETE /api/auth/sessions/{id}", s.Require(http.HandlerFunc(s.DeleteSessionHandler)))
	mux.Handle("POST /api/auth/token/revoke", s.Require(http.HandlerFunc(s.RevokeTokenHandler)))
	mux.Handle("POST /api/auth/logout-all", s.Require(http.HandlerFunc(s.LogoutAllHandler)))
	mux.Handle("POST /api/auth/signing-key/rotate", s.Require(http.HandlerFunc(s.RotateSigningKeyHandler)))
	mux.Handle("PUT /api/settings/password", s.Require(http.HandlerFunc(s.ChangePasswordHandler)))
	mux.Handle("GET /protected", s.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })))
	return s, s.Security(s.LoadAndSave(mux))
}

func nativeRequest(h http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	var data []byte
	if body != nil {
		data, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func nativeLogin(t *testing.T, h http.Handler) tokenResponse {
	t.Helper()
	w := nativeRequest(h, "POST", "/api/auth/token", map[string]string{"password": testPassword, "deviceName": "Test iPhone", "platform": "ios"}, "")
	if w.Code != 200 {
		t.Fatalf("native login: %d %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("token endpoint must not set cookies or cache credentials")
	}
	var result tokenResponse
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || result.AccessToken == "" || result.RefreshToken == "" {
		t.Fatal("missing token response")
	}
	return result
}

func TestTokenLoginRefreshAndReplayRevokesDevice(t *testing.T) {
	_, h := tokenService(t, false)
	login := nativeLogin(t, h)
	if w := nativeRequest(h, "GET", "/protected", nil, login.AccessToken); w.Code != 204 {
		t.Fatalf("protected: %d %s", w.Code, w.Body)
	}
	w := nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": login.RefreshToken}, "")
	if w.Code != 200 {
		t.Fatalf("refresh: %d %s", w.Code, w.Body)
	}
	var next tokenResponse
	_ = json.Unmarshal(w.Body.Bytes(), &next)
	if next.RefreshToken == login.RefreshToken || next.SessionID != login.SessionID {
		t.Fatal("refresh must rotate within the same device session")
	}
	w = nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": login.RefreshToken}, "")
	if w.Code != 401 {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "GET", "/protected", nil, next.AccessToken); w.Code != 401 {
		t.Fatal("replay left issued access token valid")
	}
	if w := nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": next.RefreshToken}, ""); w.Code != 401 {
		t.Fatal("replay left refresh family valid")
	}
}

func TestTokenConcurrentRefreshDetectsReuse(t *testing.T) {
	_, h := tokenService(t, false)
	login := nativeLogin(t, h)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			results <- nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": login.RefreshToken}, "").Code
		})
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for status := range results {
		counts[status]++
	}
	if counts[200] != 1 || counts[401] != 1 {
		t.Fatalf("concurrent refresh statuses: %v", counts)
	}
	if w := nativeRequest(h, "GET", "/protected", nil, login.AccessToken); w.Code != 401 {
		t.Fatal("reuse must revoke device")
	}
}

func TestTokenFirstPasswordChangeRevokesAllDevices(t *testing.T) {
	_, h := tokenService(t, true)
	first := nativeLogin(t, h)
	second := nativeLogin(t, h)
	if !first.MustChangePassword {
		t.Fatal("initial login must require password change")
	}
	if w := nativeRequest(h, "GET", "/protected", nil, first.AccessToken); w.Code != 403 {
		t.Fatalf("initial protected: %d", w.Code)
	}
	w := nativeRequest(h, "PUT", "/api/settings/password", map[string]string{"currentPassword": testPassword, "newPassword": "Changed horse battery2!"}, first.AccessToken)
	if w.Code != 200 {
		t.Fatalf("change password: %d %s", w.Code, w.Body)
	}
	for _, token := range []tokenResponse{first, second} {
		if w := nativeRequest(h, "GET", "/protected", nil, token.AccessToken); w.Code != 401 {
			t.Fatal("password change left access valid")
		}
		if w := nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": token.RefreshToken}, ""); w.Code != 401 {
			t.Fatal("password change left refresh valid")
		}
	}
}

func TestTokenDeviceRevocationAndSigningKeyRotation(t *testing.T) {
	s, h := tokenService(t, false)
	a := nativeLogin(t, h)
	b := nativeLogin(t, h)
	w := nativeRequest(h, "GET", "/api/auth/sessions", nil, a.AccessToken)
	var page struct {
		Items []sessionView `json:"items"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Items) != 2 {
		t.Fatalf("device list: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "POST", "/api/auth/signing-key/rotate", nil, a.AccessToken); w.Code != 200 {
		t.Fatalf("rotate: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "GET", "/protected", nil, a.AccessToken); w.Code != 204 {
		t.Fatal("rotation should allow existing short-lived access")
	}
	if w := nativeRequest(h, "DELETE", "/api/auth/sessions/"+b.SessionID, nil, a.AccessToken); w.Code != 200 {
		t.Fatalf("revoke: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "GET", "/protected", nil, b.AccessToken); w.Code != 401 {
		t.Fatal("revoked access accepted")
	}
	if w := nativeRequest(h, "GET", "/protected", nil, a.AccessToken); w.Code != 204 {
		t.Fatal("other device revoked")
	}
	// Restart uses the persisted active signing key and device state.
	restarted, err := New(context.Background(), s.db, Config{Mode: ModePassword, ListenAddress: "127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.validateAccess(context.Background(), a.AccessToken); err != nil {
		t.Fatalf("restart lost login: %v", err)
	}
	if w := nativeRequest(h, "POST", "/api/auth/logout-all", nil, a.AccessToken); w.Code != 200 {
		t.Fatal("logout all failed")
	}
	if w := nativeRequest(h, "GET", "/protected", nil, a.AccessToken); w.Code != 401 {
		t.Fatal("logout all left access valid")
	}
}

func TestTokenExpiryAndRefreshExpiry(t *testing.T) {
	s, h := tokenService(t, false)
	now := time.Now().UTC().Truncate(time.Second)
	s.now = func() time.Time { return now }
	login := nativeLogin(t, h)
	expires := now.Add(deviceLifetime)
	refresh := func(current tokenResponse) tokenResponse {
		t.Helper()
		w := nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": current.RefreshToken}, "")
		var renewed tokenResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &renewed) != nil {
			t.Fatalf("refresh: %d %s", w.Code, w.Body)
		}
		if !renewed.RefreshExpiresAt.Equal(expires) || renewed.RefreshToken == current.RefreshToken || renewed.SessionID != login.SessionID {
			t.Fatal("refresh must rotate tokens without extending the device lifetime")
		}
		return renewed
	}
	now = now.Add(accessLifetime)
	if w := nativeRequest(h, "GET", "/protected", nil, login.AccessToken); w.Code != 401 {
		t.Fatal("expired access accepted")
	}
	renewed := refresh(login)
	now = expires.Add(-time.Second)
	renewed = refresh(renewed)
	if renewed.ExpiresIn != 1 {
		t.Fatalf("access lifetime exceeds device expiry: %d", renewed.ExpiresIn)
	}
	if w := nativeRequest(h, "GET", "/protected", nil, renewed.AccessToken); w.Code != 204 {
		t.Fatalf("device expired early: %d %s", w.Code, w.Body)
	}
	now = expires
	if w := nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": renewed.RefreshToken}, ""); w.Code != 401 {
		t.Fatal("expired refresh accepted")
	}
	if w := nativeRequest(h, "GET", "/protected", nil, renewed.AccessToken); w.Code != 401 {
		t.Fatal("access accepted at device expiry")
	}
}

func TestBrowserLoginRejectsAuthorization(t *testing.T) {
	s, h := tokenService(t, false)
	login := nativeLogin(t, h)
	for _, authorization := range []string{"Bearer " + login.AccessToken, "Bearer invalid", ""} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/auth/login", strings.NewReader(`{"password":"`+testPassword+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", authorization)
		w := httptest.NewRecorder()
		tokens := s.passwordLimit.tokens
		h.ServeHTTP(w, r)
		var result struct {
			Code string `json:"code"`
		}
		if w.Code != 400 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Code != "invalid_token_request" {
			t.Fatalf("browser login accepted Authorization: %d %s", w.Code, w.Body)
		}
		if len(w.Result().Cookies()) != 0 || s.passwordLimit.tokens != tokens {
			t.Fatal("rejected browser login created cookies or spent a login token")
		}
	}
}

func TestTokenFirstLoginCanRevokeDevice(t *testing.T) {
	_, h := tokenService(t, true)
	login := nativeLogin(t, h)
	if !login.MustChangePassword {
		t.Fatal("initial login must require password change")
	}
	if w := nativeRequest(h, "POST", "/api/auth/token/revoke", nil, login.AccessToken); w.Code != 200 {
		t.Fatalf("initial device logout: %d %s", w.Code, w.Body)
	}
	if w := nativeRequest(h, "POST", "/api/auth/token/refresh", map[string]string{"refreshToken": login.RefreshToken}, ""); w.Code != 401 {
		t.Fatal("initial device logout left refresh valid")
	}
	if w := nativeRequest(h, "GET", "/protected", nil, login.AccessToken); w.Code != 401 {
		t.Fatal("initial device logout left access valid")
	}
}

func TestTokenSecurityDoesNotBypassBrowserCSRF(t *testing.T) {
	_, h := tokenService(t, false)
	login := nativeLogin(t, h)
	for _, headers := range []map[string]string{
		{"Cookie": "workbench_session=fake"},
		{"Content-Type": "text/plain"},
		{"Authorization": "Bearer " + login.AccessToken},
		{"Origin": "https://evil.invalid"},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/auth/token", strings.NewReader(`{"password":"`+testPassword+`","deviceName":"x","platform":"ios"}`))
		r.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code < 400 {
			t.Fatalf("unsafe token request accepted: %v", headers)
		}
	}
	if w := nativeRequest(h, "POST", "/api/auth/login", map[string]string{"password": testPassword}, ""); w.Code != 400 {
		t.Fatal("browser login skipped CSRF")
	}
	if w := nativeRequest(h, "POST", "/api/auth/logout-all", nil, "invalid"); w.Code != 401 {
		t.Fatal("invalid bearer fell through to browser session")
	}
	r := httptest.NewRequest("GET", "http://127.0.0.1:8080/protected", nil)
	r.Header.Set("Authorization", "Bearer "+login.AccessToken)
	r.AddCookie(&http.Cookie{Name: "workbench_session", Value: "other"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("ambiguous credentials accepted")
	}
	parts := strings.Split(login.AccessToken, ".")
	parts[2] = strings.Repeat("A", len(parts[2]))
	if w := nativeRequest(h, "GET", "/protected", nil, strings.Join(parts, ".")); w.Code != 401 {
		t.Fatal("forged signature accepted")
	}
	unsigned, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "owner"}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if w := nativeRequest(h, "GET", "/protected", nil, unsigned); w.Code != 401 {
		t.Fatal("unsigned token accepted")
	}
}

func TestRevokedBrowserSessionCannotBeResurrected(t *testing.T) {
	s, _ := tokenService(t, false)
	ctx := context.Background()
	const hash = "a-test-hash"
	if err := s.queries.InsertBrowserSession(ctx, dbsqlc.InsertBrowserSessionParams{ID: "browser", TokenHash: hash, CreatedAt: 1, LastSeenAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.sessionStore.Commit(hash, []byte("old-session"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.queries.RevokeBrowserSession(ctx, dbsqlc.RevokeBrowserSessionParams{TokenHash: hash, RevokedAt: sql.NullInt64{Int64: time.Now().UnixMilli(), Valid: true}}); err != nil {
		t.Fatal(err)
	}
	if err := s.sessionStore.Commit(hash, []byte("late-session"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.sessionStore.Find(hash); err != nil || found {
		t.Fatalf("revoked browser resurrected: found=%v err=%v", found, err)
	}
}
