package investment

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workbench/internal/foundation/auth"
)

// Opt-in because this test requires Playwright and Chromium. All credentials,
// tokens, databases and OAuth endpoints are disposable local fixtures.
func TestSchwabOAuthBrowser(t *testing.T) {
	if os.Getenv("WORKBENCH_BROWSER_TEST") != "1" {
		t.Skip("set WORKBENCH_BROWSER_TEST=1 and PLAYWRIGHT_MODULE to run Chromium OAuth verification")
	}
	module := openInvestmentModule(t)
	server := httptest.NewUnstartedServer(nil)
	t.Cleanup(server.Close)
	baseURL := "https://" + server.Listener.Addr().String()
	var exchanges atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/oauth/authorize":
			callback, err := url.Parse(r.URL.Query().Get("redirect_uri"))
			if err != nil {
				t.Error(err)
				http.Error(w, "invalid test callback", 400)
				return
			}
			query := callback.Query()
			query.Set("code", "browser-code")
			query.Set("state", r.URL.Query().Get("state"))
			callback.RawQuery = query.Encode()
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<h1>Accounts linked to WorkBench</h1><a href="`+html.EscapeString(callback.String())+`">Done</a>`)
		case "/v1/oauth/token":
			exchanges.Add(1)
			if r.Method != http.MethodPost || r.ParseForm() != nil || r.Form.Get("code") != "browser-code" || r.Form.Get("redirect_uri") != baseURL+"/oauth/schwab" {
				t.Error("invalid OAuth token exchange")
				http.Error(w, "invalid test token exchange", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "browser-access", "refresh_token": "browser-refresh", "expires_in": 1800})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	// localhost and 127.0.0.1 are different sites, not just different ports.
	module.schwabAPI = strings.Replace(upstream.URL, "127.0.0.1", "localhost", 1)

	seedSchwab(t, module, schwabRecord{AppKey: "browser-key", AppSecret: "browser-secret", CallbackURL: baseURL + "/oauth/schwab"})
	service, err := auth.New(context.Background(), module.deps.DB, auth.Config{
		Mode: auth.ModePassword, ListenAddress: server.Listener.Addr().String(), PublicHTTPS: true, InitialPassword: "Browser-Test-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/csrf", service.CSRFTokenHandler)
	mux.HandleFunc("GET /api/auth/status", service.StatusHandler)
	mux.HandleFunc("POST /api/auth/login", service.LoginHandler)
	mux.Handle("GET /api/modules/investment/schwab/oauth/login", service.Require(http.HandlerFunc(module.oauthLogin)))
	var callbackHadSession atomic.Bool
	mux.HandleFunc("GET /oauth/schwab", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("workbench_session"); err == nil {
			callbackHadSession.Store(true)
		}
		module.oauthCallback(w, r)
	})
	mux.Handle("GET /investment", service.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Referer() != "" {
			t.Error("authorization callback URL leaked as a referrer")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<h1>投资</h1>")
	})))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<h1>WorkBench</h1><a href="/api/modules/investment/schwab/oauth/login">登录 Schwab</a>`)
	})
	server.Config.Handler = service.Security(service.LoadAndSave(mux))
	server.StartTLS()
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	script, err := filepath.Abs("../../../scripts/schwab-oauth.e2e.cjs")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(), "WORKBENCH_TEST_URL="+baseURL, "SCHWAB_TEST_URL="+module.schwabAPI)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("OAuth browser verification failed: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	if callbackHadSession.Load() {
		t.Error("test did not exercise a cross-site callback without the Strict session cookie")
	}
	stored, err := module.loadSchwab(context.Background())
	if err != nil || stored.AccessToken != "browser-access" || stored.RefreshToken != "browser-refresh" || stored.OAuthState != "" || exchanges.Load() != 1 {
		t.Error("OAuth must persist tokens and consume the state with exactly one token exchange")
	}
}
