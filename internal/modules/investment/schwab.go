package investment

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

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
