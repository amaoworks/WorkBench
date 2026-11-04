package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"workbench/internal/contracts"
	workbenchdb "workbench/internal/foundation/database"
)

func TestDashboardLayoutsAndResetBelongToCurrentUser(t *testing.T) {
	database, err := workbenchdb.Open(context.Background(), workbenchdb.Config{Path: filepath.Join(t.TempDir(), "dashboard.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.MigrateCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := New(database.SQL(), []contracts.WidgetDefinition{{
		ID: "test.card", Module: "test", Size: contracts.WidgetSmall, Order: 1,
	}}, func(contracts.ModuleID) bool { return true })
	userContext := func(id string) context.Context {
		return contracts.WithPrincipal(context.Background(), contracts.Principal{UserID: id, Role: contracts.RoleUser})
	}
	alice, bob := userContext("alice"), userContext("bob")
	for _, input := range []struct {
		ctx  context.Context
		body string
	}{
		{alice, `{"items":[{"id":"test.card","visible":false,"size":"large","order":9}]}`},
		{bob, `{"items":[{"id":"test.card","visible":true,"size":"medium","order":3}]}`},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/api/dashboard/layout", strings.NewReader(input.body)).WithContext(input.ctx)
		service.Save(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("save=%d %s", recorder.Code, recorder.Body.String())
		}
	}
	for _, check := range []struct {
		ctx     context.Context
		visible bool
		size    contracts.WidgetSize
		order   int
	}{{alice, false, contracts.WidgetLarge, 9}, {bob, true, contracts.WidgetMedium, 3}} {
		items, err := service.list(check.ctx)
		if err != nil || len(items) != 1 || items[0].Visible != check.visible || items[0].Size != check.size || items[0].Order != check.order {
			t.Fatalf("list=%+v, error=%v", items, err)
		}
	}
	recorder := httptest.NewRecorder()
	service.Reset(recorder, httptest.NewRequest(http.MethodDelete, "/api/dashboard/layout", nil).WithContext(alice))
	if recorder.Code != http.StatusOK {
		t.Fatalf("reset=%d %s", recorder.Code, recorder.Body.String())
	}
	items, err := service.list(alice)
	if err != nil || len(items) != 1 || !items[0].Visible || items[0].Order != 1 || items[0].Size != contracts.WidgetSmall {
		t.Fatalf("reset alice=%+v, error=%v", items, err)
	}
	items, err = service.list(bob)
	if err != nil || len(items) != 1 || items[0].Order != 3 || items[0].Size != contracts.WidgetMedium {
		t.Fatalf("reset changed bob=%+v, error=%v", items, err)
	}
	var bobLayouts int
	if err := database.SQL().QueryRow("SELECT COUNT(*) FROM user_settings WHERE user_id='bob' AND section='dashboard'").Scan(&bobLayouts); err != nil || bobLayouts != 1 {
		t.Fatalf("bob layouts=%d, error=%v", bobLayouts, err)
	}
}

func TestDashboardRequiresPrincipalBeforeReadingOrSaving(t *testing.T) {
	service := &Service{}
	if _, err := service.list(context.Background()); !errors.Is(err, contracts.ErrUnauthenticated) {
		t.Fatalf("anonymous list error=%v", err)
	}
	for _, handler := range []http.HandlerFunc{service.ServeHTTP, service.Catalog, service.Save, service.Reset} {
		recorder := httptest.NewRecorder()
		handler(recorder, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous dashboard response=%d %s", recorder.Code, recorder.Body.String())
		}
	}
}
