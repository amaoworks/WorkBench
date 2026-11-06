package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"

	"workbench/internal/foundation/httpapi"
)

func validAIModelID(id string) bool {
	return id != "" && len(id) <= 200 && !strings.ContainsFunc(id, unicode.IsControl)
}

func normalizeAIModels(defaultModel string, models []string) ([]string, error) {
	if !validAIModelID(defaultModel) {
		return nil, errors.New("请输入有效的默认模型 ID")
	}
	// Existing single-model settings and callers retain their configured model.
	if models == nil {
		models = []string{defaultModel}
	}
	if len(models) == 0 || len(models) > 50 {
		return nil, errors.New("请添加 1 至 50 个模型")
	}
	result := make([]string, 0, len(models))
	for _, raw := range models {
		model := strings.TrimSpace(raw)
		if !validAIModelID(model) {
			return nil, errors.New("模型 ID 须为 1 至 200 字节，不能包含控制字符")
		}
		if !slices.Contains(result, model) {
			result = append(result, model)
		}
	}
	if !slices.Contains(result, defaultModel) {
		return nil, errors.New("默认模型须在已添加模型中")
	}
	return result, nil
}

func resolveAIEndpoint(baseURL, apiKey string, clearKey bool, stored AISettings) (string, string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	apiKey = strings.TrimSpace(apiKey)
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("接口地址必须是完整的 HTTP 或 HTTPS 地址，不能包含账号、查询参数或片段")
	}
	if len(baseURL) > 2048 || len(apiKey) > 8192 {
		return "", "", errors.New("配置内容过长")
	}
	if clearKey && apiKey != "" {
		return "", "", errors.New("不能同时清除和替换 API Key")
	}
	if apiKey == "" && !clearKey {
		if baseURL != stored.BaseURL && stored.APIKey != "" {
			return "", "", errors.New("更换接口地址时，请重新填写该服务的 API Key")
		}
		apiKey = stored.APIKey
	}
	return baseURL, apiKey, nil
}

func (a *App) listAIModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input struct {
		BaseURL     string `json:"baseUrl"`
		APIKey      string `json:"apiKey"`
		ClearAPIKey bool   `json:"clearApiKey"`
	}
	if err := httpapi.Decode(w, r, &input, 16384); err != nil {
		httpapi.Error(w, 400, "invalid_settings", "配置格式不正确")
		return
	}
	a.settingsMu.Lock()
	stored := a.aiSettings
	a.settingsMu.Unlock()
	baseURL, apiKey, err := resolveAIEndpoint(input.BaseURL, input.APIKey, input.ClearAPIKey, stored)
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", err.Error())
		return
	}
	if apiKey == "" {
		httpapi.Error(w, 400, "missing_key", "请填写 API Key 后再获取模型")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	models, err := fetchAIModels(ctx, baseURL, apiKey)
	if err != nil {
		// Upstream bodies and errors may contain secrets; expose only a fixed message.
		httpapi.Error(w, 502, "models_failed", "获取模型失败，请检查接口地址、密钥及模型列表接口")
		return
	}
	httpapi.Write(w, 200, map[string]any{"models": models})
}

func fetchAIModels(ctx context.Context, baseURL, apiKey string) ([]string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	request.Header.Set("Accept", "application/json")
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("model listing returned an unsuccessful status")
	}
	const limit = 2 * 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, errors.New("model listing exceeds response limit")
	}
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result.Data == nil {
		return nil, errors.New("invalid model listing")
	}
	models := make([]string, 0, len(result.Data))
	for _, item := range result.Data {
		id := strings.TrimSpace(item.ID)
		if validAIModelID(id) {
			models = append(models, id)
		}
	}
	slices.Sort(models)
	return slices.Compact(models), nil
}
