package dashboard

import (
	"net/http"
	"slices"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
)

type EnabledFunc func(contracts.ModuleID) bool

type Service struct {
	widgets []contracts.WidgetDefinition
	enabled EnabledFunc
}

func New(widgets []contracts.WidgetDefinition, enabled EnabledFunc) *Service {
	copyOfWidgets := slices.Clone(widgets)
	slices.SortFunc(copyOfWidgets, func(a, b contracts.WidgetDefinition) int { return a.Order - b.Order })
	return &Service{widgets: copyOfWidgets, enabled: enabled}
}

func (s *Service) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	widgets := make([]contracts.WidgetDefinition, 0, len(s.widgets))
	for _, widget := range s.widgets {
		if s.enabled(widget.Module) {
			widgets = append(widgets, widget)
		}
	}
	httpapi.Write(w, http.StatusOK, map[string]any{"widgets": widgets})
}
