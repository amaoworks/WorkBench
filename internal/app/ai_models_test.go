package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestAIModelListingUsesDraftAndRetainedCredentialsWithoutSaving(t *testing.T) {
	a := newTestApp(t)
	token, cookies := csrf(t, a.Handler())
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "GET" || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("unexpected model listing request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(401)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"model-b"},{"id":"model-a"},{"id":"model-b"},{"id":""}]}`)
	}))
	defer upstream.Close()
	body, _ := json.Marshal(map[string]any{"baseUrl": upstream.URL + "/v1/", "apiKey": "test-secret"})
	listed := request(t, a.Handler(), "POST", "/api/settings/ai/models", body, token, cookies)
	if listed.Code != 200 || listed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list models: %d %s", listed.Code, listed.Body.String())
	}
	var result struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &result); err != nil || !slices.Equal(result.Models, []string{"model-a", "model-b"}) {
		t.Fatalf("model list: %s %v", listed.Body.String(), err)
	}
	if a.gateway.Available() || a.aiSettings.APIKey != "" {
		t.Fatal("listing changed saved AI settings")
	}
	saved, _ := json.Marshal(map[string]any{
		"enabled": true, "baseUrl": upstream.URL + "/v1", "apiKey": "test-secret", "model": "model-a", "models": []string{"model-a", "model-b"},
	})
	if w := request(t, a.Handler(), "PUT", "/api/settings/ai", saved, token, cookies); w.Code != 200 {
		t.Fatalf("save models: %d %s", w.Code, w.Body.String())
	}
	body, _ = json.Marshal(map[string]string{"baseUrl": upstream.URL + "/v1", "apiKey": ""})
	if w := request(t, a.Handler(), "POST", "/api/settings/ai/models", body, token, cookies); w.Code != 200 {
		t.Fatalf("retained key: %d %s", w.Code, w.Body.String())
	}
	if calls != 2 {
		t.Fatalf("listing calls = %d", calls)
	}
	for _, input := range []map[string]any{
		{"baseUrl": upstream.URL + "/other", "apiKey": ""},
		{"baseUrl": upstream.URL + "/v1", "clearApiKey": true},
	} {
		body, _ := json.Marshal(input)
		if w := request(t, a.Handler(), "POST", "/api/settings/ai/models", body, token, cookies); w.Code != 400 {
			t.Fatalf("unchecked credentials: %d %s", w.Code, w.Body.String())
		}
	}
	if calls != 2 {
		t.Fatal("forwarded retained credentials to an unapproved endpoint")
	}
}

func TestAIModelListingRejectsRedirectsAndRedactsUpstreamErrors(t *testing.T) {
	a := newTestApp(t)
	token, cookies := csrf(t, a.Handler())
	targetCalls := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls++
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer target.Close()
	for _, test := range []struct {
		name  string
		serve http.HandlerFunc
	}{
		{"redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL+"/models", 302) }},
		{"upstream-error", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, "secret-upstream-response")
		}},
		{"invalid-json", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secret-upstream-response") }},
		{"missing-data", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) }},
		{"oversized", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, strings.Repeat("x", 2*1024*1024+1))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := httptest.NewServer(test.serve)
			defer upstream.Close()
			body, _ := json.Marshal(map[string]string{"baseUrl": upstream.URL, "apiKey": "test-secret"})
			w := request(t, a.Handler(), "POST", "/api/settings/ai/models", body, token, cookies)
			if w.Code != 502 || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("unsafe failure: %d %s", w.Code, w.Body.String())
			}
		})
	}
	if targetCalls != 0 {
		t.Fatal("followed an upstream redirect")
	}
	if w := request(t, a.Handler(), "POST", "/api/settings/ai/models", []byte(`{"baseUrl":"https://example.com/v1","apiKey":"key"}`), "", nil); w.Code != 400 {
		t.Fatalf("model listing bypassed CSRF: %d", w.Code)
	}
}

func TestAIModelsValidationAndPersistence(t *testing.T) {
	a := newTestApp(t)
	token, cookies := csrf(t, a.Handler())
	for _, models := range [][]string{{}, {"other"}, {"model-a", ""}, {"model-a", "bad\nmodel"}, {"model-a", strings.Repeat("m", 201)}, slices.Repeat([]string{"model-a"}, 51)} {
		body, _ := json.Marshal(map[string]any{"enabled": false, "model": "model-a", "models": models})
		if w := request(t, a.Handler(), "PUT", "/api/settings/ai", body, token, cookies); w.Code != 400 {
			t.Fatalf("invalid model list %q: %d %s", models, w.Code, w.Body.String())
		}
	}
	body := []byte(`{"enabled":true,"baseUrl":"https://example.com/v1","model":"model-a","models":["model-a"," model-b ","model-a"],"apiKey":"test-secret"}`)
	if w := request(t, a.Handler(), "PUT", "/api/settings/ai", body, token, cookies); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	got := request(t, a.Handler(), "GET", "/api/settings", nil, "", cookies)
	if bytes.Contains(got.Body.Bytes(), []byte("test-secret")) || !bytes.Contains(got.Body.Bytes(), []byte(`"models":["model-a","model-b"]`)) {
		t.Fatalf("model settings: %s", got.Body.String())
	}
	cfg := a.config
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if !slices.Equal(restarted.aiSettings.Models, []string{"model-a", "model-b"}) || restarted.aiSettings.Model != "model-a" {
		t.Fatalf("models did not survive restart: %+v", restarted.aiSettings.Models)
	}
}

func TestLegacySingleAIModelRestoresAsModelList(t *testing.T) {
	a := newTestApp(t)
	if err := a.persistSettings(context.Background(), "ai", map[string]any{"enabled": false, "baseUrl": "https://example.com/v1", "model": "legacy-model", "apiKey": ""}); err != nil {
		t.Fatal(err)
	}
	cfg := a.config
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if !slices.Equal(restarted.aiSettings.Models, []string{"legacy-model"}) || restarted.aiSettings.Model != "legacy-model" {
		t.Fatalf("legacy model was lost: %+v", restarted.aiSettings.Models)
	}
}
