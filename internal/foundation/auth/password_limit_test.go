package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPasswordLimitRefill(t *testing.T) {
	now := time.Unix(100, 0)
	limit := &passwordLimit{tokens: loginBurst, refillAt: now, now: func() time.Time { return now }}
	consume := func() {
		t.Helper()
		if retry, err := limit.acquire(true); err != nil {
			t.Fatalf("acquire = %d, %v", retry, err)
		}
		limit.release()
	}
	denied := func(wantRetry int) {
		t.Helper()
		if retry, err := limit.acquire(true); !errors.Is(err, errLoginRateLimited) || retry != wantRetry {
			t.Fatalf("acquire = %d, %v; want %d, rate limit", retry, err, wantRetry)
		}
	}
	for range loginBurst {
		consume()
	}
	denied(3)
	now = now.Add(loginRefillInterval - time.Nanosecond)
	denied(1)
	now = now.Add(time.Nanosecond)
	consume()
	denied(3)
	now = now.Add(2 * time.Second)
	denied(1)
	now = now.Add(time.Second)
	consume()
	denied(3)
	now = now.Add(100 * loginRefillInterval)
	for range loginBurst {
		consume()
	}
	denied(3)
}

func TestPasswordLimitConcurrentCapacity(t *testing.T) {
	limit := newPasswordLimit()
	acquired := make(chan error, maxPasswordConcurrency)
	release := make(chan struct{})
	var workers sync.WaitGroup
	for range maxPasswordConcurrency {
		workers.Go(func() {
			_, err := limit.acquire(false)
			acquired <- err
			if err == nil {
				<-release
				limit.release()
			}
		})
	}
	defer func() {
		close(release)
		workers.Wait()
	}()
	for range maxPasswordConcurrency {
		if err := <-acquired; err != nil {
			t.Fatal(err)
		}
	}
	if retry, err := limit.acquire(true); retry != 1 || !errors.Is(err, errPasswordBusy) {
		t.Fatalf("full capacity = %d, %v", retry, err)
	}
	limit.mu.Lock()
	tokens := limit.tokens
	limit.mu.Unlock()
	if tokens != loginBurst {
		t.Fatalf("busy login spent a token: %d", tokens)
	}
}

