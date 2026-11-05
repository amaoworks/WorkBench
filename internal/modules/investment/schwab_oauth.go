package investment

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
)

const oauthStateTTL = 10 * time.Minute

const (
	mobileOAuthPending   = "pending"
	mobileOAuthSucceeded = "succeeded"
	mobileOAuthFailed    = "failed"
)

type mobileOAuthSession struct {
	State     string
	Status    string
	Error     string
	ExpiresAt time.Time
}

type mobileOAuthStartView struct {
	AuthorizationURL string    `json:"authorizationUrl"`
	StatusID         string    `json:"statusId"`
	Status           string    `json:"status"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type mobileOAuthStatusView struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type oauthStartError struct {
	Status  int
	Message string
}

func (e *oauthStartError) Error() string { return e.Message }

func (m *Module) oauthLogin(w http.ResponseWriter, r *http.Request) {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	target, _, _, err := m.beginSchwabOAuth(r.Context())
	if err != nil {
		startErr := new(oauthStartError)
		if !errors.As(err, &startErr) {
			writeHTML(w, http.StatusInternalServerError, "无法创建授权状态")
			return
		}
		message := html.EscapeString(startErr.Message)
		if startErr.Status == http.StatusBadRequest {
			if contracts.IsAdmin(r.Context()) {
				message += `。<a href="/admin/settings?tab=modules&amp;module=investment">前往后台设置修正回调地址</a>。`
			} else {
				message += "。联系管理员修正回调地址。"
			}
		}
		writeHTML(w, startErr.Status, message)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (m *Module) oauthStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	statusID, err := randomHex(24)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "schwab_oauth_start_failed", "无法创建状态查询标识")
		return
	}
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	target, state, expiresAt, err := m.beginSchwabOAuth(r.Context())
	if err != nil {
		startErr := new(oauthStartError)
		if errors.As(err, &startErr) {
			httpapi.Error(w, startErr.Status, "schwab_oauth_unavailable", startErr.Message)
			return
		}
		httpapi.Error(w, http.StatusInternalServerError, "schwab_oauth_start_failed", "无法创建授权状态")
		return
	}
	m.oauthMu.Lock()
	m.expireMobileOAuthSessionsLocked(m.now().UTC())
	m.oauthSessions[statusID] = mobileOAuthSession{State: state, Status: mobileOAuthPending, ExpiresAt: expiresAt}
	m.oauthMu.Unlock()
	httpapi.Write(w, http.StatusCreated, mobileOAuthStartView{
		AuthorizationURL: target, StatusID: statusID, Status: mobileOAuthPending, ExpiresAt: expiresAt,
	})
}

func (m *Module) oauthStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	statusID := strings.TrimSpace(r.PathValue("id"))
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	m.oauthMu.Lock()
	now := m.now().UTC()
	m.expireMobileOAuthSessionsLocked(now)
	session, ok := m.oauthSessions[statusID]
	if ok && session.Status == mobileOAuthPending && !session.ExpiresAt.After(now) {
		session.Status = mobileOAuthFailed
		session.Error = "授权已过期，请重新发起"
		m.oauthSessions[statusID] = session
	}
	m.oauthMu.Unlock()
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "schwab_oauth_status_not_found", "授权状态不存在")
		return
	}
	httpapi.Write(w, http.StatusOK, mobileOAuthStatusView{Status: session.Status, Error: session.Error})
}

// beginSchwabOAuth requires tokenMu and stores the single-use state consumed by the public callback.
func (m *Module) beginSchwabOAuth(ctx context.Context) (string, string, time.Time, error) {
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return "", "", time.Time{}, &oauthStartError{Status: http.StatusInternalServerError, Message: "无法读取 Schwab 配置"}
	}
	if rec.AppKey == "" || rec.AppSecret == "" || rec.CallbackURL == "" {
		return "", "", time.Time{}, &oauthStartError{Status: http.StatusConflict, Message: "Schwab 应用未配置"}
	}
	if err := validateSchwabInput(schwabSettingsInput{AppKey: rec.AppKey, AppSecret: rec.AppSecret, CallbackURL: rec.CallbackURL}); err != nil {
		return "", "", time.Time{}, &oauthStartError{Status: http.StatusBadRequest, Message: err.Error()}
	}
	state, err := randomHex(16)
	if err != nil {
		return "", "", time.Time{}, err
	}
	expiresAt := m.now().UTC().Add(oauthStateTTL)
	rec.OAuthState = state
	rec.OAuthStateExpiresAt = expiresAt
	rec.LastError = ""
	if err := m.storeSchwab(ctx, rec); err != nil {
		return "", "", time.Time{}, &oauthStartError{Status: http.StatusInternalServerError, Message: "无法保存授权状态"}
	}
	m.supersedeMobileOAuth(state)
	target := m.schwabAPI + "/v1/oauth/authorize?" + url.Values{
		"client_id": {rec.AppKey}, "redirect_uri": {rec.CallbackURL}, "response_type": {"code"}, "state": {state},
	}.Encode()
	return target, state, expiresAt, nil
}

func (m *Module) supersedeMobileOAuth(state string) {
	m.oauthMu.Lock()
	defer m.oauthMu.Unlock()
	for id, session := range m.oauthSessions {
		if session.State != state && session.Status == mobileOAuthPending {
			session.Status = mobileOAuthFailed
			session.Error = "授权已被新的请求替代，请重新发起"
			m.oauthSessions[id] = session
		}
	}
}

func (m *Module) oauthCallback(w http.ResponseWriter, r *http.Request) {
	if m.root == nil {
		state := strings.TrimSpace(r.URL.Query().Get("state"))
		id, err := m.queries.FindSchwabOAuthOwner(r.Context(), state)
		if err != nil || id == "" {
			writeHTML(w, 400, "授权状态无效或已过期，请重新登录。")
			return
		}
		ctx := contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: id, Role: contracts.RoleUser})
		if _, err := m.requireUser(ctx); err != nil {
			writeHTML(w, 403, "用户不可用")
			return
		}
		m.userModule(id).oauthCallback(w, r.WithContext(ctx))
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" {
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
	if rec.OAuthState == "" || rec.OAuthState != state {
		m.finishMobileOAuth(state, mobileOAuthFailed, "授权状态无效或已过期，请重新发起")
		writeHTML(w, http.StatusBadRequest, "授权状态无效或已过期，请重新登录。")
		return
	}
	expired := !rec.OAuthStateExpiresAt.After(m.now().UTC())
	// Consume before contacting Schwab, including callbacks that fail or omit code.
	rec.OAuthState, rec.OAuthStateExpiresAt = "", time.Time{}
	if err := m.storeSchwab(r.Context(), rec); err != nil {
		writeHTML(w, http.StatusInternalServerError, "无法消费授权状态")
		return
	}
	// The consumed state write advances the optimistic connection version.
	rec.ConnectionVersion++
	if expired {
		m.finishMobileOAuth(state, mobileOAuthFailed, "授权状态无效或已过期，请重新发起")
		writeHTML(w, http.StatusBadRequest, "授权状态无效或已过期，请重新登录。")
		return
	}
	if code == "" {
		m.finishMobileOAuth(state, mobileOAuthFailed, "授权未完成，请重新发起")
		writeHTML(w, http.StatusBadRequest, "缺少授权 code。")
		return
	}
	if err := m.exchangeToken(r.Context(), &rec, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {rec.CallbackURL},
	}); err != nil {
		rec.LastError = err.Error()
		_ = m.storeSchwab(r.Context(), rec)
		m.finishMobileOAuth(state, mobileOAuthFailed, "换取令牌失败，请重新发起授权")
		writeHTML(w, http.StatusBadGateway, "换取令牌失败："+html.EscapeString(err.Error()))
		return
	}
	rec.LastError = ""
	rec.ReauthorizationRequired = false
	rec.AuthorizedAt = m.now().UTC()
	if err := m.storeSchwab(r.Context(), rec); err != nil {
		m.finishMobileOAuth(state, mobileOAuthFailed, "授权成功但无法保存令牌")
		writeHTML(w, http.StatusInternalServerError, "授权成功但无法保存令牌")
		return
	}
	m.streamer.reset()
	m.finishMobileOAuth(state, mobileOAuthSucceeded, "")
	// Commit a same-site document before navigation so Strict session cookies
	// become available again after Schwab's cross-site redirect chain.
	writeHTML(w, http.StatusOK, "正在返回投资…")
}

func (m *Module) finishMobileOAuth(state, status, message string) {
	if state == "" {
		return
	}
	m.oauthMu.Lock()
	defer m.oauthMu.Unlock()
	for id, session := range m.oauthSessions {
		if session.State == state && session.Status == mobileOAuthPending {
			session.Status, session.Error = status, message
			m.oauthSessions[id] = session
		}
	}
}

func (m *Module) expireMobileOAuthSessionsLocked(now time.Time) {
	for id, session := range m.oauthSessions {
		if session.ExpiresAt.Add(oauthStateTTL).Before(now) {
			delete(m.oauthSessions, id)
		}
	}
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
	httpapi.Write(w, http.StatusOK, rec.viewFor(r.Context()))
}

func writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	title, role, refresh := "授权未完成", "alert", ""
	if status == http.StatusOK {
		title, role = "授权成功", "status"
		refresh = `<meta http-equiv="refresh" content="0;url=/investment">`
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, oauthResultPage(refresh, role, title, body))
}

func oauthResultPage(refresh, role, title, body string) string {
	return `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">` +
		refresh + `<title>Schwab</title><style>` + oauthResultCSS + `</style></head><body><main class="page"><section class="card" role="` +
		role + `"><div class="brand"><span class="mark" aria-hidden="true">` + oauthMark + `</span><span>Workbench</span></div><h1>` +
		title + `</h1><p class="message">` + body + `</p><a class="button" href="/investment">返回投资</a></section></main></body></html>`
}

