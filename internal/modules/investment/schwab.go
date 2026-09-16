package investment

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

const (
	tokenRefreshSkew    = 3 * time.Minute
	oauthStateTTL       = 10 * time.Minute
	maxSchwabProxyBody  = 1 << 20
	maxSchwabTokenBytes = 1 << 20
)

var errSchwabReauthorizationRequired = errors.New("Schwab 授权已失效，请重新授权后继续使用行情、持仓和交易功能。")

type schwabRecord struct {
	AppKey                  string
	AppSecret               string
	CallbackURL             string
	AccessToken             string
	RefreshToken            string
	TokenExpiresAt          time.Time
	StreamerInfo            string
	OAuthState              string
	OAuthStateExpiresAt     time.Time
	LastError               string
	UpdatedAt               time.Time
	ReauthorizationRequired bool
}

type schwabSettingsView struct {
	AppKey                  string     `json:"appKey"`
	CallbackURL             string     `json:"callbackUrl"`
	HasAppSecret            bool       `json:"hasAppSecret"`
	Connected               bool       `json:"connected"`
	TokenExpiresAt          *time.Time `json:"tokenExpiresAt,omitempty"`
	LastError               string     `json:"lastError"`
	ReauthorizationRequired bool       `json:"reauthorizationRequired"`
}

type schwabSettingsInput struct {
	AppKey      string `json:"appKey"`
	AppSecret   string `json:"appSecret"`
	CallbackURL string `json:"callbackUrl"`
}

type schwabTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func (m *Module) loadSchwab(ctx context.Context) (schwabRecord, error) {
	row, err := m.queries.GetSchwab(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return schwabRecord{}, nil
	}
	if err != nil {
		return schwabRecord{}, err
	}
	return schwabRecord{
		AppKey: row.AppKey, AppSecret: row.AppSecret, CallbackURL: row.CallbackUrl,
		AccessToken: row.AccessToken, RefreshToken: row.RefreshToken,
		TokenExpiresAt: unixMilli(row.TokenExpiresAt), StreamerInfo: row.StreamerInfo,
		OAuthState: row.OauthState, OAuthStateExpiresAt: unixMilli(row.OauthStateExpiresAt),
		LastError: row.LastError, UpdatedAt: unixMilli(row.UpdatedAt),
		ReauthorizationRequired: row.ReauthorizationRequired != 0,
	}, nil
}

func (m *Module) storeSchwab(ctx context.Context, rec schwabRecord) error {
	var reauthorizationRequired int64
	if rec.ReauthorizationRequired {
		reauthorizationRequired = 1
	}
	return m.queries.UpsertSchwab(ctx, investmentsqlc.UpsertSchwabParams{
		AppKey: rec.AppKey, AppSecret: rec.AppSecret, CallbackUrl: rec.CallbackURL,
		AccessToken: rec.AccessToken, RefreshToken: rec.RefreshToken,
		TokenExpiresAt: timeUnixMilli(rec.TokenExpiresAt), StreamerInfo: rec.StreamerInfo,
		OauthState: rec.OAuthState, OauthStateExpiresAt: timeUnixMilli(rec.OAuthStateExpiresAt),
		LastError: rec.LastError, UpdatedAt: m.now().UTC().UnixMilli(),
		ReauthorizationRequired: reauthorizationRequired,
	})
}

func (m *Module) getSchwab(w http.ResponseWriter, r *http.Request) {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	rec, err := m.loadSchwab(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "schwab_settings_failed", "无法读取 Schwab 配置")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, rec.view())
}

func (rec schwabRecord) view() schwabSettingsView {
	view := schwabSettingsView{
		AppKey: rec.AppKey, CallbackURL: rec.CallbackURL,
		HasAppSecret: rec.AppSecret != "", Connected: rec.RefreshToken != "" && !rec.ReauthorizationRequired,
		LastError:               rec.LastError,
		ReauthorizationRequired: rec.ReauthorizationRequired,
	}
	if !rec.TokenExpiresAt.IsZero() {
		expires := rec.TokenExpiresAt.UTC()
		view.TokenExpiresAt = &expires
	}
	return view
}

