package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplySessionSkipsIdleRefreshForHashedChartBundles(t *testing.T) {
	var saved bool
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Vary", "Cookie")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Set("Cache-Control", `no-cache="Set-Cookie"`)
		w.Header().Set("Set-Cookie", "workbench_session=secret")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	handler := applySession(
		func(http.Handler) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { saved = true })
		},
		func(h http.Handler) http.Handler { return h },
		next,
	)

	bundle := httptest.NewRecorder()
	handler.ServeHTTP(bundle, httptest.NewRequest(http.MethodGet, "/charting_library/bundles/library.02a5618b954328b298b8.js", nil))
	if saved {
		t.Fatal("hashed chart bundles must not commit the idle session")
	}
	if bundle.Code != http.StatusOK || bundle.Header().Get("Set-Cookie") != "" {
		t.Fatalf("bundle: %d cookies %v", bundle.Code, bundle.Header().Values("Set-Cookie"))
	}
	if bundle.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("bundle cache: %q", bundle.Header().Get("Cache-Control"))
	}
	if got := bundle.Header().Get("Vary"); got != "Accept-Encoding" {
		t.Fatalf("bundle Vary: %q", got)
	}

	saved = false
	entry := httptest.NewRecorder()
	handler.ServeHTTP(entry, httptest.NewRequest(http.MethodGet, "/charting_library/charting_library.esm.js", nil))
	if !saved {
		t.Fatal("entry points must still use LoadAndSave")
	}
}
