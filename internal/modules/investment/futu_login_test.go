package investment

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFutuLoginCredentialsPublishRetainAndClear(t *testing.T) {
	m := openInvestmentModule(t)
	m.deps.FutuConfigDir = t.TempDir()
	input := futuSettingsInput{Account: "test@example.invalid", Password: " fixture password "}
	digest := md5.Sum([]byte(input.Password))
	wantDigest := hex.EncodeToString(digest[:])
	save := func(want int) string {
		t.Helper()
		body, _ := json.Marshal(input)
		got := httptest.NewRecorder()
		m.saveFutu(got, httptest.NewRequest("PUT", "/", strings.NewReader(string(body))))
		if got.Code != want {
			t.Fatalf("save: %d %s", got.Code, got.Body.String())
		}
		if strings.Contains(got.Body.String(), wantDigest) || strings.Contains(got.Body.String(), "fixture password") {
			t.Fatal("credentials leaked")
		}
		return got.Body.String()
	}
	if body := save(200); !strings.Contains(body, `"hasPassword":true`) {
		t.Fatal(body)
	}
	path := filepath.Join(m.deps.FutuConfigDir, "login.json")
	raw, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(raw), wantDigest) || strings.Contains(string(raw), input.Password) {
		t.Fatalf("login configuration invalid: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0640 {
		t.Fatal("login file has unexpected permissions")
	}
	input.Password = ""
	save(200)
	rec, _ := m.loadFutu(context.Background())
	if rec.PasswordMD5 != wantDigest {
		t.Fatal("blank password erased login")
	}
	input.Account = "changed@example.invalid"
	save(400)
	input.Account = rec.Account
	input.ClearPassword = true
	save(200)
	rec, _ = m.loadFutu(context.Background())
	if rec.PasswordMD5 != "" || rec.Enabled || rec.OvernightEnabled {
		t.Fatal("clear did not disable and erase")
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), wantDigest) {
		t.Fatal("published password survived clear")
	}
}

func TestFutuLoginCanSaveBeforeOpenDIsConfigured(t *testing.T) {
	m := openInvestmentModule(t)
	input := `{"enabled":false,"account":"test","password":"test password"}`
	got := httptest.NewRecorder()
	m.saveFutu(got, httptest.NewRequest("PUT", "/", strings.NewReader(input)))
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"hasPassword":true`) || !strings.Contains(got.Body.String(), `"managed":false`) {
		t.Fatal(got.Body.String())
	}
	rec, _ := m.loadFutu(context.Background())
	if rec.Account != "test" || rec.PasswordMD5 == "" || rec.Enabled {
		t.Fatal("account not saved while OpenD unavailable")
	}
	enable := httptest.NewRecorder()
	m.saveFutu(enable, httptest.NewRequest("PUT", "/", strings.NewReader(`{"enabled":true,"account":"test"}`)))
	if enable.Code != 400 {
		t.Fatal("enabled credentials without an OpenD login integration")
	}
	unchanged, _ := m.loadFutu(context.Background())
	if unchanged.Enabled || unchanged.PasswordMD5 != rec.PasswordMD5 {
		t.Fatal("rejected enable changed saved credentials")
	}
	m.deps.FutuConfigDir = t.TempDir()
	if err := m.RestoreFutuLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(m.deps.FutuConfigDir, "login.json"))
	if err != nil || !strings.Contains(string(raw), rec.PasswordMD5) {
		t.Fatal("saved credentials were not published after integration", err)
	}
}

func TestFutuLoginPublishFailureDoesNotChangeSettings(t *testing.T) {
	m := openInvestmentModule(t)
	input := `{"enabled":false,"account":"test","password":"test password"}`
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	m.deps.FutuConfigDir = path
	got := httptest.NewRecorder()
	m.saveFutu(got, httptest.NewRequest("PUT", "/", strings.NewReader(input)))
	if got.Code != 500 {
		t.Fatal("publish failure was hidden")
	}
	rec, _ := m.loadFutu(context.Background())
	if rec.Account != "" || rec.PasswordMD5 != "" {
		t.Fatal("failed save changed settings")
	}
}