func (m *Module) saveSchwab(w http.ResponseWriter, r *http.Request) {
	var input schwabSettingsInput
	if httpapi.Decode(w, r, &input, 16*1024) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "无效的 Schwab 配置")
		return
	}
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	old, err := m.loadSchwab(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "schwab_settings_failed", "无法读取 Schwab 配置")
		return
	}
	input.AppKey = strings.TrimSpace(input.AppKey)
	input.AppSecret = strings.TrimSpace(input.AppSecret)
	input.CallbackURL = strings.TrimSpace(input.CallbackURL)
	if input.AppSecret == "" && old.AppSecret != "" && input.CallbackURL != old.CallbackURL && input.AppKey != "" {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "更换回调地址时请重新填写 App Secret")
		return
	}
	if input.AppSecret == "" {
		if input.AppKey != old.AppKey && old.AppSecret != "" && input.AppKey != "" {
			httpapi.Error(w, http.StatusBadRequest, "invalid_request", "更换 App Key 时请重新填写 App Secret")
			return
		}
		if input.AppKey == old.AppKey {
			input.AppSecret = old.AppSecret
		}
	}
	if err := validateSchwabInput(input); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	rec := old
	rec.AppKey, rec.AppSecret, rec.CallbackURL = input.AppKey, input.AppSecret, input.CallbackURL
	credentialsChanged := rec.AppKey != old.AppKey || rec.AppSecret != old.AppSecret || rec.CallbackURL != old.CallbackURL
	if credentialsChanged {
		rec.AccessToken, rec.RefreshToken, rec.StreamerInfo = "", "", ""
		rec.TokenExpiresAt, rec.OAuthState, rec.OAuthStateExpiresAt = time.Time{}, "", time.Time{}
		rec.ReauthorizationRequired = false
	}
	if rec.AppKey == "" {
		rec = schwabRecord{}
	}
	rec.LastError = ""
	if err := m.storeSchwab(r.Context(), rec); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "schwab_settings_failed", "无法保存 Schwab 配置")
		return
	}
	if credentialsChanged {
		m.streamer.reset()
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, rec.view())
}

func validateSchwabInput(input schwabSettingsInput) error {
	if len(input.AppKey) > 256 || len(input.AppSecret) > 512 {
		return errors.New("App Key 或 App Secret 过长")
	}
	if input.CallbackURL == "" {
		if input.AppKey != "" || input.AppSecret != "" {
			return errors.New("请填写与 Schwab 应用一致的回调地址")
		}
		return nil
	}
	parsed, err := url.Parse(input.CallbackURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("Schwab 回调地址必须是可访问工作台的 HTTPS 地址，不能包含凭据、查询参数或片段")
	}
	if parsed.Path != "/oauth/schwab" {
		return errors.New("回调路径必须是 /oauth/schwab")
	}
	if len(input.CallbackURL) > 2048 {
		return errors.New("回调地址过长")
	}
	if input.AppKey == "" || input.AppSecret == "" {
		return errors.New("保存回调地址时需要 App Key 和 App Secret")
	}
	return nil
}

func (m *Module) disconnectSchwab(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := httpapi.Decode(w, r, &input, 4096); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "请提交空 JSON 对象")
		return
	}
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	rec, err := m.loadSchwab(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "schwab_settings_failed", "无法读取 Schwab 配置")
		return
	}
	rec.AccessToken, rec.RefreshToken, rec.StreamerInfo = "", "", ""
	rec.TokenExpiresAt, rec.OAuthState, rec.OAuthStateExpiresAt = time.Time{}, "", time.Time{}
	rec.ReauthorizationRequired = false
	rec.LastError = ""
	if err := m.storeSchwab(r.Context(), rec); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "schwab_settings_failed", "无法断开 Schwab")
		return
	}
	m.streamer.reset()
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, rec.view())
}

func (m *Module) oauthLogin(w http.ResponseWriter, r *http.Request) {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	rec, err := m.loadSchwab(r.Context())
	if err != nil {
		writeHTML(w, http.StatusInternalServerError, "无法读取 Schwab 配置")
		return
	}
	if rec.AppKey == "" || rec.AppSecret == "" || rec.CallbackURL == "" {
		writeHTML(w, http.StatusConflict, "请先在设置中保存 Schwab App Key、Secret 和回调地址。")
		return
	}
	if err := validateSchwabInput(schwabSettingsInput{AppKey: rec.AppKey, AppSecret: rec.AppSecret, CallbackURL: rec.CallbackURL}); err != nil {
		writeHTML(w, http.StatusBadRequest, html.EscapeString(err.Error())+`。<a href="/settings?tab=modules">返回设置修正回调地址</a>。`)
		return
	}
	state, err := randomHex(16)
	if err != nil {
		writeHTML(w, http.StatusInternalServerError, "无法创建授权状态")
		return
	}
	rec.OAuthState = state
	rec.OAuthStateExpiresAt = m.now().UTC().Add(oauthStateTTL)
	rec.LastError = ""
	if err := m.storeSchwab(r.Context(), rec); err != nil {
		writeHTML(w, http.StatusInternalServerError, "无法保存授权状态")
		return
	}
	target := m.schwabAPI + "/v1/oauth/authorize?" + url.Values{
		"client_id":     {rec.AppKey},
		"redirect_uri":  {rec.CallbackURL},
		"response_type": {"code"},
		"state":         {state},
	}.Encode()
	http.Redirect(w, r, target, http.StatusFound)
}

