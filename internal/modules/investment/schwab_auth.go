package investment

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const tokenRefreshSkew = 3 * time.Minute

const maxSchwabTokenBytes = 1 << 20

var errSchwabReauthorizationRequired = errors.New("Schwab 授权已失效，请重新授权后继续使用行情、持仓和交易功能。")

type schwabTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
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

// An invalid_client response alone can also mean an incorrect app secret.
// Schwab's expired refresh token response identifies the token in its description.
func schwabTokenFailure(status int, body []byte, refreshing bool) error {
	if status == http.StatusTooManyRequests || status >= 500 {
		return errors.New("Schwab 服务暂时不可用，请稍后重试。")
	}
	var response struct {
		Code        string `json:"error"`
		Description string `json:"error_description"`
	}
	if json.Unmarshal(body, &response) == nil {
		description := strings.NewReplacer("_", " ", "-", " ").Replace(strings.ToLower(response.Description))
		invalidRefresh := strings.Contains(description, "refresh token") &&
			(strings.Contains(description, "invalid") || strings.Contains(description, "expired") || strings.Contains(description, "revoked"))
		if refreshing && (status == http.StatusBadRequest || status == http.StatusUnauthorized) &&
			(response.Code == "invalid_grant" || response.Code == "invalid_client" && invalidRefresh) {
			return errSchwabReauthorizationRequired
		}
		if response.Code == "invalid_client" || response.Code == "unauthorized_client" {
			return errors.New("Schwab 应用认证失败，请到设置中检查 App Key 和 App Secret。")
		}
		if response.Code == "invalid_grant" {
			return errors.New("Schwab 授权链接已失效，请重新登录授权。")
		}
	}
	// Do not expose the upstream body: OAuth errors may echo credentials or tokens.
	return errors.New("Schwab 授权请求失败，请稍后重试。")
}

// Only invalidate the access token used by the rejected request. A late response
// from an older connection must not invalidate a newly authorized connection.
func (m *Module) refreshRejectedAccessToken(ctx context.Context, rejected string) error {
	m.tokenMu.Lock()
	defer m.tokenMu.Unlock()
	rec, err := m.loadSchwab(ctx)
	if err != nil {
		return err
	}
	if rec.AccessToken != rejected {
		return nil
	}
	rec.TokenExpiresAt = time.Time{}
	if err := m.storeSchwab(ctx, rec); err != nil {
		return err
	}
	return m.refreshAccessTokenLocked(ctx)
}
