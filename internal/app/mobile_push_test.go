package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"workbench/internal/foundation/auth"
)

func writeTestAPNsKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "AuthKey_TEST.p8")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMobilePushConfig(t *testing.T) {
	disabled, err := loadMobilePushConfig(MobilePushDeployment{APNsP8File: "/does/not/exist"})
	if err != nil || disabled.Enabled || disabled.APNs != nil || disabled.FCM != nil {
		t.Fatalf("disabled config = %+v, err = %v", disabled, err)
	}
	if _, err := loadMobilePushConfig(MobilePushDeployment{Enabled: true, APNsP8File: writeTestAPNsKey(t)}); err == nil {
		t.Fatal("partial APNs configuration was accepted")
	}
	accountPath := filepath.Join(t.TempDir(), "service-account.json")
	if err := os.WriteFile(accountPath, []byte(`{"type":"service_account","project_id":"project","client_email":"push@example.test","private_key":"PEM"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadMobilePushConfig(MobilePushDeployment{Enabled: true, FCMServiceAccountFile: accountPath})
	if err != nil || loaded.FCM == nil || loaded.FCM.ProjectID != "project" || loaded.FCM.ClientEmail != "push@example.test" {
		t.Fatalf("FCM config = %+v, err = %v", loaded.FCM, err)
	}
}

func TestMobilePushRoutesRequireEnabledDeviceSession(t *testing.T) {
	disabled := newTestApp(t)
	csrfToken, cookies := csrf(t, disabled.Handler())
	response := request(t, disabled.Handler(), http.MethodPost, "/api/mobile/push-token", []byte(`{"platform":"ios","pushToken":"device-token"}`), csrfToken, cookies)
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled status = %d, body = %s", response.Code, response.Body.String())
	}

	enabled, err := New(context.Background(), Config{
		ListenAddress: "127.0.0.1:8080", DataPath: filepath.Join(t.TempDir(), "data.db"),
		AuthMode: auth.ModePassword, Password: "Push-Test-123!", ShutdownGrace: time.Second,
		MobilePush: MobilePushDeployment{
			Enabled: true, APNsP8File: writeTestAPNsKey(t), APNsTeamID: "TEAM", APNsKeyID: "KEY", APNsBundleID: "com.example.app",
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = enabled.Close() })
	if _, err := enabled.DB().Exec(`UPDATE auth_credentials SET must_change_password = 0`); err != nil {
		t.Fatal(err)
	}
	csrfToken, cookies = csrf(t, enabled.Handler())
	login := request(t, enabled.Handler(), http.MethodPost, "/api/auth/login", []byte(`{"password":"Push-Test-123!"}`), csrfToken, cookies)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", login.Code, login.Body.String())
	}
	cookies = append(cookies, login.Result().Cookies()...)
	response = request(t, enabled.Handler(), http.MethodPost, "/api/mobile/push-token", []byte(`{"platform":"ios","pushToken":"device-token"}`), csrfToken, cookies)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("browser session status = %d, body = %s", response.Code, response.Body.String())
	}
	var count int
	if err := enabled.DB().QueryRow(`SELECT COUNT(*) FROM mobile_push_tokens`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("registered token count = %d, err = %v", count, err)
	}
}