func (m *Module) oauthCallback(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || state == "" {
		writeHTML(w, http.StatusBadRequest, "缺少授权 code 或 state。")
		return
	}
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	rec, err := m.loadSchwab(r.Context())
	if err != nil {
		writeHTML(w, http.StatusInternalServerError, "无法读取 Schwab 配置")
		return
	}
	if rec.OAuthState == "" || rec.OAuthState != state || rec.OAuthStateExpiresAt.Before(m.now().UTC()) {
		writeHTML(w, http.StatusBadRequest, "授权状态无效或已过期，请重新登录。")
		return
	}
	if err := m.exchangeToken(r.Context(), &rec, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {rec.CallbackURL},
	}); err != nil {
		rec.LastError = err.Error()
		_ = m.storeSchwab(r.Context(), rec)
		writeHTML(w, http.StatusBadGateway, "换取令牌失败："+html.EscapeString(err.Error()))
		return
	}
	rec.OAuthState, rec.OAuthStateExpiresAt = "", time.Time{}
	rec.LastError = ""
	rec.ReauthorizationRequired = false
	if err := m.storeSchwab(r.Context(), rec); err != nil {
		writeHTML(w, http.StatusInternalServerError, "授权成功但无法保存令牌")
		return
	}
	m.streamer.reset()
	// Commit a same-site document before navigation so Strict session cookies
	// become available again after Schwab's cross-site redirect chain.
	writeHTML(w, http.StatusOK, `<meta http-equiv="refresh" content="0;url=/investment"><h1>Schwab 授权成功</h1><p>正在返回投资…</p><p>若未自动跳转，<a href="/investment">点击返回工作台</a>。</p>`)
}

func (m *Module) oauthRefreshHTTP(w http.ResponseWriter, r *http.Request) {
	var input struct{}
	if err := httpapi.Decode(w, r, &input, 4096); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "请提交空 JSON 对象")
		return
	}
	m.tokenMu.Lock()
	rec, err := m.loadSchwab(r.Context())
	m.tokenMu.Unlock()
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "schwab_settings_failed", "无法读取 Schwab 配置")
		return
	}
	if rec.ReauthorizationRequired {
		httpapi.Error(w, http.StatusConflict, "schwab_reauthorization_required", errSchwabReauthorizationRequired.Error())
		return
	}
	if rec.RefreshToken == "" {
		httpapi.Error(w, http.StatusConflict, "schwab_disconnected", "尚未连接 Schwab，请先完成授权")
		return
	}
	if err := m.refreshAccessToken(r.Context()); err != nil {
		if errors.Is(err, errSchwabReauthorizationRequired) {
			httpapi.Error(w, http.StatusConflict, "schwab_reauthorization_required", err.Error())
			return
		}
		httpapi.Error(w, http.StatusBadGateway, "schwab_refresh_failed", err.Error())
		return
	}
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	rec, err = m.loadSchwab(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "schwab_settings_failed", "无法读取 Schwab 配置")
		return
	}
	httpapi.Write(w, http.StatusOK, rec.view())
}

func (m *Module) refreshAccessToken(ctx context.Context) error {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	return m.refreshAccessTokenLocked(ctx)
}

// Caller holds tokenMu so credentials and the returned token belong to one connection.
func (m *Module) refreshAccessTokenLocked(ctx context.Context) error {
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return err
	}
	if rec.ReauthorizationRequired {
		return errSchwabReauthorizationRequired
	}
	if rec.RefreshToken == "" {
		return nil
	}
	if rec.AccessToken != "" && rec.TokenExpiresAt.After(m.now().UTC().Add(tokenRefreshSkew)) {
		return nil
	}
	if err := m.exchangeToken(ctx, &rec, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {rec.RefreshToken},
	}); err != nil {
		rec.LastError = err.Error()
		if errors.Is(err, errSchwabReauthorizationRequired) {
			rec.ReauthorizationRequired = true
			rec.AccessToken, rec.RefreshToken, rec.StreamerInfo = "", "", ""
			rec.TokenExpiresAt = time.Time{}
		}
		if storeErr := m.storeSchwab(ctx, rec); storeErr != nil {
			return storeErr
		}
		if rec.ReauthorizationRequired {
			m.streamer.reset()
		}
		return err
	}
	rec.LastError = ""
	if err := m.storeSchwab(ctx, rec); err != nil {
		return err
	}
	m.streamer.reset()
	return nil
}

