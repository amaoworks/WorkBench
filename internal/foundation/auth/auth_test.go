package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	workbenchdb "workbench/internal/foundation/database"
)

const testPassword = "correct horse battery staple"

func TestPasswordMinimumCountsUnicodeCharacters(t *testing.T) {
	if _, err := hashPassword("四个汉字"); err == nil {
		t.Fatal("four Unicode characters passed the twelve-character minimum")
	}
	if _, err := hashPassword("十二字符密码安全测试甲乙丙"); err != nil {
		t.Fatalf("long Unicode password was rejected: %v", err)
	}
}

func openAuthDatabase(t *testing.T) *workbenchdb.Database {
	t.Helper()
	database, err := workbenchdb.Open(context.Background(), workbenchdb.Config{Path: filepath.Join(t.TempDir(), "data.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.MigrateCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	return database
}

func TestConfigRejectsUnsafeListenModes(t *testing.T) {
	database := openAuthDatabase(t)
	if _, err := New(context.Background(), database.SQL(), Config{Mode: ModeLocal, ListenAddress: "0.0.0.0:8080"}); err == nil {
		t.Fatal("local mode accepted non-loopback address")
	}
	if _, err := New(context.Background(), database.SQL(), Config{
		Mode: ModePassword, ListenAddress: "0.0.0.0:8080", InitialPassword: testPassword,
	}); err == nil {
		t.Fatal("password mode accepted non-HTTPS public address")
	}
}

func TestPasswordPersistsAndAuthenticates(t *testing.T) {
	database := openAuthDatabase(t)
	service, err := New(context.Background(), database.SQL(), Config{
		Mode: ModePassword, ListenAddress: "127.0.0.1:8080", InitialPassword: testPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := service.Authenticate(context.Background(), testPassword); err != nil || !ok {
		t.Fatalf("Authenticate(correct) = %v, %v", ok, err)
	}
	if ok, err := service.Authenticate(context.Background(), "incorrect password"); err != nil || ok {
		t.Fatalf("Authenticate(incorrect) = %v, %v", ok, err)
	}
	if _, err := New(context.Background(), database.SQL(), Config{
		Mode: ModePassword, ListenAddress: "127.0.0.1:8080",
	}); err != nil {
		t.Fatalf("restart without initial password failed: %v", err)
	}
}

func TestLoginCreatesServerSideSession(t *testing.T) {
	database := openAuthDatabase(t)
	service, err := New(context.Background(), database.SQL(), Config{
		Mode: ModePassword, ListenAddress: "127.0.0.1:8080", InitialPassword: testPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/login", service.LoginHandler)
	mux.Handle("GET /protected", service.Require(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	handler := service.LoadAndSave(mux)

	body, _ := json.Marshal(map[string]string{"password": testPassword})
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	loginRecorder := httptest.NewRecorder()
	handler.ServeHTTP(loginRecorder, loginRequest)
	if loginRecorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", loginRecorder.Code, loginRecorder.Body.String())
	}
	cookies := loginRecorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	protectedRequest := httptest.NewRequest(http.MethodGet, "/protected", nil)
	for _, cookie := range cookies {
		protectedRequest.AddCookie(cookie)
	}
	protectedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(protectedRecorder, protectedRequest)
	if protectedRecorder.Code != http.StatusNoContent {
		t.Fatalf("protected status = %d, body = %s", protectedRecorder.Code, protectedRecorder.Body.String())
	}
}

func TestSessionStoreDoesNotReturnExpiredData(t *testing.T) {
	database := openAuthDatabase(t)
	store := NewSessionStore(database.SQL())
	store.now = func() time.Time { return time.Unix(100, 0).UTC() }
	if err := store.Commit("token", []byte("data"), time.Unix(101, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	if data, found, err := store.Find("token"); err != nil || !found || string(data) != "data" {
		t.Fatalf("Find(active) = %q, %v, %v", data, found, err)
	}
	store.now = func() time.Time { return time.Unix(102, 0).UTC() }
	if _, found, err := store.Find("token"); err != nil || found {
		t.Fatalf("Find(expired) found = %v, error = %v", found, err)
	}
}

func TestSecurityRejectsInvalidHostAndOrigin(t *testing.T) {
	database := openAuthDatabase(t)
	service, err := New(context.Background(), database.SQL(), Config{Mode: ModeLocal, ListenAddress: "127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	handler := service.Security(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	invalidHost := httptest.NewRequest(http.MethodGet, "http://attacker.invalid/", nil)
	invalidHost.Host = "attacker.invalid"
	hostRecorder := httptest.NewRecorder()
	handler.ServeHTTP(hostRecorder, invalidHost)
	if hostRecorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid Host status = %d, want %d", hostRecorder.Code, http.StatusBadRequest)
	}

	invalidOrigin := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/", nil)
	invalidOrigin.Host = "127.0.0.1:8080"
	invalidOrigin.Header.Set("Origin", "http://attacker.invalid")
	originRecorder := httptest.NewRecorder()
	handler.ServeHTTP(originRecorder, invalidOrigin)
	if originRecorder.Code != http.StatusForbidden {
		t.Fatalf("invalid Origin status = %d, want %d", originRecorder.Code, http.StatusForbidden)
	}
}

func TestHTTPSMarksSecurityCookiesSecure(t *testing.T) {
	database := openAuthDatabase(t)
	service, err := New(context.Background(), database.SQL(), Config{
		Mode: ModePassword, ListenAddress: "0.0.0.0:8443", PublicHTTPS: true, InitialPassword: testPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := service.Security(service.LoadAndSave(http.HandlerFunc(service.CSRFTokenHandler)))
	request := httptest.NewRequest(http.MethodGet, "https://0.0.0.0:8443/api/auth/csrf", nil)
	request.Host = "0.0.0.0:8443"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("CSRF status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("CSRF response did not set a cookie")
	}
	for _, cookie := range cookies {
		if !cookie.Secure {
			t.Fatalf("cookie %q was not marked Secure", cookie.Name)
		}
	}
}
