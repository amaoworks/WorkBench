package investment

import (
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"workbench/internal/foundation/httpapi"
)

const oauthStateTTL = 10 * time.Minute

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

func writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, "<!doctype html><meta charset=\"utf-8\"><title>Schwab</title><body style=\"font-family:sans-serif;padding:24px;line-height:1.6\">"+body+"</body>")
}
