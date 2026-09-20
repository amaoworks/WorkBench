package investment

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"workbench/internal/foundation/httpapi"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

const maxFutuSettingsBytes = 16 << 10

type futuRecord struct {
	Host             string
	Port             int
	Enabled          bool
	OvernightEnabled bool
	Account          string
	PasswordMD5      string
	AllowNonLocal    bool
	LastError        string
	UpdatedAt        time.Time
}

type futuSettingsView struct {
	Enabled          bool   `json:"enabled"`
	OvernightEnabled bool   `json:"overnightEnabled"`
	Account          string `json:"account"`
	HasPassword      bool   `json:"hasPassword"`
	Managed          bool   `json:"managed"`
	ServiceState     string `json:"serviceState"`
	ServiceError     string `json:"serviceError"`
	Connected        bool   `json:"connected"`
	QotLogined       bool   `json:"qotLogined"`
	LastError        string `json:"lastError"`
	SubUsed          int    `json:"subUsed,omitempty"`
	SubRemain        int    `json:"subRemain,omitempty"`
	HistoryRemain    int    `json:"historyRemain,omitempty"`
}

type futuSettingsInput struct {
	Enabled       bool   `json:"enabled"`
	Account       string `json:"account"`
	Password      string `json:"password"`
	ClearPassword bool   `json:"clearPassword"`
}

func (m *Module) futuConnection() (string, int, error) {
	address := m.deps.FutuOpenDAddress
	if address == "" {
		address = "127.0.0.1:11111"
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, errors.New("富途牛牛 OpenD 地址必须为主机:端口")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, errors.New("富途牛牛 OpenD 端口必须为数字")
	}
	if err := validateOpenDAddr(host, port, m.deps.FutuAllowNonLocal); err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func (m *Module) loadFutu(ctx context.Context) (futuRecord, error) {
	row, err := m.queries.GetFutu(ctx)
	if err != nil {
		return futuRecord{}, err
	}
	host, port, err := m.futuConnection()
	if err != nil {
		return futuRecord{}, err
	}
	return futuRecord{
		Host: host, Port: port, Enabled: row.Enabled != 0,
		OvernightEnabled: row.OvernightEnabled != 0,
		Account:          row.Account, PasswordMD5: row.PasswordMd5,
		AllowNonLocal: m.deps.FutuAllowNonLocal, LastError: row.LastError, UpdatedAt: unixMilli(row.UpdatedAt),
	}, nil
}

func (m *Module) storeFutu(ctx context.Context, rec futuRecord) error {
	enabled, allow, overnight := int64(0), int64(0), int64(0)
	if rec.Enabled {
		enabled = 1
	}
	if rec.AllowNonLocal {
		allow = 1
	}
	if rec.Enabled && rec.OvernightEnabled {
		overnight = 1
	}
	return m.queries.UpsertFutu(ctx, investmentsqlc.UpsertFutuParams{
		Host: rec.Host, Port: int64(rec.Port), Enabled: enabled, AllowNonLocal: allow,
		OvernightEnabled: overnight,
		Account:          rec.Account, PasswordMd5: rec.PasswordMD5,
		LastError: rec.LastError, UpdatedAt: m.now().UTC().UnixMilli(),
	})
}

func (m *Module) getFutu(w http.ResponseWriter, r *http.Request) {
	rec, err := m.loadFutu(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法读取富途牛牛配置")
		return
	}
	view := futuSettingsView{
		Enabled: rec.Enabled, LastError: rec.LastError,
		OvernightEnabled: rec.Enabled && rec.OvernightEnabled,
		Account:          rec.Account, HasPassword: rec.PasswordMD5 != "", Managed: m.futuManaged(),
		ServiceState: "external",
	}
	if m.opend != nil {
		view.ServiceState, view.ServiceError = m.opend.status()
	}
	if rec.Enabled && (m.opend == nil || view.ServiceState == "running") {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if client, err := m.futu.ensure(ctx); err == nil {
			if loggedIn, err := client.globalState(ctx); err == nil {
				view.Connected, view.QotLogined = true, loggedIn
				view.LastError = ""
				view.SubUsed, view.SubRemain, _ = client.subInfo(ctx)
				view.HistoryRemain, _, _ = client.historyQuota(ctx)
			} else {
				view.LastError = err.Error()
			}
		} else {
			view.LastError = err.Error()
		}
	}
	if view.ServiceError != "" {
		view.LastError = view.ServiceError
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, view)
}

func (m *Module) saveFutu(w http.ResponseWriter, r *http.Request) {
	var input futuSettingsInput
	if httpapi.Decode(w, r, &input, maxFutuSettingsBytes) != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid_request", "无效的富途牛牛配置")
		return
	}
	m.futu.connectMu.Lock()
	rec, err := m.loadFutu(r.Context())
	if err != nil {
		m.futu.connectMu.Unlock()
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法读取富途牛牛配置")
		return
	}
	old := rec
	rec.Enabled = input.Enabled
	rec.OvernightEnabled = rec.Enabled && rec.OvernightEnabled
	rec.LastError = ""
	if err := m.applyFutuLogin(&rec, input); err != nil {
		m.futu.connectMu.Unlock()
		httpapi.Error(w, 400, "invalid_request", err.Error())
		return
	}
	if err := m.publishFutuLogin(rec); err != nil {
		m.futu.connectMu.Unlock()
		httpapi.Error(w, 500, "futu_login_failed", "无法更新 OpenD 登录配置，请检查共享目录权限")
		return
	}
	if err := m.storeFutu(r.Context(), rec); err != nil {
		_ = m.publishFutuLogin(old)
		m.futu.connectMu.Unlock()
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法保存富途牛牛配置")
		return
	}
	m.futu.reset()
	m.syncOpenD(rec)
	m.futu.connectMu.Unlock()
	m.getFutu(w, r)
}

