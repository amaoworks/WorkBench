package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"workbench/internal/capabilities/ai"
	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
)

// APIKey is stored in the private workspace database, never returned by settings APIs.
type AISettings struct {
	Enabled bool   `json:"enabled"`
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
	APIKey  string `json:"apiKey"`
}

type Appearance struct {
	Theme  string `json:"theme"`
	Motion string `json:"motion"`
}

func (a *App) loadSettings(ctx context.Context) error {
	a.aiSettings = AISettings{Enabled: a.config.OpenAIAPIKey != "", APIKey: a.config.OpenAIAPIKey, BaseURL: a.config.OpenAIBaseURL, Model: a.config.OpenAIModel}
	if a.aiSettings.BaseURL == "" {
		a.aiSettings.BaseURL = "https://api.openai.com/v1"
	}
	if a.aiSettings.Model == "" {
		a.aiSettings.Model = "gpt-5.2"
	}
	a.appearance = Appearance{Theme: "system", Motion: "full"}
	for section, target := range map[string]any{"ai": &a.aiSettings, "appearance": &a.appearance} {
		var raw string
		err := a.database.SQL().QueryRowContext(ctx, "SELECT value FROM workspace_settings WHERE section = ?", section).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal([]byte(raw), target); err != nil {
			return err
		}
	}
	provider, err := settingsProvider(a.aiSettings)
	if err != nil {
		return err
	}
	a.gateway.SetProvider(provider)
	a.textAI.SetProvider(provider)
	return nil
}

func (a *App) persistSettings(ctx context.Context, section string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = a.database.SQL().ExecContext(ctx, "INSERT INTO workspace_settings(section, value) VALUES (?, ?) ON CONFLICT(section) DO UPDATE SET value = excluded.value", section, string(raw))
	return err
}

func (a *App) getSettings(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, 200, map[string]any{
		"ai":         map[string]any{"enabled": a.aiSettings.Enabled, "baseUrl": a.aiSettings.BaseURL, "model": a.aiSettings.Model, "hasApiKey": a.aiSettings.APIKey != ""},
		"appearance": a.appearance,
		"deployment": map[string]any{"listenAddress": a.config.ListenAddress, "dataPath": a.config.DataPath, "authMode": a.config.AuthMode, "tls": a.config.HTTPS(), "allowedHosts": a.config.AllowedHosts},
	})
}

func (a *App) readAIInput(w http.ResponseWriter, r *http.Request) (AISettings, error) {
	var input struct {
		Enabled     *bool  `json:"enabled"`
		BaseURL     string `json:"baseUrl"`
		Model       string `json:"model"`
		APIKey      string `json:"apiKey"`
		ClearAPIKey bool   `json:"clearApiKey"`
	}
	if err := httpapi.Decode(w, r, &input, 16384); err != nil {
		return AISettings{}, errors.New("配置格式不正确")
	}
	if input.Enabled == nil {
		return AISettings{}, errors.New("请选择是否启用 AI")
	}
	value := AISettings{Enabled: *input.Enabled, BaseURL: strings.TrimRight(strings.TrimSpace(input.BaseURL), "/"), Model: strings.TrimSpace(input.Model), APIKey: strings.TrimSpace(input.APIKey)}
	if value.BaseURL == "" {
		value.BaseURL = "https://api.openai.com/v1"
	}
	u, err := url.Parse(value.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return value, errors.New("接口地址必须是完整的 HTTP 或 HTTPS 地址，不能包含账号、查询参数或片段")
	}
	if len(value.BaseURL) > 2048 || len(value.Model) > 200 || len(value.APIKey) > 8192 {
		return value, errors.New("配置内容过长")
	}
	if value.Model == "" {
		return value, errors.New("请输入模型名称")
	}
	if input.ClearAPIKey && value.APIKey != "" {
		return value, errors.New("不能同时清除和替换 API Key")
	}
	if value.APIKey == "" && !input.ClearAPIKey {
		// A stored secret must not be forwarded to a newly selected endpoint silently.
		if value.BaseURL != a.aiSettings.BaseURL && a.aiSettings.APIKey != "" {
			return value, errors.New("更换接口地址时，请重新填写该服务的 API Key")
		}
		value.APIKey = a.aiSettings.APIKey
	}
	if value.Enabled && value.APIKey == "" {
		return value, errors.New("启用 AI 前请填写 API Key")
	}
	return value, nil
}

func settingsProvider(value AISettings) (contracts.AIProvider, error) {
	if !value.Enabled {
		return nil, nil
	}
	return ai.NewOpenAIProvider(ai.OpenAIConfig{
		APIKey: value.APIKey, BaseURL: value.BaseURL,
		HTTPClient: &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		Profiles:   map[string]ai.Profile{"default": {Model: value.Model, MaxOutputTokens: 2048}},
	})
}

func (a *App) saveAISettings(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	value, err := a.readAIInput(w, r)
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", err.Error())
		return
	}
	provider, err := settingsProvider(value)
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", "无法初始化 AI 配置")
		return
	}
	if err := a.persistSettings(r.Context(), "ai", value); err != nil {
		httpapi.Error(w, 500, "settings_failed", "无法保存设置")
		return
	}
	a.aiSettings = value
	a.gateway.SetProvider(provider)
	a.textAI.SetProvider(provider)
	httpapi.Write(w, 200, map[string]bool{"saved": true})
}

func (a *App) testAISettings(w http.ResponseWriter, r *http.Request) {
	a.settingsMu.Lock()
	value, err := a.readAIInput(w, r)
	a.settingsMu.Unlock()
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", err.Error())
		return
	}
	if value.APIKey == "" {
		httpapi.Error(w, 400, "missing_key", "请填写 API Key 后再测试")
		return
	}
	value.Enabled = true
	provider, err := settingsProvider(value)
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", "无法初始化 AI 配置")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	started := time.Now()
	_, err = provider.Generate(ctx, contracts.GenerateRequest{Profile: "default", Messages: []contracts.AIMessage{ai.TextMessage("user", "Reply with OK.")}})
	if err != nil {
		// Upstream errors can contain request credentials or sensitive response bodies.
		httpapi.Error(w, 502, "connection_failed", "连接测试失败，请检查接口地址、密钥、模型及服务是否支持 Responses API")
		return
	}
	httpapi.Write(w, 200, map[string]any{"ok": true, "latencyMs": time.Since(started).Milliseconds()})
}

func (a *App) saveAppearance(w http.ResponseWriter, r *http.Request) {
	var value Appearance
	if err := httpapi.Decode(w, r, &value, 4096); err != nil {
		httpapi.Error(w, 400, "invalid_settings", "外观设置格式不正确")
		return
	}
	if (value.Theme != "light" && value.Theme != "dark" && value.Theme != "system") || (value.Motion != "full" && value.Motion != "reduced") {
		httpapi.Error(w, 400, "invalid_settings", "不支持的主题或动态效果选项")
		return
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if err := a.persistSettings(r.Context(), "appearance", value); err != nil {
		httpapi.Error(w, 500, "settings_failed", "无法保存设置")
		return
	}
	a.appearance = value
	httpapi.Write(w, 200, value)
}
