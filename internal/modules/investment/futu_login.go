package investment

import (
	"context"
	"crypto/md5" // OpenD's login protocol requires an MD5 password digest.
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// RestoreFutuLogin republishes persisted settings after migrations or a backup restore.
func (m *Module) RestoreFutuLogin(ctx context.Context) error {
	if m.deps.FutuConfigDir == "" {
		return nil
	}
	m.futu.connectMu.Lock()
	defer m.futu.connectMu.Unlock()
	rec, err := m.loadFutu(ctx)
	if err != nil {
		return err
	}
	return m.publishFutuLogin(rec)
}

func (m *Module) applyFutuLogin(rec *futuRecord, input futuSettingsInput) error {
	account := strings.TrimSpace(input.Account)
	if len(account) > 256 || strings.IndexFunc(account, unicode.IsControl) >= 0 || len(input.Password) > 1024 {
		return errors.New("富途牛牛账号或密码格式无效")
	}
	if input.ClearPassword {
		if input.Password != "" {
			return errors.New("清除密码时不能同时填写新密码")
		}
		rec.Account, rec.PasswordMD5 = account, ""
		rec.Enabled, rec.OvernightEnabled = false, false
		return nil
	}
	if account != rec.Account && rec.PasswordMD5 != "" && input.Password == "" {
		return errors.New("更换富途牛牛账号时请重新填写密码")
	}
	rec.Account = account
	if input.Password != "" {
		digest := md5.Sum([]byte(input.Password))
		rec.PasswordMD5 = hex.EncodeToString(digest[:])
	}
	if (rec.Enabled && m.futuManaged() || rec.PasswordMD5 != "") && (rec.Account == "" || rec.PasswordMD5 == "") {
		return errors.New("请填写富途牛牛账号和登录密码")
	}
	if rec.Enabled && rec.PasswordMD5 != "" && !m.futuManaged() {
		return errors.New("当前部署尚未接入富途牛牛登录服务（OpenD），保存账号密码不会自动启动该服务")
	}
	return nil
}

func (m *Module) futuManaged() bool {
	return m.deps.FutuConfigDir != "" || m.opend != nil
}

// Called while connectMu is held, after settings have committed successfully.
func (m *Module) syncOpenD(rec futuRecord) {
	if m.opend != nil {
		rec.Enabled = rec.Enabled && m.moduleEnabled
		m.opend.apply(rec)
	}
}

// Only the companion OpenD process reads this private file. Password digests are
// login credentials, not safe-to-share hashes; never return them in API views.
func (m *Module) publishFutuLogin(rec futuRecord) error {
	rec.Enabled = rec.Enabled && m.moduleEnabled
	if m.deps.FutuConfigDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.deps.FutuConfigDir, 0750); err != nil {
		return err
	}
	data, err := json.Marshal(struct {
		Enabled     bool   `json:"enabled"`
		Account     string `json:"account"`
		PasswordMD5 string `json:"passwordMD5"`
	}{rec.Enabled, rec.Account, rec.PasswordMD5})
	if err != nil {
		return err
	}
	name := filepath.Join(m.deps.FutuConfigDir, "login.json")
	if current, err := os.ReadFile(name); err == nil && string(current) == string(data) {
		return nil
	}
	file, err := os.CreateTemp(m.deps.FutuConfigDir, ".login-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(0640); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), name)
}
