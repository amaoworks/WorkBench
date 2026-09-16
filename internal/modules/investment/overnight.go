package investment

import (
	"net/http"

	"workbench/internal/foundation/httpapi"
)

type overnightSettingsView struct {
	Enabled         bool   `json:"enabled"`
	Provider        string `json:"provider"`
	ProviderEnabled bool   `json:"providerEnabled"`
}

func (m *Module) getOvernight(w http.ResponseWriter, r *http.Request) {
	rec, err := m.loadFutu(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "overnight_settings_failed", "无法读取夜盘设置")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, 200, overnightSettingsView{Enabled: rec.Enabled && rec.OvernightEnabled, Provider: "futu", ProviderEnabled: rec.Enabled})
}

func (m *Module) saveOvernight(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if httpapi.Decode(w, r, &input, 1024) != nil || input.Enabled == nil {
		httpapi.Error(w, 400, "invalid_request", "请指定是否启用夜盘")
		return
	}
	m.futu.connectMu.Lock()
	defer m.futu.connectMu.Unlock()
	rec, err := m.loadFutu(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "overnight_settings_failed", "无法读取夜盘设置")
		return
	}
	if *input.Enabled && !rec.Enabled {
		httpapi.Error(w, 409, "futu_required", "请先在设置中启用富途牛牛，再启用夜盘行情")
		return
	}
	rec.OvernightEnabled = *input.Enabled
	if err := m.storeFutu(r.Context(), rec); err != nil {
		httpapi.Error(w, 500, "overnight_settings_failed", "无法保存夜盘设置")
		return
	}
	m.futu.reset()
	m.getOvernight(w, r)
}