func (m *Module) ensureAccessToken(ctx context.Context) (string, error) {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	if err := m.refreshAccessTokenLocked(ctx); err != nil {
		return "", err
	}
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return "", err
	}
	if rec.AccessToken == "" {
		return "", errors.New("尚未连接 Schwab，请先完成授权")
	}
	return rec.AccessToken, nil
}

func (m *Module) exchangeToken(ctx context.Context, rec *schwabRecord, params url.Values) error {
	if rec.AppKey == "" || rec.AppSecret == "" {
		return errors.New("缺少 App Key 或 App Secret")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.schwabAPI+"/v1/oauth/token", strings.NewReader(params.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(rec.AppKey+":"+rec.AppSecret)))
	req.Header.Set("Accept", "application/json")
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return errors.New("暂时无法连接 Schwab，请稍后重试。")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSchwabTokenBytes))
	if err != nil {
		return errors.New("读取 Schwab 授权响应失败，请稍后重试。")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return schwabTokenFailure(resp.StatusCode, body, params.Get("grant_type") == "refresh_token")
	}
	var token schwabTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return errors.New("Schwab 授权响应无效，请稍后重试。")
	}
	if token.AccessToken == "" {
		return errors.New("token 响应缺少 access_token")
	}
	rec.AccessToken = token.AccessToken
	if token.RefreshToken != "" {
		rec.RefreshToken = token.RefreshToken
	}
	expires := token.ExpiresIn
	if expires <= 0 {
		expires = 1800
	}
	rec.TokenExpiresAt = m.now().UTC().Add(time.Duration(expires) * time.Second)
	rec.StreamerInfo = ""
	return nil
}

func (m *Module) proxySchwab(apiPrefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := chi.URLParam(r, "*")
		if !validProxyPath(rest) {
			httpapi.Error(w, http.StatusBadRequest, "invalid_request", "无效的 Schwab 路径")
			return
		}
		token, err := m.ensureAccessToken(r.Context())
		if err != nil {
			httpapi.Error(w, http.StatusConflict, "schwab_disconnected", err.Error())
			return
		}
		target := strings.TrimRight(m.schwabAPI, "/") + apiPrefix + "/" + rest
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		var body io.Reader = http.MaxBytesReader(w, r.Body, maxSchwabProxyBody)
		if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
			body = http.NoBody
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
		if err != nil {
			httpapi.Error(w, http.StatusBadGateway, "schwab_proxy_failed", "无法创建 Schwab 请求")
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		if accept := r.Header.Get("Accept"); accept != "" {
			req.Header.Set("Accept", accept)
		} else {
			req.Header.Set("Accept", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := m.httpClient.Do(req)
		if err != nil {
			httpapi.Error(w, http.StatusBadGateway, "schwab_proxy_failed", "请求 Schwab 失败")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			// Refresh the connection state, but never replay an order or other request.
			if err := m.refreshRejectedAccessToken(r.Context(), token); err != nil {
				code := "schwab_refresh_failed"
				if errors.Is(err, errSchwabReauthorizationRequired) {
					code = "schwab_reauthorization_required"
				}
				httpapi.Error(w, http.StatusConflict, code, err.Error())
				return
			}
		}
		copySchwabHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 16<<20))
	}
}

func copySchwabHeaders(dst, src http.Header) {
	for key, values := range src {
		switch strings.ToLower(key) {
		case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailers", "transfer-encoding", "upgrade", "set-cookie":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func validProxyPath(path string) bool {
	if path == "" || strings.Contains(path, "\\") || strings.Contains(path, "..") || strings.Contains(path, "://") {
		return false
	}
	return !strings.HasPrefix(path, "/")
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func unixMilli(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func timeUnixMilli(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func trimErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		return text[:300]
	}
	if text == "" {
		return "empty body"
	}
	return text
}

func writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<!doctype html><meta charset=\"utf-8\"><title>Schwab</title><body style=\"font-family:sans-serif;padding:24px;line-height:1.6\">"+body+"</body>")
}
