package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workbench/internal/foundation/auth"
)

func TestHTTPSProxyToHTTPApplication(t *testing.T) {
	proxy := httptest.NewUnstartedServer(nil)
	defer proxy.Close()
	publicURL := "https://" + proxy.Listener.Addr().String()
	application, err := New(context.Background(), Config{
		ListenAddress: "0.0.0.0:8080", DataPath: filepath.Join(t.TempDir(), "data.db"),
		AuthMode: auth.ModePassword, Password: "Proxy-Test-123", PublicURL: publicURL, ShutdownGrace: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			t.Error("application must receive plaintext HTTP")
		}
		application.Handler().ServeHTTP(w, r)
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	proxy.Config.Handler = httputil.NewSingleHostReverseProxy(target)
	proxy.StartTLS()
	client := proxy.Client()
	client.Timeout = 5 * time.Second
	client.Jar, _ = cookiejar.New(nil)
	call := func(method, path, body, token, origin string, want int) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, publicURL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", origin)
		req.Header.Set("X-CSRF-Token", token)
		// These must not change the configured origin or Secure cookie behavior.
		req.Header.Set("Forwarded", "proto=http;host=attacker.invalid")
		req.Header.Set("X-Forwarded-Proto", "http")
		req.Header.Set("X-Forwarded-Host", "attacker.invalid")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = res.Body.Close() })
		if res.StatusCode != want {
			payload, _ := io.ReadAll(res.Body)
			t.Fatalf("%s %s: status %d, want %d; %s", method, path, res.StatusCode, want, payload)
		}
		return res
	}
	call("GET", "/api/modules/todo/tasks", "", "", "", 401).Body.Close()
	csrf := call("GET", "/api/auth/csrf", "", "", "", 200)
	var token struct{ Token string }
	if err := json.NewDecoder(csrf.Body).Decode(&token); err != nil || token.Token == "" {
		t.Fatalf("missing CSRF token: %v", err)
	}
	csrf.Body.Close()
	login := call("POST", "/api/auth/login", `{"password":"Proxy-Test-123"}`, token.Token, publicURL, 200)
	login.Body.Close()
	var secureSession bool
	for _, res := range []*http.Response{csrf, login} {
		for _, cookie := range res.Cookies() {
			if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
				t.Errorf("insecure cookie attributes for %s", cookie.Name)
			}
			secureSession = secureSession || cookie.Name == "workbench_session"
		}
	}
	if !secureSession {
		t.Fatal("login did not issue a session")
	}
	call("POST", "/api/modules/todo/tasks", `{"title":"Proxy task"}`, token.Token, publicURL, 201).Body.Close()
	call("POST", "/api/modules/todo/tasks", `{"title":"CSRF bypass"}`, "", publicURL, 400).Body.Close()
	call("POST", "/api/modules/todo/tasks", `{"title":"Origin bypass"}`, token.Token, "https://attacker.invalid", 403).Body.Close()
	call("POST", "/api/modules/todo/tasks", `{"title":"Scheme downgrade"}`, token.Token, strings.Replace(publicURL, "https:", "http:", 1), 403).Body.Close()
	settings := call("GET", "/api/settings", "", "", "", 200)
	payload, _ := io.ReadAll(settings.Body)
	settings.Body.Close()
	if !strings.Contains(string(payload), `"publicUrl":"`+publicURL+`"`) {
		t.Fatal("settings did not report external URL")
	}
	stream := call("GET", "/api/notifications/stream", "", "", "", 200)
	if _, err := bufio.NewReader(stream.Body).ReadString('\n'); err != nil {
		t.Fatalf("SSE was not flushed through the proxy: %v", err)
	}
	stream.Body.Close()
	badHost, _ := http.NewRequest("GET", backend.URL+"/api/auth/status", nil)
	badHost.Host = "attacker.invalid"
	badHost.Header.Set("X-Forwarded-Host", strings.TrimPrefix(publicURL, "https://"))
	rejected, err := backend.Client().Do(badHost)
	if err != nil {
		t.Fatal(err)
	}
	defer rejected.Body.Close()
	if rejected.StatusCode != 400 {
		t.Fatal("spoofed forwarded Host bypassed Host validation")
	}
}
