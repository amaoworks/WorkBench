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

const testPassword = "Correct horse battery staple1"

func TestPasswordPolicy(t *testing.T) {
	tests := []struct {
		name     string
		password string
		valid    bool
	}{
		{"empty", "", false},
		{"seven characters with all categories", "Abcde1!", false},
		{"eight characters with all categories", "Abcdef1!", true},
		{"eight characters with three categories", "Abcdefg1", true},
		{"uppercase lowercase digits", "Abcdefgh1", true},
		{"uppercase lowercase symbols", "Abcdefgh!", true},
		{"uppercase digits symbols", "ABCDEFG1!", true},
		{"lowercase digits symbols", "abcdefg1!", true},
		{"all categories", "Abcdefg1!", true},
		{"one category", "abcdefghijk", false},
		{"two categories", "Abcdefghijk", false},
		{"spaces are not symbols", "Abcdefgh ", false},
		{"control characters are not symbols", "Abcdefgh\t", false},
		{"uncased letters are not symbols", "Abcdefgh中", false},
		{"Unicode length below minimum", "中文测Ab1!", false},
		{"Unicode length at minimum", "中文测试密Ab1", true},
		{"Unicode categories", "Äbcdefgh１", true},
		{"Unicode punctuation", "abcdefgh。1", true},
		{"Unicode symbol", "abcdefgh€1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := hashPassword(tt.password)
			if (err == nil) != tt.valid {
				t.Fatalf("hashPassword validity = %v, want %v; error = %v", err == nil, tt.valid, err)
			}
		})
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
