package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func newStreamTokenService(t *testing.T) *Service {
	t.Helper()
	database := openAuthDatabase(t)
	service, err := New(context.Background(), database.SQL(), Config{
		Mode: ModePassword, ListenAddress: "127.0.0.1:8080", InitialPassword: testPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	markInitialPasswordChanged(t, database)
	return service
}

func streamAccessToken(t *testing.T, handler http.Handler, target string) tokenResponse {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{
		"username": "admin", "password": testPassword, "deviceName": "Stream test", "platform": "other",
	})
	request := httptest.NewRequest(http.MethodPost, target+"/api/auth/token", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("token login: %d %s", recorder.Code, recorder.Body.String())
	}
	var token tokenResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	return token
}

func revokeStreamDevice(t *testing.T, handler http.Handler, target, accessToken string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target+"/api/auth/token/revoke", nil)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("revoke device: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestDeviceRevocationCancelsSSERequestContext(t *testing.T) {
	service := newStreamTokenService(t)
	started := make(chan struct{})
	canceled := make(chan struct{})
	returned := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/token", service.TokenHandler)
	mux.Handle("POST /api/auth/token/revoke", service.Require(http.HandlerFunc(service.RevokeTokenHandler)))
	mux.Handle("GET /events/stream", service.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_ = http.NewResponseController(w).Flush()
		close(started)
		<-r.Context().Done()
		close(canceled)
	})))
	handler := service.Security(service.LoadAndSave(mux))
	token := streamAccessToken(t, handler, "http://127.0.0.1:8080")
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/events/stream", nil)
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), request)
		close(returned)
	}()
	<-started
	revokeStreamDevice(t, handler, "http://127.0.0.1:8080", token.AccessToken)
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("SSE context remained active after device revocation")
	}
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not return after context cancellation")
	}
}

func TestDeviceRevocationClosesWebSocket(t *testing.T) {
	service := newStreamTokenService(t)
	started := make(chan struct{})
	done := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth/token", service.TokenHandler)
	mux.Handle("POST /api/auth/token/revoke", service.Require(http.HandlerFunc(service.RevokeTokenHandler)))
	mux.Handle("GET /socket", service.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		close(started)
		_, _, _ = connection.ReadMessage()
		close(done)
	})))
	handler := service.Security(service.LoadAndSave(mux))
	server := httptest.NewServer(handler)
	defer server.Close()
	service.allowedHosts[strings.TrimPrefix(server.URL, "http://")] = struct{}{}
	token := streamAccessToken(t, handler, server.URL)

	header := http.Header{"Authorization": {"Bearer " + token.AccessToken}}
	connection, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/socket", header)
	if err != nil {
		if response != nil {
			t.Fatalf("websocket handshake: %v (%d)", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	defer connection.Close()
	<-started

	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/auth/token/revoke", nil)
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("revoke status = %d", response.StatusCode)
	}
	_ = connection.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("websocket remained open after device revocation")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("websocket handler did not observe the closed connection")
	}
}
