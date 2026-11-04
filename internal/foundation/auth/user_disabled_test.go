package auth

import (
	"context"
	"testing"
)

func TestDisabledUserHookRunsAfterCommit(t *testing.T) {
	s, handler := tokenService(t, false)
	admin := nativeLogin(t, handler)
	user := createTestUser(t, handler, admin, "resource-owner", "user")
	var revoked string
	s.SetUserDisabledHandler(func(id string) {
		revoked = id
		active, err := s.IsUserActive(context.Background(), id)
		if err != nil || active {
			t.Errorf("resource cleanup observed uncommitted disabled state: active=%v error=%v", active, err)
		}
	})
	result := nativeRequest(handler, "PATCH", "/api/admin/users/"+user.ID, map[string]string{"status": "disabled"}, admin.AccessToken)
	if result.Code != 200 || revoked != user.ID {
		t.Fatalf("disabled cleanup hook: status=%d revoked=%q body=%s", result.Code, revoked, result.Body.String())
	}
	revoked = ""
	result = nativeRequest(handler, "PATCH", "/api/admin/users/"+user.ID, map[string]string{"status": "active"}, admin.AccessToken)
	if result.Code != 200 || revoked != "" {
		t.Fatalf("enable unexpectedly revoked resources: status=%d revoked=%q", result.Code, revoked)
	}
}
