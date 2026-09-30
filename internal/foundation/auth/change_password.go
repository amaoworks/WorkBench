package auth

import (
	"net/http"
	"time"

	"workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/httpapi"
)

func (s *Service) ChangePasswordHandler(w http.ResponseWriter, r *http.Request) {
	if s.mode != ModePassword {
		httpapi.Error(w, http.StatusBadRequest, "password_mode_required", "当前为本机免登录模式，请通过启动参数启用密码模式")
		return
	}
	var input struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := httpapi.Decode(w, r, &input, 4096); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "密码请求格式不正确")
		return
	}
	credential, err := s.queries.GetAuthCredential(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "password_failed", "无法读取密码")
		return
	}
	previous := credential.PasswordHash
	valid, err := verifyPassword(input.CurrentPassword, previous)
	if err != nil || !valid {
		httpapi.Error(w, http.StatusForbidden, "invalid_password", "当前密码不正确")
		return
	}
	if credential.MustChangePassword != 0 && input.NewPassword == input.CurrentPassword {
		httpapi.Error(w, http.StatusBadRequest, "password_unchanged", "新密码不能与初始密码相同")
		return
	}
	hash, err := hashPassword(input.NewPassword)
	if err != nil {
		httpapi.Error(w, 400, "invalid_password", "新密码至少 8 位，且大写字母、小写字母、数字、特殊符号至少包含三类")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpapi.Error(w, 500, "password_failed", "无法更新密码")
		return
	}
	defer tx.Rollback()
	queries := s.queries.WithTx(tx)
	count, err := queries.UpdatePasswordHashIfCurrent(r.Context(), dbsqlc.UpdatePasswordHashIfCurrentParams{PasswordHash: hash, UpdatedAt: time.Now().UTC().UnixMilli(), PreviousHash: previous})
	if err != nil {
		httpapi.Error(w, 500, "password_failed", "无法更新密码")
		return
	}
	if err != nil || count != 1 {
		httpapi.Error(w, 409, "password_changed", "密码已被修改，请重新登录")
		return
	}
	if err := queries.DeleteAllSessions(r.Context()); err != nil {
		httpapi.Error(w, 500, "password_failed", "无法注销登录")
		return
	}
	if err := tx.Commit(); err != nil {
		httpapi.Error(w, 500, "password_failed", "无法保存密码")
		return
	}
	if err := s.sessions.Destroy(r.Context()); err != nil {
		httpapi.Error(w, 500, "session_failed", "密码已更新，请重新登录")
		return
	}
	httpapi.Write(w, 200, map[string]bool{"reauthenticate": true})
}
