package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/httpapi"
)

const (
	accessLifetime = 15 * time.Minute
	deviceLifetime = 30 * 24 * time.Hour
)

var errInvalidToken = errors.New("invalid token")

type accessClaims struct {
	SessionID string `json:"sid"`
	Version   int64  `json:"ver"`
	jwt.RegisteredClaims
}

type deviceContextKey struct{}

func DeviceSessionID(r *http.Request) string {
	claims, _ := r.Context().Value(deviceContextKey{}).(*accessClaims)
	if claims == nil {
		return ""
	}
	return claims.SessionID
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value[:]), nil
}

func (s *Service) ensureSigningKey(ctx context.Context) error {
	_, err := s.queries.GetActiveSigningKey(ctx)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	return s.queries.InsertSigningKey(ctx, dbsqlc.InsertSigningKeyParams{ID: uuid.NewString(), PrivateKey: key, CreatedAt: s.now().UnixMilli()})
}

type tokenResponse struct {
	AccessToken        string    `json:"accessToken"`
	RefreshToken       string    `json:"refreshToken"`
	TokenType          string    `json:"tokenType"`
	ExpiresIn          int64     `json:"expiresIn"`
	RefreshExpiresAt   time.Time `json:"refreshExpiresAt"`
	SessionID          string    `json:"sessionId"`
	MustChangePassword bool      `json:"mustChangePassword"`
}

func (s *Service) issueTokens(ctx context.Context, q *dbsqlc.Queries, session dbsqlc.AuthDeviceSession, mustChange bool) (tokenResponse, error) {
	now := s.now()
	key, err := q.GetActiveSigningKey(ctx)
	if err != nil {
		return tokenResponse{}, err
	}
	if len(key.PrivateKey) != ed25519.PrivateKeySize {
		return tokenResponse{}, errors.New("invalid signing key")
	}
	expires := min(now.Add(accessLifetime).Unix(), session.ExpiresAt/1000)
	claims := accessClaims{SessionID: session.ID, Version: session.CredentialVersion, RegisteredClaims: jwt.RegisteredClaims{
		Issuer: "workbench", Subject: session.UserID, Audience: jwt.ClaimStrings{"workbench-api"},
		ExpiresAt: jwt.NewNumericDate(time.Unix(expires, 0)), IssuedAt: jwt.NewNumericDate(now), ID: uuid.NewString(),
	}}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = key.ID
	token.Header["typ"] = "at+jwt"
	access, err := token.SignedString(ed25519.PrivateKey(key.PrivateKey))
	if err != nil {
		return tokenResponse{}, err
	}
	refresh, err := randomToken()
	if err != nil {
		return tokenResponse{}, err
	}
	if err := q.InsertRefreshToken(ctx, dbsqlc.InsertRefreshTokenParams{TokenHash: tokenHash(refresh), SessionID: session.ID, CreatedAt: now.UnixMilli()}); err != nil {
		return tokenResponse{}, err
	}
	return tokenResponse{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: expires - now.Unix(), RefreshExpiresAt: time.UnixMilli(session.ExpiresAt).UTC(), SessionID: session.ID, MustChangePassword: mustChange}, nil
}

