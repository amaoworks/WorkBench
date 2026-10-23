package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityRejectionsDoNotSpendLoginTokens(t *testing.T) {
	service := newLimitedAuthService(t)
	handler := service.Security(service.LoadAndSave(http.HandlerFunc(service.LoginHandler)))
	for _, tc := range []struct {
		name   string
		host   string
		origin string
		status int
	}{
		{"host", "attacker.invalid", "", http.StatusBadRequest},
		{"origin", "127.0.0.1:8080", "https://attacker.invalid", http.StatusForbidden},
		{"csrf", "127.0.0.1:8080", "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/auth/login", strings.NewReader(`{"password":"wrong"}`))
			req.Host = tc.host
			req.Header.Set("Origin", tc.origin)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Code != tc.status {
				t.Fatalf("security rejection: %d %s", recorder.Code, recorder.Body.String())
			}
			if service.passwordLimit.tokens != loginBurst {
				t.Fatal("security rejection spent a login token")
			}
			assertNoPasswordWork(t, service)
		})
	}
	preflight := httptest.NewRequest(http.MethodOptions, "http://127.0.0.1:8080/api/auth/login", nil)
	preflight.Header.Set("Origin", "http://127.0.0.1:8080")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, preflight)
	if recorder.Code != http.StatusNoContent || service.passwordLimit.tokens != loginBurst {
		t.Fatalf("preflight: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestLocalModeBypassesPasswordLimit(t *testing.T) {
	database := openAuthDatabase(t)
	service, err := New(context.Background(), database.SQL(), Config{Mode: ModeLocal, ListenAddress: "127.0.0.1:8080"})
	if err != nil {
		t.Fatal(err)
	}
	service.passwordLimit.tokens = 0
	service.passwordLimit.active = maxPasswordConcurrency
	if ok, err := service.Authenticate(context.Background(), ""); !ok || err != nil {
		t.Fatalf("local Authenticate = %v, %v", ok, err)
	}
	recorder := httptest.NewRecorder()
	service.LoginHandler(recorder, httptest.NewRequest(http.MethodPost, "/api/auth/login", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("local login: %d %s", recorder.Code, recorder.Body.String())
	}
	if service.passwordLimit.tokens != 0 || service.passwordLimit.active != maxPasswordConcurrency {
		t.Fatal("local authentication changed password limit state")
	}
}
