package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
)

func TestFillDataIsolatesRoutingAndPreservesRequestContext(t *testing.T) {
	type contextKey struct{}
	data := chi.NewRouter()
	data.Get("/api/cards/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Value(contextKey{}) != "authenticated" {
			t.Error("lost caller context")
		}
		httpapi.Write(w, http.StatusOK, map[string]string{"id": chi.URLParam(r, "id")})
	})
	service := &Service{data: data, budget: time.Second}
	outer := chi.NewRouter()
	outer.Get("/api/dashboard/{page}", func(w http.ResponseWriter, r *http.Request) {
		widgets := make([]visibleWidget, 6)
		for i := range widgets {
			widgets[i].WidgetDefinition = contracts.WidgetDefinition{DataRoute: fmt.Sprintf("/api/cards/%d", i)}
		}
		service.fillData(r, widgets)
		if got := chi.RouteContext(r.Context()).RoutePattern(); got != "/api/dashboard/{page}" {
			t.Errorf("caller route changed to %q", got)
		}
		for i, widget := range widgets {
			var body struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(widget.Data, &body); err != nil || body.ID != fmt.Sprint(i) {
				t.Errorf("card %d: data=%s, err=%v", i, widget.Data, err)
			}
		}
	})
	for range 10 {
		req := httptest.NewRequest(http.MethodGet, "/api/dashboard/main", nil)
		req = req.WithContext(context.WithValue(req.Context(), contextKey{}, "authenticated"))
		outer.ServeHTTP(httptest.NewRecorder(), req)
	}
}