func (s *Service) validateAccess(ctx context.Context, raw string) (*accessClaims, error) {
	claims := &accessClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok || t.Header["typ"] != "at+jwt" {
			return nil, errInvalidToken
		}
		key, err := s.queries.GetSigningKey(ctx, kid)
		if err != nil || len(key.PrivateKey) != ed25519.PrivateKeySize {
			return nil, errInvalidToken
		}
		if key.RetiredAt.Valid && s.now().UnixMilli() >= key.RetiredAt.Int64+accessLifetime.Milliseconds() {
			return nil, errInvalidToken
		}
		return ed25519.PrivateKey(key.PrivateKey).Public(), nil
	}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer("workbench"), jwt.WithAudience("workbench-api"), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(s.now))
	if err != nil || !token.Valid || claims.SessionID == "" || claims.Subject == "" || claims.IssuedAt == nil || claims.ID == "" {
		return nil, errInvalidToken
	}
	if err := s.checkDevice(ctx, claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *Service) checkDevice(ctx context.Context, claims *accessClaims) error {
	if claims.ExpiresAt == nil || !s.now().Before(claims.ExpiresAt.Time) {
		return errInvalidToken
	}
	session, err := s.queries.GetDeviceSession(ctx, claims.SessionID)
	if err != nil {
		return err
	}
	credential, err := s.queries.GetAuthUser(ctx, session.UserID)
	if err != nil {
		return err
	}
	if session.RevokedAt.Valid || session.ExpiresAt <= s.now().UnixMilli() || session.UserID != claims.Subject || session.CredentialVersion != claims.Version || credential.CredentialVersion != claims.Version || credential.Status != "active" {
		return errInvalidToken
	}
	if s.now().UnixMilli()-session.LastSeenAt >= time.Minute.Milliseconds() {
		return s.queries.TouchDeviceSession(ctx, dbsqlc.TouchDeviceSessionParams{ID: session.ID, LastSeenAt: s.now().UnixMilli()})
	}
	return nil
}

func (s *Service) TokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.mode != ModePassword {
		httpapi.Error(w, 400, "password_mode_required", "请先启用密码登录")
		return
	}
	var input struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		DeviceName string `json:"deviceName"`
		Platform   string `json:"platform"`
	}
	if httpapi.Decode(w, r, &input, 4096) != nil {
		httpapi.Error(w, 400, "invalid_request", "登录请求格式不正确")
		return
	}
	input.DeviceName = strings.TrimSpace(input.DeviceName)
	if input.DeviceName == "" || utf8.RuneCountInString(input.DeviceName) > 100 || (input.Platform != "ios" && input.Platform != "android" && input.Platform != "other") {
		httpapi.Error(w, 400, "invalid_device", "请提供设备名称和平台")
		return
	}
	if retry, err := s.acquireLogin(r, input.Username); err != nil {
		writePasswordLimit(w, retry, err)
		return
	}
	defer s.passwordLimit.release()
	credential, valid, err := s.verifyLogin(r.Context(), input.Username, input.Password)
	if err != nil {
		httpapi.Error(w, 500, "authentication_failed", "无法验证密码")
		return
	}
	if !valid {
		httpapi.Error(w, 401, "invalid_credentials", "用户名或密码不正确")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		tokenFailed(w)
		return
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	now := s.now().UnixMilli()
	session := dbsqlc.AuthDeviceSession{ID: uuid.NewString(), UserID: credential.ID, DeviceName: input.DeviceName, Platform: input.Platform, CredentialVersion: credential.CredentialVersion, CreatedAt: now, LastSeenAt: now, ExpiresAt: s.now().Add(deviceLifetime).UnixMilli()}
	err = q.InsertDeviceSession(r.Context(), dbsqlc.InsertDeviceSessionParams{ID: session.ID, UserID: session.UserID, DeviceName: session.DeviceName, Platform: session.Platform, CredentialVersion: session.CredentialVersion, CreatedAt: now, LastSeenAt: now, ExpiresAt: session.ExpiresAt})
	if err != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	result, err := s.issueTokens(r.Context(), q, session, credential.MustChangePassword != 0)
	if err != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	if err := tx.Commit(); err != nil {
		tokenFailed(w)
		return
	}
	httpapi.Write(w, 200, result)
}

func tokenFailed(w http.ResponseWriter) {
	httpapi.Error(w, 500, "token_failed", "无法保存登录凭据")
}
func invalidToken(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
	httpapi.Error(w, 401, "invalid_token", "登录已失效，请重新登录")
}

