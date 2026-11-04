package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/httpapi"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,63}$`)

type userView struct {
	ID                 string    `json:"id"`
	Username           string    `json:"username"`
	DisplayName        string    `json:"displayName"`
	Role               string    `json:"role"`
	Status             string    `json:"status"`
	MustChangePassword bool      `json:"mustChangePassword"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

func viewUser(u dbsqlc.AuthUser) userView {
	return userView{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, Role: u.Role, Status: u.Status, MustChangePassword: u.MustChangePassword != 0, CreatedAt: time.UnixMilli(u.CreatedAt).UTC(), UpdatedAt: time.UnixMilli(u.UpdatedAt).UTC()}
}

func (s *Service) SetUserDisabledHandler(handler func(string)) {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	s.userDisabled = handler
}

func (s *Service) ActiveUserIDs(ctx context.Context) ([]string, error) {
	return s.queries.ActiveAuthUserIDs(ctx)
}
func (s *Service) IsUserActive(ctx context.Context, id string) (bool, error) {
	u, err := s.queries.GetAuthUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && u.Status == "active", err
}

// Local mode always represents the migrated owner and cannot expose a multi-user workspace.
func (s *Service) localUser(ctx context.Context) (dbsqlc.AuthUser, error) {
	users, err := s.queries.ListAuthUsers(ctx)
	if err != nil {
		return dbsqlc.AuthUser{}, err
	}
	if len(users) != 1 || users[0].ID != contracts.LegacyUserID {
		return dbsqlc.AuthUser{}, errInvalidToken
	}
	u := users[0]
	if u.Role != "admin" || u.Status != "active" {
		return dbsqlc.AuthUser{}, errInvalidToken
	}
	return u, nil
}

func (s *Service) requestUser(r *http.Request) (dbsqlc.AuthUser, error) {
	if s.mode == ModeLocal {
		return s.localUser(r.Context())
	}
	var id string
	var version int64
	if c, ok := r.Context().Value(deviceContextKey{}).(*accessClaims); ok {
		if err := s.checkDevice(r.Context(), c); err != nil {
			return dbsqlc.AuthUser{}, err
		}
		id, version = c.Subject, c.Version
	} else {
		if !s.sessions.GetBool(r.Context(), "authenticated") || !s.browserDeviceActive(r) {
			return dbsqlc.AuthUser{}, errInvalidToken
		}
		id, version = s.sessions.GetString(r.Context(), "userID"), s.sessions.GetInt64(r.Context(), "credentialVersion")
	}
	u, err := s.queries.GetAuthUser(r.Context(), id)
	if err != nil || u.Status != "active" || u.CredentialVersion != version {
		return dbsqlc.AuthUser{}, errInvalidToken
	}
	return u, nil
}

func (s *Service) principalRequest(r *http.Request, u dbsqlc.AuthUser) *http.Request {
	sessionID := DeviceSessionID(r)
	if sessionID == "" {
		if cookie, err := r.Cookie("workbench_session"); err == nil {
			sessionID = tokenHash(cookie.Value)
		}
	}
	if s.mode == ModeLocal {
		sessionID = "local"
	}
	return r.WithContext(contracts.WithPrincipal(r.Context(), contracts.Principal{UserID: u.ID, Role: contracts.Role(u.Role), SessionID: sessionID}))
}

func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return s.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !contracts.IsAdmin(r.Context()) {
			httpapi.Error(w, 403, "admin_required", "需要管理员权限")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (s *Service) ListUsersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.adminRequest(w, r) {
		return
	}
	users, err := s.queries.ListAuthUsers(r.Context())
	if err != nil {
		tokenFailed(w)
		return
	}
	items := make([]userView, 0, len(users))
	for _, u := range users {
		items = append(items, viewUser(u))
	}
	httpapi.Write(w, 200, map[string]any{"items": items})
}

// Recheck the database in handlers as well so a role changed after middleware cannot authorize management.
func (s *Service) adminRequest(w http.ResponseWriter, r *http.Request) bool {
	id := contracts.UserID(r.Context())
	u, err := s.queries.GetAuthUser(r.Context(), id)
	if !contracts.IsAdmin(r.Context()) || err != nil || u.Status != "active" || u.Role != "admin" || (s.mode == ModePassword && u.MustChangePassword != 0) {
		httpapi.Error(w, 403, "admin_required", "需要管理员权限")
		return false
	}
	return true
}

func (s *Service) CreateUserHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.adminRequest(w, r) {
		return
	}
	if s.mode == ModeLocal {
		httpapi.Error(w, 400, "password_mode_required", "多用户需要启用密码登录")
		return
	}
	var input struct {
		Username    string `json:"username"`
		DisplayName string `json:"displayName"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if httpapi.Decode(w, r, &input, 4096) != nil {
		httpapi.Error(w, 400, "invalid_request", "用户请求格式不正确")
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.Role == "" {
		input.Role = "user"
	}
	if input.DisplayName == "" {
		input.DisplayName = input.Username
	}
	if !usernamePattern.MatchString(input.Username) || utf8.RuneCountInString(input.DisplayName) > 100 || (input.Role != "admin" && input.Role != "user") {
		httpapi.Error(w, 400, "invalid_user", "用户名、名称或角色不正确")
		return
	}
	password := input.Password
	var err error
	if password == "" {
		password, err = generateInitialPassword()
		if err != nil {
			tokenFailed(w)
			return
		}
	}
	if retry, err := s.passwordLimit.acquire(false); err != nil {
		writePasswordLimit(w, retry, err)
		return
	}
	defer s.passwordLimit.release()
	hash, err := hashPassword(password)
	if err != nil {
		httpapi.Error(w, 400, "invalid_password", "密码至少 8 位，且包含大写字母、小写字母、数字、特殊符号中的三类")
		return
	}
	now := s.now().UnixMilli()
	u := dbsqlc.AuthUser{ID: uuid.NewString(), Username: input.Username, DisplayName: input.DisplayName, PasswordHash: hash, Role: input.Role, Status: "active", CredentialVersion: now, MustChangePassword: 1, CreatedAt: now, UpdatedAt: now}
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if !s.adminRequest(w, r) {
		return
	}
	if _, err := s.queries.GetAuthUserByUsername(r.Context(), u.Username); err == nil {
		httpapi.Error(w, 409, "username_exists", "用户名已存在")
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		tokenFailed(w)
		return
	}
	err = s.queries.InsertAuthUser(r.Context(), dbsqlc.InsertAuthUserParams{ID: u.ID, Username: u.Username, DisplayName: u.DisplayName, PasswordHash: u.PasswordHash, Role: u.Role, Status: u.Status, CredentialVersion: u.CredentialVersion, MustChangePassword: u.MustChangePassword, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		tokenFailed(w)
		return
	}
	httpapi.Write(w, 201, map[string]any{"user": viewUser(u), "initialPassword": password})
}

func (s *Service) UpdateUserHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.adminRequest(w, r) {
		return
	}
	var input struct {
		DisplayName *string `json:"displayName"`
		Role        *string `json:"role"`
		Status      *string `json:"status"`
	}
	if httpapi.Decode(w, r, &input, 4096) != nil {
		httpapi.Error(w, 400, "invalid_request", "用户请求格式不正确")
		return
	}
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if !s.adminRequest(w, r) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		tokenFailed(w)
		return
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	u, err := q.GetAuthUser(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		httpapi.Error(w, 404, "user_not_found", "用户不存在")
		return
	}
	if err != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	old := u
	if input.DisplayName != nil {
		u.DisplayName = strings.TrimSpace(*input.DisplayName)
	}
	if input.Role != nil {
		u.Role = *input.Role
	}
	if input.Status != nil {
		u.Status = *input.Status
	}
	if u.DisplayName == "" || utf8.RuneCountInString(u.DisplayName) > 100 || (u.Role != "admin" && u.Role != "user") || (u.Status != "active" && u.Status != "disabled") {
		_ = tx.Rollback()
		httpapi.Error(w, 400, "invalid_user", "名称、角色或状态不正确")
		return
	}
	if old.Role == "admin" && old.Status == "active" && (u.Role != "admin" || u.Status != "active") {
		count, err := q.CountActiveAdmins(r.Context())
		if err != nil {
			_ = tx.Rollback()
			tokenFailed(w)
			return
		}
		if count <= 1 {
			_ = tx.Rollback()
			httpapi.Error(w, 409, "last_admin", "不能禁用或降级最后一个管理员")
			return
		}
	}
	u.UpdatedAt = s.now().UnixMilli()
	if q.UpdateAuthUser(r.Context(), dbsqlc.UpdateAuthUserParams{ID: u.ID, DisplayName: u.DisplayName, Role: u.Role, Status: u.Status, UpdatedAt: u.UpdatedAt}) != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	if u.Status == "disabled" {
		if s.revokeUser(r.Context(), q, u.ID) != nil {
			_ = tx.Rollback()
			tokenFailed(w)
			return
		}
	}
	if tx.Commit() != nil {
		tokenFailed(w)
		return
	}
	if u.Status == "disabled" && s.userDisabled != nil {
		s.userDisabled(u.ID)
	}
	httpapi.Write(w, 200, map[string]any{"user": viewUser(u)})
}

func (s *Service) ResetUserPasswordHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.adminRequest(w, r) {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if httpapi.Decode(w, r, &input, 4096) != nil {
		httpapi.Error(w, 400, "invalid_request", "密码请求格式不正确")
		return
	}
	password := input.Password
	var err error
	if password == "" {
		password, err = generateInitialPassword()
		if err != nil {
			tokenFailed(w)
			return
		}
	}
	if retry, err := s.passwordLimit.acquire(false); err != nil {
		writePasswordLimit(w, retry, err)
		return
	}
	defer s.passwordLimit.release()
	hash, err := hashPassword(password)
	if err != nil {
		httpapi.Error(w, 400, "invalid_password", "密码至少 8 位，且包含大写字母、小写字母、数字、特殊符号中的三类")
		return
	}
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	if !s.adminRequest(w, r) {
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		tokenFailed(w)
		return
	}
	defer tx.Rollback()
	q := s.queries.WithTx(tx)
	count, err := q.ResetAuthUserPassword(r.Context(), dbsqlc.ResetAuthUserPasswordParams{ID: r.PathValue("id"), PasswordHash: hash, Now: s.now().UnixMilli()})
	if err != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	if count != 1 {
		_ = tx.Rollback()
		httpapi.Error(w, 404, "user_not_found", "用户不存在")
		return
	}
	if s.revokeUser(r.Context(), q, r.PathValue("id")) != nil {
		_ = tx.Rollback()
		tokenFailed(w)
		return
	}
	if tx.Commit() != nil {
		tokenFailed(w)
		return
	}
	httpapi.Write(w, 200, map[string]any{"initialPassword": password})
}

func (s *Service) revokeUser(ctx context.Context, q *dbsqlc.Queries, id string) error {
	if err := q.BumpAuthUserVersion(ctx, id); err != nil {
		return err
	}
	if err := q.DeleteAllSessions(ctx, id); err != nil {
		return err
	}
	now := sql.NullInt64{Int64: s.now().UnixMilli(), Valid: true}
	if err := q.RevokeAllBrowserSessions(ctx, dbsqlc.RevokeAllBrowserSessionsParams{UserID: id, RevokedAt: now}); err != nil {
		return err
	}
	return q.RevokeAllDeviceSessions(ctx, dbsqlc.RevokeAllDeviceSessionsParams{UserID: id, RevokedAt: now})
}
