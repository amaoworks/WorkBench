package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/httpapi"
)

type EnabledFunc func(contracts.ModuleID) bool

type Preference struct {
	ID      string               `json:"id"`
	Visible *bool                `json:"visible"`
	Size    contracts.WidgetSize `json:"size"`
	Order   int                  `json:"order"`
}

type Widget struct {
	contracts.WidgetDefinition
	Visible bool `json:"visible"`
	Enabled bool `json:"enabled"`
}

type Service struct {
	widgets []contracts.WidgetDefinition
	enabled EnabledFunc
	queries *dbsqlc.Queries
}

func New(db *sql.DB, widgets []contracts.WidgetDefinition, enabled EnabledFunc) *Service {
	return &Service{widgets: slices.Clone(widgets), enabled: enabled, queries: dbsqlc.New(db)}
}

func (s *Service) list(ctx context.Context) ([]Widget, error) {
	preferences := map[string]Preference{}
	raw, err := s.queries.GetDashboardLayout(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		var items []Preference
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return nil, err
		}
		for _, item := range items {
			preferences[item.ID] = item
		}
	}
	widgets := make([]Widget, 0, len(s.widgets))
	for _, definition := range s.widgets {
		widget := Widget{WidgetDefinition: definition, Visible: true, Enabled: s.enabled(definition.Module)}
		if pref, ok := preferences[definition.ID]; ok && pref.Visible != nil {
			widget.Visible, widget.Size, widget.Order = *pref.Visible, pref.Size, pref.Order
		}
		widgets = append(widgets, widget)
	}
	slices.SortFunc(widgets, func(a, b Widget) int {
		if a.Order < b.Order {
			return -1
		}
		if a.Order > b.Order {
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return widgets, nil
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	items, err := s.list(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "dashboard_failed", "无法读取总览配置")
		return
	}
	widgets := make([]Widget, 0, len(items))
	for _, item := range items {
		if item.Enabled && item.Visible {
			widgets = append(widgets, item)
		}
	}
	httpapi.Write(w, 200, map[string]any{"widgets": widgets})
}

// Catalog includes disabled modules so their preferences remain editable and survive re-enabling.
func (s *Service) Catalog(w http.ResponseWriter, r *http.Request) {
	items, err := s.list(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "dashboard_failed", "无法读取总览配置")
		return
	}
	httpapi.Write(w, 200, map[string]any{"widgets": items})
}

func (s *Service) Save(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Items []Preference `json:"items"`
	}
	if err := httpapi.Decode(w, r, &input, 65536); err != nil || len(input.Items) != len(s.widgets) {
		httpapi.Error(w, 400, "invalid_layout", "请提交完整的卡片配置")
		return
	}
	known := map[string]bool{}
	for _, widget := range s.widgets {
		known[widget.ID] = true
	}
	for _, pref := range input.Items {
		if !known[pref.ID] || pref.Visible == nil || pref.Order < 0 || pref.Order > 100000 ||
			(pref.Size != contracts.WidgetSmall && pref.Size != contracts.WidgetMedium && pref.Size != contracts.WidgetLarge) {
			httpapi.Error(w, 400, "invalid_layout", "卡片配置包含未知、重复或无效的选项")
			return
		}
		delete(known, pref.ID)
	}
	raw, _ := json.Marshal(input.Items)
	if err := s.queries.SaveDashboardLayout(r.Context(), string(raw)); err != nil {
		httpapi.Error(w, 500, "dashboard_failed", "无法保存总览配置")
		return
	}
	s.Catalog(w, r)
}

func (s *Service) Reset(w http.ResponseWriter, r *http.Request) {
	if err := s.queries.ResetDashboardLayout(r.Context()); err != nil {
		httpapi.Error(w, 500, "dashboard_failed", "无法重置总览配置")
		return
	}
	s.Catalog(w, r)
}
