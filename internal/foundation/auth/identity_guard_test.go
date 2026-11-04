package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workbench/internal/contracts"
)

func TestPinnedClientIdentityCannotFollowAnotherAccount(t *testing.T) {
	_, handler := tokenService(t, false)
	admin := nativeLogin(t, handler)
	for _, tc := range []struct {
		path, expectedUser string
		want               int
	}{
		{"/protected", "", 204},
		{"/protected", contracts.LegacyUserID, 204},
		{"/protected", "another-user", 409},
		{"/protected?workbenchUser=another-user", "", 409},
		{"/protected?workbenchUser=owner", "owner", 204},
		{"/protected?workbenchUser=another-user", "owner", 409},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080"+tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+admin.AccessToken)
		if tc.expectedUser != "" {
			req.Header.Set("X-Workbench-User", tc.expectedUser)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != tc.want || (tc.want == 409 && !strings.Contains(response.Body.String(), "account_changed")) {
			t.Errorf("pinned identity (%s %s): %d %s", tc.path, tc.expectedUser, response.Code, response.Body.String())
		}
	}
}