func newLimitedAuthService(t *testing.T) *Service {
	t.Helper()
	database := openAuthDatabase(t)
	service, err := New(context.Background(), database.SQL(), Config{
		Mode: ModePassword, ListenAddress: "127.0.0.1:8080", InitialPassword: testPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(100, 0)
	service.passwordLimit = &passwordLimit{tokens: loginBurst, refillAt: now, now: func() time.Time { return now }}
	return service
}

func assertNoPasswordWork(t *testing.T, service *Service) {
	t.Helper()
	service.passwordLimit.mu.Lock()
	defer service.passwordLimit.mu.Unlock()
	if service.passwordLimit.active != 0 {
		t.Fatalf("leaked password work: %d", service.passwordLimit.active)
	}
}

func TestPasswordHandlersShareCapacity(t *testing.T) {
	service := newLimitedAuthService(t)
	for range maxPasswordConcurrency {
		if _, err := service.passwordLimit.acquire(false); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := service.Authenticate(context.Background(), testPassword); ok || !errors.Is(err, errPasswordBusy) {
		t.Fatalf("Authenticate while busy = %v, %v", ok, err)
	}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		body    string
	}{
		{"login", service.LoginHandler, `{"password":"` + testPassword + `"}`},
		{"change password", service.ChangePasswordHandler, `{"currentPassword":"` + testPassword + `","newPassword":"Changed-Pass123"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			tc.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)))
			if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "1" || !strings.Contains(recorder.Body.String(), `"code":"authentication_busy"`) {
				t.Fatalf("busy response: %d %v %s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
		})
	}
	for range maxPasswordConcurrency {
		service.passwordLimit.release()
	}
	if ok, err := service.Authenticate(context.Background(), testPassword); !ok || err != nil {
		t.Fatalf("Authenticate after release = %v, %v", ok, err)
	}
	assertNoPasswordWork(t, service)
	if service.passwordLimit.tokens != loginBurst {
		t.Fatal("busy requests or Authenticate consumed login tokens")
	}
}

func TestLoginLimitAndErrorRelease(t *testing.T) {
	service := newLimitedAuthService(t)
	handler := service.LoadAndSave(http.HandlerFunc(service.LoginHandler))
	call := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body)))
		assertNoPasswordWork(t, service)
		return recorder
	}
	if got := call(`{"password":`); got.Code != http.StatusBadRequest || service.passwordLimit.tokens != loginBurst {
		t.Fatalf("invalid JSON consumed capacity: %d %s", got.Code, got.Body.String())
	}
	for range loginBurst - 1 {
		if got := call(`{"password":"wrong"}`); got.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password: %d %s", got.Code, got.Body.String())
		}
	}
	correct := `{"password":"` + testPassword + `"}`
	login := call(correct)
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), `"mustChangePassword":true`) {
		t.Fatalf("normal initial login: %d %s", login.Code, login.Body.String())
	}
	got := call(correct)
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") != "3" || !strings.Contains(got.Body.String(), `"code":"login_rate_limited"`) {
		t.Fatalf("rate limit: %d %v %s", got.Code, got.Header(), got.Body.String())
	}
	for _, cookie := range got.Result().Cookies() {
		if cookie.Name == "workbench_session" {
			t.Fatal("rate-limited login created a session")
		}
	}

	change := service.LoadAndSave(service.Require(http.HandlerFunc(service.ChangePasswordHandler)))
	changeRequest := httptest.NewRequest(http.MethodPut, "/api/settings/password", strings.NewReader(`{"currentPassword":"`+testPassword+`","newPassword":"Changed-Pass123"}`))
	for _, cookie := range login.Result().Cookies() {
		changeRequest.AddCookie(cookie)
	}
	changed := httptest.NewRecorder()
	change.ServeHTTP(changed, changeRequest)
	if changed.Code != http.StatusOK {
		t.Fatalf("initial password change with empty login bucket: %d %s", changed.Code, changed.Body.String())
	}
	assertNoPasswordWork(t, service)
	if service.passwordLimit.tokens != 0 {
		t.Fatal("password change reset login tokens")
	}
	service.passwordLimit.now = func() time.Time { return time.Unix(100, 0).Add(loginRefillInterval) }
	relogin := call(`{"password":"Changed-Pass123"}`)
	if relogin.Code != http.StatusOK || !strings.Contains(relogin.Body.String(), `"mustChangePassword":false`) {
		t.Fatalf("login after initial password change: %d %s", relogin.Code, relogin.Body.String())
	}
}

func TestPasswordLimitCredentialErrorRelease(t *testing.T) {
	service := newLimitedAuthService(t)
	if _, err := service.db.Exec("UPDATE auth_credentials SET password_hash = 'invalid' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	if ok, err := service.Authenticate(context.Background(), testPassword); ok || err == nil {
		t.Fatalf("malformed hash Authenticate = %v, %v", ok, err)
	}
	assertNoPasswordWork(t, service)
	for _, tc := range []struct {
		handler http.HandlerFunc
		body    string
		status  int
	}{
		{service.LoginHandler, `{"password":"wrong"}`, http.StatusInternalServerError},
		{service.ChangePasswordHandler, `{"currentPassword":"wrong","newPassword":"Changed-Pass123"}`, http.StatusForbidden},
	} {
		recorder := httptest.NewRecorder()
		tc.handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body)))
		if recorder.Code != tc.status {
			t.Fatalf("malformed hash: %d %s", recorder.Code, recorder.Body.String())
		}
		assertNoPasswordWork(t, service)
	}
}

func TestChangePasswordLimitErrorRelease(t *testing.T) {
	service := newLimitedAuthService(t)
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"currentPassword":"wrong","newPassword":"Changed-Pass123"}`, http.StatusForbidden},
		{`{"currentPassword":"` + testPassword + `","newPassword":"short"}`, http.StatusBadRequest},
		{`{"currentPassword":"` + testPassword + `","newPassword":"` + testPassword + `"}`, http.StatusBadRequest},
	} {
		recorder := httptest.NewRecorder()
		service.ChangePasswordHandler(recorder, httptest.NewRequest(http.MethodPut, "/api/settings/password", bytes.NewBufferString(tc.body)))
		if recorder.Code != tc.status {
			t.Fatalf("change-password error: %d %s", recorder.Code, recorder.Body.String())
		}
		assertNoPasswordWork(t, service)
	}
	if service.passwordLimit.tokens != loginBurst {
		t.Fatal("password changes consumed login tokens")
	}
}