func (s *Service) RefreshHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.mode != ModePassword {
		httpapi.Error(w, 400, "password_mode_required", "请先启用密码登录")
		return
	}
	var input struct {
		RefreshToken string `json:"refreshToken"`
	}
	if httpapi.Decode(w, r, &input, 4096) != nil || len(input.RefreshToken) != 43 {
		httpapi.Error(w, 400, "invalid_request", "刷新请求格式不正确")
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
	old, err := q.GetRefreshToken(r.Context(), tokenHash(input.RefreshToken))
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		invalidToken(w)
		return
	}
	if err != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	now := s.now().UnixMilli()
	if old.UsedAt.Valid {
		if _, err := q.RevokeDeviceSession(r.Context(), dbsqlc.RevokeDeviceSessionParams{ID: old.SessionID, RevokedAt: sql.NullInt64{Int64: now, Valid: true}}); err != nil {
			_ = tx.Rollback()
			tokenFailed(w)
			return
		}
		if tx.Commit() != nil {
			tokenFailed(w)
			return
		}
		invalidToken(w)
		return
	}
	session, err := q.GetDeviceSession(r.Context(), old.SessionID)
	if err != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	credential, err := q.GetAuthUser(r.Context(), session.UserID)
	if err != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	if session.RevokedAt.Valid || session.ExpiresAt <= now || session.CredentialVersion != credential.CredentialVersion || credential.Status != "active" {
		_ = tx.Rollback()
		invalidToken(w)
		return
	}
	count, err := q.ConsumeRefreshToken(r.Context(), dbsqlc.ConsumeRefreshTokenParams{TokenHash: old.TokenHash, UsedAt: sql.NullInt64{Int64: now, Valid: true}})
	if err != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	if count != 1 {
		_ = tx.Rollback()
		invalidToken(w)
		return
	}
	if q.TouchDeviceSession(r.Context(), dbsqlc.TouchDeviceSessionParams{ID: session.ID, LastSeenAt: now}) != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	result, err := s.issueTokens(r.Context(), q, session, credential.MustChangePassword != 0)
	if err != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	if tx.Commit() != nil {
		tokenFailed(w)
		return
	}
	httpapi.Write(w, 200, result)
}

func (s *Service) RevokeTokenHandler(w http.ResponseWriter, r *http.Request) {
	if DeviceSessionID(r) == "" {
		httpapi.Error(w, 400, "bearer_required", "请使用设备登录凭据")
		return
	}
	device, err := s.queries.GetDeviceSession(r.Context(), DeviceSessionID(r))
	if err != nil || contracts.UserID(r.Context()) == "" || device.UserID != contracts.UserID(r.Context()) {
		invalidToken(w)
		return
	}
	if _, err := s.queries.RevokeDeviceSession(r.Context(), dbsqlc.RevokeDeviceSessionParams{ID: DeviceSessionID(r), RevokedAt: sql.NullInt64{Int64: s.now().UnixMilli(), Valid: true}}); err != nil {
		tokenFailed(w)
		return
	}
	httpapi.Write(w, 200, map[string]bool{"revoked": true})
}

