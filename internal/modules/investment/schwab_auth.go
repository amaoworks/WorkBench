package investment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

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
