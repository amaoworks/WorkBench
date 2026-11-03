package auth

import (
	"database/sql"
	"net/http"
	"time"

	dbsqlc "workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/httpapi"
)

// Browser grants authenticate a WebView and its same-origin subresources without
// exposing the device's access or refresh token to JavaScript or a URL.
func (s *Service) BrowserGrantHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if DeviceSessionID(r) == "" {
		httpapi.Error(w, 400, "bearer_required", "请使用设备登录凭据")
		return
	}
	var input struct{}
	if httpapi.Decode(w, r, &input, 256) != nil {
		httpapi.Error(w, 400, "invalid_request", "请提交空 JSON 对象")
		return
	}
	ticket, err := randomToken()
	if err != nil {
		tokenFailed(w)
		return
	}
	expires := s.now().Add(time.Minute).UTC()
	if s.queries.InsertBrowserGrant(r.Context(), dbsqlc.InsertBrowserGrantParams{TokenHash: tokenHash(ticket), SessionID: DeviceSessionID(r), ExpiresAt: expires.UnixMilli()}) != nil {
		tokenFailed(w)
		return
	}
	httpapi.Write(w, 200, map[string]any{"urlPath": "/api/auth/browser-session?ticket=" + ticket, "expiresAt": expires})
}

func (s *Service) BrowserSessionHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	ticket := r.URL.Query().Get("ticket")
	if s.mode != ModePassword || len(ticket) != 43 {
		invalidToken(w)
		return
	}
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		tokenFailed(w)
		return
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	grant, err := q.GetBrowserGrant(r.Context(), tokenHash(ticket))
	if err != nil || grant.UsedAt.Valid || grant.ExpiresAt <= s.now().UnixMilli() {
		invalidToken(w)
		return
	}
	device, err := q.GetDeviceSession(r.Context(), grant.SessionID)
	if err != nil || device.RevokedAt.Valid || device.ExpiresAt <= s.now().UnixMilli() {
		invalidToken(w)
		return
	}
	credential, err := q.GetAuthCredential(r.Context())
	if err != nil {
		tokenFailed(w)
		return
	}
	if credential.UpdatedAt != device.CredentialVersion || credential.MustChangePassword != 0 {
		invalidToken(w)
		return
	}
	count, err := q.ConsumeBrowserGrant(r.Context(), dbsqlc.ConsumeBrowserGrantParams{TokenHash: grant.TokenHash, UsedAt: sql.NullInt64{Int64: s.now().UnixMilli(), Valid: true}})
	if err != nil {
		tokenFailed(w)
		return
	}
	if count != 1 {
		invalidToken(w)
		return
	}
	if tx.Commit() != nil {
		tokenFailed(w)
		return
	}
	if s.sessions.RenewToken(r.Context()) != nil {
		tokenFailed(w)
		return
	}
	s.sessions.Put(r.Context(), "authenticated", true)
	s.sessions.Put(r.Context(), "credentialVersion", credential.UpdatedAt)
	s.sessions.Put(r.Context(), "deviceSessionID", grant.SessionID)
	if s.trackBrowser(r) != nil {
		_ = s.sessions.Destroy(r.Context())
		tokenFailed(w)
		return
	}
	http.Redirect(w, r, "/investment/terminal", http.StatusSeeOther)
}

func (s *Service) browserDeviceActive(r *http.Request) bool {
	id := s.sessions.GetString(r.Context(), "deviceSessionID")
	if id == "" {
		return true
	}
	device, err := s.queries.GetDeviceSession(r.Context(), id)
	return err == nil && !device.RevokedAt.Valid && device.ExpiresAt > s.now().UnixMilli()
}