func (m *Module) disconnectFutu(w http.ResponseWriter, r *http.Request) {
	m.futu.connectMu.Lock()
	defer m.futu.connectMu.Unlock()
	rec, err := m.loadFutu(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法读取富途牛牛配置")
		return
	}
	old := rec
	rec.Enabled = false
	rec.OvernightEnabled = false
	rec.LastError = ""
	if err := m.publishFutuLogin(rec); err != nil {
		httpapi.Error(w, 500, "futu_login_failed", "无法更新 OpenD 登录配置")
		return
	}
	if err := m.storeFutu(r.Context(), rec); err != nil {
		_ = m.publishFutuLogin(old)
		httpapi.Error(w, http.StatusInternalServerError, "futu_settings_failed", "无法保存富途牛牛配置")
		return
	}
	m.futu.reset()
	m.syncOpenD(rec)
	httpapi.Write(w, http.StatusOK, map[string]bool{"ok": true})
}

func validateOpenDAddr(host string, port int, allowNonLocal bool) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("OpenD 主机不能为空")
	}
	if port < 1 || port > 65535 {
		return errors.New("OpenD 端口无效")
	}
	if strings.ContainsAny(host, "[]/") {
		return errors.New("OpenD 主机不能包含括号或路径")
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return nil
		}
		if !allowNonLocal {
			return errors.New("非本机 OpenD 地址需要勾选允许")
		}
		return nil
	}
	if strings.Contains(host, ":") {
		return errors.New("OpenD 主机不能包含端口")
	}
	if !allowNonLocal {
		return errors.New("非本机 OpenD 地址需要勾选允许")
	}
	if len(host) > 253 {
		return errors.New("OpenD 主机无效")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 {
			return errors.New("OpenD 主机无效")
		}
		for i, c := range label {
			ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || (c == '-' && i > 0 && i < len(label)-1)
			if !ok {
				return errors.New("OpenD 主机无效")
			}
		}
	}
	return nil
}