const oauthMark = `<svg xmlns="http://www.w3.org/2000/svg" width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><path d="m14.31 8 5.74 9.94"/><path d="M9.69 8h11.48"/><path d="m7.38 12 5.74-9.94"/><path d="M9.69 16 3.95 6.06"/><path d="M14.31 16H2.83"/><path d="m16.62 12-5.74 9.94"/></svg>`

const oauthResultCSS = `:root{color-scheme:light;--ink:#000;--heading:#000;--canvas:#fff;--surface:#fff;--card-border:#e0e0e0;--muted:#707070;--brand:#2962ff;--cta:#000;--on-cta:#fff;font-family:-apple-system,BlinkMacSystemFont,"Trebuchet MS",Roboto,Ubuntu,"PingFang SC","Hiragino Sans GB","Noto Sans CJK SC","Microsoft YaHei",sans-serif}
@media (prefers-color-scheme:dark){:root{color-scheme:dark;--ink:#dbdbdb;--heading:#fff;--canvas:#000;--surface:#0f0f0f;--card-border:#464646;--muted:#8c8c8c;--brand:#5b9cf6;--cta:#fff;--on-cta:#000}}
*{box-sizing:border-box}body{margin:0;background:var(--canvas);color:var(--ink);font-size:15px;line-height:1.55}
.page{min-height:100vh;display:grid;place-items:center;padding:24px}
.card{width:100%;max-width:390px;padding:32px;border:1px solid var(--card-border);border-radius:16px;background:var(--surface)}
.brand{display:flex;align-items:center;gap:10px;margin-bottom:32px;font-weight:600}
.mark{display:grid;place-items:center;width:34px;height:34px;flex-shrink:0;border-radius:10px;background:var(--cta);color:var(--on-cta)}
h1{margin:0;color:var(--heading);font-size:23px;font-weight:600;letter-spacing:-.03em}
.message{margin:8px 0 0;color:var(--muted);font-size:14px;overflow-wrap:anywhere}
.message a{color:var(--brand)}
.button{display:flex;align-items:center;justify-content:center;width:100%;margin-top:24px;min-height:38px;padding:9px 14px;border-radius:999px;background:var(--cta);color:var(--on-cta);font-size:14px;font-weight:500;line-height:1.4;text-decoration:none}
.button:hover{opacity:.85}.button:active{opacity:.75}
a:focus-visible{outline:2px solid var(--brand);outline-offset:3px}
@media (max-width:600px){.card{padding:26px}}`
