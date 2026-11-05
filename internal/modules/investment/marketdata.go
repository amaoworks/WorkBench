package investment

import (
	"context"
	"net/http"

	"workbench/internal/foundation/httpapi"
)

// marketDataView describes routing, not upstream availability or quote entitlement.
type marketDataView struct {
	Provider                string `json:"provider"`
	SchwabConnected         bool   `json:"schwabConnected"`
	ReauthorizationRequired bool   `json:"reauthorizationRequired"`
	FutuEnabled             bool   `json:"futuEnabled"`
	OvernightEnabled        bool   `json:"overnightEnabled"`
}

func (m *Module) marketDataStatus(ctx context.Context) (marketDataView, error) {
	schwab, err := m.loadSchwab(ctx)
	if err != nil {
		return marketDataView{}, err
	}
	futu, err := m.loadFutu(ctx)
	if err != nil {
		return marketDataView{}, err
	}
	view := marketDataView{Provider: "none", SchwabConnected: schwab.view().Connected, ReauthorizationRequired: schwab.ReauthorizationRequired, FutuEnabled: futu.Enabled, OvernightEnabled: futu.Enabled && futu.OvernightEnabled}
	if view.SchwabConnected {
		view.Provider = "schwab"
	} else if view.FutuEnabled {
		view.Provider = "futu"
	}
	return view, nil
}

func (m *Module) getMarketData(w http.ResponseWriter, r *http.Request) {
	if _, err := m.requireUser(r.Context()); err != nil {
		httpapi.Error(w, http.StatusUnauthorized, "authentication_required", "请登录")
		return
	}
	view, err := m.marketDataStatus(r.Context())
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "market_data_failed", "无法读取行情来源")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, http.StatusOK, view)
}
