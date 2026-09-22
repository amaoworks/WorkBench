package investment

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestChartingLibraryProxyPreservesCompressionAndCacheHeaders(t *testing.T) {
	payload := []byte(strings.Repeat("export const chart = 1;\n", 100))
	var compressed bytes.Buffer
	zip := gzip.NewWriter(&compressed)
	if _, err := zip.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	for _, encoding := range []string{"gzip, deflate, br", "identity", ""} {
		t.Run("encoding="+encoding, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				wantEncoding := encoding
				if wantEncoding == "" {
					wantEncoding = "identity"
				}
				if got := r.Header.Get("Accept-Encoding"); got != wantEncoding {
					t.Errorf("upstream encoding = %q, want %q", got, wantEncoding)
				}
				w.Header().Set("Content-Type", "text/javascript")
				w.Header().Set("Vary", "Accept-Encoding")
				w.Header().Set("Cache-Control", "public, max-age=3600")
				w.Header().Set("ETag", `"chart-v1"`)
				if r.Header.Get("If-None-Match") == `"chart-v1"` {
					w.WriteHeader(http.StatusNotModified)
					return
				}
				body := payload
				if strings.Contains(encoding, "gzip") {
					w.Header().Set("Content-Encoding", "gzip")
					body = compressed.Bytes()
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				_, _ = w.Write(body)
			}))
			defer upstream.Close()
			proxy := newTVProxy(upstream.URL)
			req := httptest.NewRequest(http.MethodGet, "/charting_library/charting_library.esm.js", nil)
			req.Header.Set("Accept-Encoding", encoding)
			got := httptest.NewRecorder()
			proxy.ServeHTTP(got, req)
			if got.Code != http.StatusOK {
				t.Fatalf("status = %d", got.Code)
			}
			body := got.Body.Bytes()
			if strings.Contains(encoding, "gzip") {
				if got.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(body, compressed.Bytes()) {
					t.Fatal("compressed response was changed")
				}
			} else if got.Header().Get("Content-Encoding") != "" || !bytes.Equal(body, payload) {
				t.Fatal("uncompressed response was changed")
			}
			if got.Header().Get("Vary") != "Accept-Encoding" || got.Header().Get("Cache-Control") != "public, max-age=3600" || got.Header().Get("ETag") != `"chart-v1"` {
				t.Fatalf("cache headers changed: %v", got.Header())
			}
			req.Header.Set("If-None-Match", got.Header().Get("ETag"))
			cached := httptest.NewRecorder()
			proxy.ServeHTTP(cached, req)
			if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
				t.Fatalf("conditional request: %d %s", cached.Code, cached.Body)
			}
		})
	}
}

func TestTerminalScriptsRevalidateByContent(t *testing.T) {
	module := &Module{}
	const asset = "/investment/terminal/datafeed.js"
	initial := httptest.NewRecorder()
	module.serveTerminal(initial, httptest.NewRequest(http.MethodGet, asset, nil))
	etag := initial.Header().Get("ETag")
	if initial.Code != http.StatusOK || initial.Body.Len() == 0 || etag == "" || initial.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("initial asset: %d %v", initial.Code, initial.Header())
	}
	for _, match := range []string{etag, "W/" + etag, `"previous-version"`} {
		req := httptest.NewRequest(http.MethodGet, asset, nil)
		req.Header.Set("If-None-Match", match)
		got := httptest.NewRecorder()
		module.serveTerminal(got, req)
		if match == `"previous-version"` {
			if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), initial.Body.Bytes()) {
				t.Fatal("a stale validator must receive the current script")
			}
		} else if got.Code != http.StatusNotModified || got.Body.Len() != 0 || got.Header().Get("ETag") != etag {
			t.Fatalf("revalidation: %d %v", got.Code, got.Header())
		}
	}
	head := httptest.NewRecorder()
	module.serveTerminal(head, httptest.NewRequest(http.MethodHead, asset, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != strconv.Itoa(initial.Body.Len()) {
		t.Fatalf("HEAD: %d %v", head.Code, head.Header())
	}
	html := httptest.NewRecorder()
	module.serveTerminal(html, httptest.NewRequest(http.MethodGet, "/investment/terminal", nil))
	if html.Header().Get("Cache-Control") != "no-store" || html.Header().Get("ETag") != "" {
		t.Fatal("terminal document must stay uncached")
	}
	if body, _ := io.ReadAll(html.Result().Body); !bytes.Contains(body, []byte("<html")) {
		t.Fatal("terminal document is missing")
	}
}