type sessionView struct {
	ID         string    `json:"id"`
	DeviceName string    `json:"deviceName"`
	Platform   string    `json:"platform"`
	CreatedAt  time.Time `json:"createdAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Current    bool      `json:"current"`
}

func (s *Service) SessionsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	userID, err := contracts.RequireUserID(r.Context())
	if err != nil {
		invalidToken(w)
		return
	}
	devices, err := s.queries.ListDeviceSessions(r.Context(), dbsqlc.ListDeviceSessionsParams{ExpiresAt: s.now().UnixMilli(), UserID: userID})
	if err != nil {
		tokenFailed(w)
		return
	}
	items := make([]sessionView, 0, len(devices))
	for _, d := range devices {
		items = append(items, sessionView{ID: d.ID, DeviceName: d.DeviceName, Platform: d.Platform, CreatedAt: time.UnixMilli(d.CreatedAt).UTC(), LastSeenAt: time.UnixMilli(d.LastSeenAt).UTC(), ExpiresAt: time.UnixMilli(d.ExpiresAt).UTC(), Current: d.ID == DeviceSessionID(r)})
	}
	browsers, err := s.queries.ListBrowserSessions(r.Context(), dbsqlc.ListBrowserSessionsParams{Now: s.now().UnixMilli(), UserID: userID})
	if err != nil {
		tokenFailed(w)
		return
	}
	var currentHash string
	if cookie, err := r.Cookie(s.sessions.Cookie.Name); err == nil && DeviceSessionID(r) == "" {
		currentHash = tokenHash(cookie.Value)
	}
	for _, b := range browsers {
		items = append(items, sessionView{ID: b.ID, DeviceName: "浏览器", Platform: "web", CreatedAt: time.UnixMilli(b.CreatedAt).UTC(), LastSeenAt: time.UnixMilli(b.LastSeenAt).UTC(), ExpiresAt: time.UnixMilli(b.Expiry).UTC(), Current: b.TokenHash == currentHash})
	}
	httpapi.Write(w, 200, map[string]any{"items": items})
}

func (s *Service) DeleteSessionHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID, err := contracts.RequireUserID(r.Context())
	if err != nil {
		invalidToken(w)
		return
	}
	device, err := s.queries.GetDeviceSession(r.Context(), id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		tokenFailed(w)
		return
	}
	if err == nil {
		if device.UserID != userID {
			httpapi.Error(w, 404, "session_not_found", "登录设备不存在")
			return
		}
		if _, err := s.queries.RevokeDeviceSession(r.Context(), dbsqlc.RevokeDeviceSessionParams{ID: id, RevokedAt: sql.NullInt64{Int64: s.now().UnixMilli(), Valid: true}}); err != nil {
			tokenFailed(w)
			return
		}
	} else {
		browser, err := s.queries.GetBrowserSession(r.Context(), id)
		if errors.Is(err, sql.ErrNoRows) {
			httpapi.Error(w, 404, "session_not_found", "登录设备不存在")
			return
		}
		if err != nil {
			tokenFailed(w)
			return
		}
		if browser.UserID != userID {
			httpapi.Error(w, 404, "session_not_found", "登录设备不存在")
			return
		}
		if s.queries.DeleteSession(r.Context(), browser.TokenHash) != nil {
			tokenFailed(w)
			return
		}
		if s.queries.RevokeBrowserSession(r.Context(), dbsqlc.RevokeBrowserSessionParams{TokenHash: browser.TokenHash, RevokedAt: sql.NullInt64{Int64: s.now().UnixMilli(), Valid: true}}) != nil {
			tokenFailed(w)
			return
		}
		if cookie, err := r.Cookie(s.sessions.Cookie.Name); err == nil && tokenHash(cookie.Value) == browser.TokenHash {
			if s.sessions.Destroy(r.Context()) != nil {
				tokenFailed(w)
				return
			}
		}
	}
	httpapi.Write(w, 200, map[string]bool{"revoked": true})
}

func (s *Service) LogoutAllHandler(w http.ResponseWriter, r *http.Request) {
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		tokenFailed(w)
		return
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	userID, err := contracts.RequireUserID(r.Context())
	if err != nil {
		_ = tx.Rollback()
		invalidToken(w)
		return
	}
	if s.revokeUser(r.Context(), q, userID) != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	if tx.Commit() != nil {
		tokenFailed(w)
		return
	}
	if DeviceSessionID(r) == "" {
		if s.sessions.Destroy(r.Context()) != nil {
			tokenFailed(w)
			return
		}
	}
	httpapi.Write(w, 200, map[string]bool{"reauthenticate": true})
}

func (s *Service) RotateSigningKeyHandler(w http.ResponseWriter, r *http.Request) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		tokenFailed(w)
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		tokenFailed(w)
		return
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	now := s.now().UnixMilli()
	if q.RetireSigningKeys(r.Context(), sql.NullInt64{Int64: now, Valid: true}) != nil || q.InsertSigningKey(r.Context(), dbsqlc.InsertSigningKeyParams{ID: uuid.NewString(), PrivateKey: key, CreatedAt: now}) != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	if tx.Commit() != nil {
		tokenFailed(w)
		return
	}
	httpapi.Write(w, 200, map[string]bool{"rotated": true})
}
