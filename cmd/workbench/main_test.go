package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workbench/internal/app"
)

func TestDeploymentOptionsPrecedence(t *testing.T) {
	env := map[string]string{
		"WORKBENCH_LISTEN": "0.0.0.0:8080", "WORKBENCH_DATA": "/data/data.db",
		"WORKBENCH_AUTH": "password", "WORKBENCH_PUBLIC_URL": "https://workbench.example.com",
		"WORKBENCH_ALLOWED_HOSTS": " one.example.com, , two.example.com ",
		"WORKBENCH_PASSWORD":      "Test-Password-123", "OPENAI_MODEL": "test-model",
	}
	getenv := func(key string) string { return env[key] }
	opt, err := parseOptions(nil, getenv, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if opt.config.ListenAddress != env["WORKBENCH_LISTEN"] || opt.config.DataPath != "/data/data.db" || opt.config.AuthMode != "password" || opt.config.PublicURL != env["WORKBENCH_PUBLIC_URL"] || opt.config.Password != env["WORKBENCH_PASSWORD"] || opt.config.OpenAIModel != "test-model" || strings.Join(opt.config.AllowedHosts, ",") != "one.example.com,two.example.com" {
		t.Fatal("deployment environment was not applied")
	}
	opt, err = parseOptions([]string{"-listen", "127.0.0.1:9090", "-data", "/tmp/override.db", "-auth", "local", "-public-url=", "-allowed-host", "localhost:9090", "-allowed-host", "127.0.0.1:9090"}, getenv, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if opt.config.ListenAddress != "127.0.0.1:9090" || opt.config.DataPath != "/tmp/override.db" || opt.config.AuthMode != "local" || opt.config.PublicURL != "" || strings.Join(opt.config.AllowedHosts, ",") != "localhost:9090,127.0.0.1:9090" {
		t.Fatal("flags must override environment, including an explicitly empty public URL")
	}
	for _, args := range [][]string{{"unexpected"}, {"-allowed-host", " "}, {"-tls-cert", "old.pem"}} {
		if _, err := parseOptions(args, getenv, io.Discard); err == nil {
			t.Fatalf("accepted invalid arguments %v", args)
		}
	}
}

func TestHealthcheckUsesHTTPAndPublicHost(t *testing.T) {
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil || r.Host != "workbench.example.com" || r.URL.Path != "/health/ready" {
			t.Error("probe must use HTTP with the configured public Host")
		}
		w.WriteHeader(status)
	}))
	defer server.Close()
	// Probe wildcard listeners through loopback, including when proxy env vars exist.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	cfg := app.Config{ListenAddress: strings.Replace(server.Listener.Addr().String(), "127.0.0.1", "0.0.0.0", 1), PublicURL: "https://workbench.example.com"}
	if err := healthcheck(cfg); err != nil {
		t.Fatal(err)
	}
	status = http.StatusServiceUnavailable
	if err := healthcheck(cfg); err == nil {
		t.Fatal("unready service passed healthcheck")
	}
	status = http.StatusFound
	if err := healthcheck(cfg); err == nil {
		t.Fatal("redirect passed healthcheck")
	}
}
