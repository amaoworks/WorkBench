package dashboard

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

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

// visibleWidget is one card of GET /api/dashboard. Data carries the card's
// DataRoute response when ?include=data could produce it within the budget.
type visibleWidget struct {
	Widget
	Data json.RawMessage `json:"data,omitempty"`
}

// widgetDataBudget bounds how long the overview waits for one card's data.
// Slower cards are sent without data and load on their own in the browser.
const widgetDataBudget = 1500 * time.Millisecond

const maxWidgetDataBytes = 512 << 10

type Service struct {
	widgets []contracts.WidgetDefinition
	enabled EnabledFunc
	queries *dbsqlc.Queries
	data    http.Handler
	budget  time.Duration
}

func New(db *sql.DB, widgets []contracts.WidgetDefinition, enabled EnabledFunc) *Service {
	return &Service{widgets: slices.Clone(widgets), enabled: enabled, queries: dbsqlc.New(db), budget: widgetDataBudget}
}

// UseDataHandler sets the handler that serves widget data routes for
// GET /api/dashboard?include=data. It must only serve read-only GET routes and
// apply the same module gating as the public router.
func (s *Service) UseDataHandler(handler http.Handler) { s.data = handler }

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
	widgets := make([]visibleWidget, 0, len(items))
	for _, item := range items {
		if item.Enabled && item.Visible {
			widgets = append(widgets, visibleWidget{Widget: item})
		}
	}
	if r.URL.Query().Get("include") == "data" && s.data != nil {
		s.fillData(r, widgets)
	}
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, 200, map[string]any{"widgets": widgets})
}

// fillData resolves every card's data route in parallel through the internal
// data handler, reusing the caller's authenticated request context.
func (s *Service) fillData(r *http.Request, widgets []visibleWidget) {
	ctx, cancel := context.WithTimeout(r.Context(), s.budget)
	defer cancel()
	var wg sync.WaitGroup
	for i := range widgets {
		route := widgets[i].DataRoute
		if !strings.HasPrefix(route, "/api/") {
			continue
		}
		target, err := url.ParseRequestURI(route)
		if err != nil {
			continue
		}
		wg.Add(1)
		go func(i int, target *url.URL) {
			defer wg.Done()
			widgets[i].Data = s.fetch(ctx, r, target)
		}(i, target)
	}
	wg.Wait()
}

func (s *Service) fetch(ctx context.Context, outer *http.Request, target *url.URL) (body json.RawMessage) {
	defer func() {
		if recover() != nil {
			body = nil
		}
	}()
	// Each internal dispatch needs its own mutable routing state.
	ctx = context.WithValue(ctx, chi.RouteCtxKey, chi.NewRouteContext())
	req := outer.Clone(ctx)
	req.Method = http.MethodGet
	req.URL = target
	req.RequestURI = target.RequestURI()
	req.Body = http.NoBody
	req.ContentLength = 0
	done := make(chan *capture, 1)
	go func() {
		rec := &capture{header: http.Header{}, status: http.StatusOK}
		defer func() {
			_ = recover()
			done <- rec
		}()
		s.data.ServeHTTP(rec, req)
	}()
	select {
	case rec := <-done:
		raw := rec.body.Bytes()
		if rec.status != http.StatusOK || rec.overflow || !json.Valid(raw) {
			return nil
		}
		return json.RawMessage(raw)
	case <-ctx.Done():
		return nil
	}
}

type capture struct {
	header   http.Header
	status   int
	wrote    bool
	overflow bool
	body     bytes.Buffer
}

func (c *capture) Header() http.Header { return c.header }
func (c *capture) WriteHeader(status int) {
	if !c.wrote {
		c.status, c.wrote = status, true
	}
}
func (c *capture) Write(p []byte) (int, error) {
	c.wrote = true
	if c.body.Len()+len(p) > maxWidgetDataBytes {
		c.overflow = true
		return len(p), nil
	}
	return c.body.Write(p)
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
