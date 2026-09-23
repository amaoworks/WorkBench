package investment

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestChartingLibraryEntryPreservesCompressionAndRevalidatesInBrowser(t *testing.T) {
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
				wantEncoding := chartRequestEncoding(encoding)
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
			proxy := newTVProxy(upstream.URL, "")
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
			if got.Header().Get("Vary") != "Accept-Encoding" || got.Header().Get("Cache-Control") != "no-cache" || got.Header().Get("ETag") != `"chart-v1"` {
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

func TestChartingLibraryProxyCachesBundles(t *testing.T) {
	type asset struct {
		body   []byte
		status int
		etag   string
	}
	files := map[string]*asset{}
	var hits int
	var sawCookie, sawAuthorization bool
	var encodings []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		encodings = append(encodings, r.Header.Get("Accept-Encoding"))
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			sawCookie = r.Header.Get("Cookie") != ""
			sawAuthorization = r.Header.Get("Authorization") != ""
		}
		item, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if item.status != http.StatusOK {
			http.Error(w, "upstream failed", item.status)
			return
		}
		if r.Header.Get("If-None-Match") == item.etag {
			w.Header().Set("ETag", item.etag)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		body := item.body
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			var compressed bytes.Buffer
			zip := gzip.NewWriter(&compressed)
			if _, err := zip.Write(item.body); err != nil {
				t.Errorf("gzip: %v", err)
			}
			if err := zip.Close(); err != nil {
				t.Errorf("gzip close: %v", err)
			}
			body = compressed.Bytes()
			w.Header().Set("Content-Encoding", "gzip")
		}
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("ETag", item.etag)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	}))
	defer upstream.Close()
	proxy := newTVProxy(upstream.URL, "")
	get := func(path, encoding, validators string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept-Encoding", encoding)
		req.Header.Set("Cookie", "workbench_session=secret")
		req.Header.Set("Authorization", "Bearer nope")
		if validators != "" {
			req.Header.Set("If-None-Match", validators)
		}
		got := httptest.NewRecorder()
		proxy.ServeHTTP(got, req)
		return got
	}

	jsPath := "/charting_library/bundles/library.02a5618b954328b298b8.js"
	cssPath := "/charting_library/bundles/8816.a218c8a1ecc38e49a8f8.css"
	files[jsPath] = &asset{body: []byte("library-v1"), status: http.StatusOK, etag: `"js-v1"`}
	files[cssPath] = &asset{body: []byte("style-v1"), status: http.StatusOK, etag: `"css-v1"`}

	jsGzip := get(jsPath, "gzip", "")
	if jsGzip.Code != http.StatusOK || jsGzip.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip bundle: %d %s", jsGzip.Code, jsGzip.Header().Get("Content-Encoding"))
	}
	jsIdentity := get(jsPath, "identity", "")
	if jsIdentity.Code != http.StatusOK || jsIdentity.Header().Get("Content-Encoding") != "" || !bytes.Equal(jsIdentity.Body.Bytes(), []byte("library-v1")) {
		t.Fatalf("identity bundle: %d %q", jsIdentity.Code, jsIdentity.Body.Bytes())
	}
	if bytes.Equal(jsGzip.Body.Bytes(), jsIdentity.Body.Bytes()) {
		t.Fatal("gzip and identity bodies were swapped or stored as one representation")
	}
	css := get(cssPath, "identity", "")
	if css.Code != http.StatusOK || !bytes.Equal(css.Body.Bytes(), []byte("style-v1")) {
		t.Fatalf("css bundle: %d %q", css.Code, css.Body.Bytes())
	}
	afterMisses := hits

	jsGzipAgain := get(jsPath, "gzip", "")
	jsIdentityAgain := get(jsPath, "identity", "")
	cssAgain := get(cssPath, "identity", "")
	if hits != afterMisses {
		t.Fatalf("cached bundles contacted upstream again: hits %d, want %d", hits, afterMisses)
	}
	if jsGzipAgain.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(jsGzipAgain.Body.Bytes(), jsGzip.Body.Bytes()) {
		t.Fatal("repeat gzip bundle did not replay the cached body and encoding")
	}
	if !bytes.Equal(jsIdentityAgain.Body.Bytes(), jsIdentity.Body.Bytes()) || jsIdentityAgain.Header().Get("Content-Encoding") != "" {
		t.Fatal("repeat identity bundle did not replay the cached body")
	}
	if !bytes.Equal(cssAgain.Body.Bytes(), []byte("style-v1")) {
		t.Fatal("repeat stylesheet did not replay the cached body")
	}
	notModified := get(jsPath, "gzip", `"js-v1"`)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 || hits != afterMisses {
		t.Fatalf("conditional bundle: %d body %d hits %d", notModified.Code, notModified.Body.Len(), hits)
	}

	files[jsPath].body = []byte("library-v2")
	files[jsPath].etag = `"js-v2"`
	pinned := get(jsPath, "identity", "")
	if hits != afterMisses || !bytes.Equal(pinned.Body.Bytes(), []byte("library-v1")) {
		t.Fatalf("hashed bundle changed with upstream: hits %d body %q", hits, pinned.Body.Bytes())
	}

	failedPath := "/charting_library/bundles/broken.8f4c2b1a0937e6d5c012.js"
	files[failedPath] = &asset{status: http.StatusBadGateway}
	failed := get(failedPath, "identity", "")
	if failed.Code != http.StatusBadGateway {
		t.Fatalf("upstream error status = %d", failed.Code)
	}
	failedHits := hits
	files[failedPath] = &asset{body: []byte("recovered"), status: http.StatusOK, etag: `"recovered"`}
	recovered := get(failedPath, "identity", "")
	if hits != failedHits+1 || recovered.Code != http.StatusOK || !bytes.Equal(recovered.Body.Bytes(), []byte("recovered")) {
		t.Fatalf("error was replayed or upstream was skipped: hits %d status %d body %q", hits, recovered.Code, recovered.Body.Bytes())
	}

	for _, path := range []string{"/charting_library/charting_library.esm.js", "/charting_library/sameorigin.html"} {
		files[path] = &asset{body: []byte("entry-v1"), status: http.StatusOK, etag: `"entry-v1"`}
		first := get(path, "identity", "")
		before := hits
		files[path].body = []byte("entry-v2")
		files[path].etag = `"entry-v2"`
		cached := get(path, "identity", "")
		if hits != before || !bytes.Equal(cached.Body.Bytes(), first.Body.Bytes()) {
			t.Fatalf("fresh entry point contacted upstream: %s", path)
		}
		transport := proxy.Transport.(*chartLibraryTransport)
		transport.mu.Lock()
		transport.cache[path]["identity"].expires = time.Now().Add(-time.Second)
		transport.mu.Unlock()
		second := get(path, "identity", "")
		if hits != before+1 || !bytes.Equal(first.Body.Bytes(), []byte("entry-v1")) || !bytes.Equal(second.Body.Bytes(), []byte("entry-v2")) {
			t.Fatalf("unversioned %s stayed pinned: hits %d first %q second %q", path, hits, first.Body.Bytes(), second.Body.Bytes())
		}
	}
	if sawCookie || sawAuthorization {
		t.Fatal("workspace credentials were forwarded upstream")
	}
	for _, encoding := range encodings {
		if encoding == "" {
			t.Fatal("upstream request dropped Accept-Encoding")
		}
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

func TestChartWarmupDiscoversOnlyLoaderBundles(t *testing.T) {
	source := []byte(`const files = ["bundles/runtime.c814a72e90f35b6d0281.js", "bundles/__LANG__.36031.9601ddd67bd9cbb4674e.js", "bundles/8816.a218c8a1ecc38e49a8f8.css"];`)
	names := discoverChartBundles(source)
	if len(names) != 3 {
		t.Fatalf("loader bundles = %v", names)
	}
	foundLanguage := false
	for _, name := range names {
		if name == "bundles/zh.36031.9601ddd67bd9cbb4674e.js" {
			foundLanguage = true
		}
	}
	if !foundLanguage {
		t.Fatalf("missing Chinese language bundle: %v", names)
	}
}

func TestChartLibraryWarmupServesLaterEncodingsFromCache(t *testing.T) {
	esm := `import "./bundles/runtime.c814a72e90f35b6d0281.js"; import "./bundles/library.9a20d761e34f8c5b0127.js";`
	runtime := `o.u=e=>1===e?"widget."+e+".8f4c2b1a0937e6d5c012.js":{2:"1c7e52a84d0396fb2e10"}[e]+".js",o.miniCssF=e=>e+"."+{3:"72e4b1903f5c86ad012e"}[e]+".css"`
	files := map[string]string{
		"/charting_library/charting_library.esm.js":                  esm,
		"/charting_library/sameorigin.html":                          "<html></html>",
		"/charting_library/bundles/runtime.c814a72e90f35b6d0281.js":  runtime,
		"/charting_library/bundles/library.9a20d761e34f8c5b0127.js":  "library-body",
		"/charting_library/bundles/widget.1.8f4c2b1a0937e6d5c012.js": "widget",
		"/charting_library/bundles/2.1c7e52a84d0396fb2e10.js":        "chunk",
		"/charting_library/bundles/3.72e4b1903f5c86ad012e.css":       "style",
	}
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		payload := []byte(body)
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			var compressed bytes.Buffer
			zip := gzip.NewWriter(&compressed)
			_, _ = zip.Write(payload)
			_ = zip.Close()
			payload = compressed.Bytes()
			w.Header().Set("Content-Encoding", "gzip")
		}
		w.Header().Set("ETag", `"warm"`)
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()
	dir := t.TempDir()
	proxy := newTVProxy(upstream.URL, dir)
	if _, err := warmChartLibrary(context.Background(), proxy); err != nil {
		t.Fatal(err)
	}
	warmHits := hits.Load()
	if warmHits != 4 {
		t.Fatalf("warmup must fetch two entry points and two core bundles, got %d requests", warmHits)
	}
	req := httptest.NewRequest(http.MethodGet, "/charting_library/bundles/library.9a20d761e34f8c5b0127.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	got := httptest.NewRecorder()
	proxy.ServeHTTP(got, req)
	if hits.Load() != warmHits {
		t.Fatalf("cached bundle contacted upstream again: %d -> %d", warmHits, hits.Load())
	}
	if got.Code != http.StatusOK || got.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("cached response: %d %s", got.Code, got.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(bytes.NewReader(got.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "library-body" {
		t.Fatalf("cached body %q", plain)
	}
	reloaded := newTVProxy(upstream.URL, dir)
	if err := reloaded.Transport.(*chartLibraryTransport).loadDisk(); err != nil {
		t.Fatal(err)
	}
	again := httptest.NewRecorder()
	reloaded.ServeHTTP(again, req)
	if hits.Load() != warmHits || again.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(again.Body.Bytes(), got.Body.Bytes()) {
		t.Fatalf("disk cache missed: hits %d status %d", hits.Load(), again.Code)
	}
}

func TestChartLibrarySingleflightSharesOneUpstreamFetch(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var hits atomic.Int32
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		once.Do(func() { close(started) })
		<-release
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			var compressed bytes.Buffer
			zip := gzip.NewWriter(&compressed)
			_, _ = zip.Write([]byte("shared"))
			_ = zip.Close()
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(compressed.Bytes())
			return
		}
		_, _ = w.Write([]byte("shared"))
	}))
	defer upstream.Close()
	proxy := newTVProxy(upstream.URL, "")
	var group sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			req := httptest.NewRequest(http.MethodGet, "/charting_library/bundles/library.8f4c2b1a0937e6d5c012.js", nil)
			req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
			got := httptest.NewRecorder()
			proxy.ServeHTTP(got, req)
			codes[i] = got.Code
		}(i)
	}
	<-started
	time.Sleep(50 * time.Millisecond)
	if hits.Load() != 1 {
		t.Fatalf("parallel misses reached upstream %d times", hits.Load())
	}
	close(release)
	group.Wait()
	if hits.Load() != 1 || codes[0] != http.StatusOK || codes[1] != http.StatusOK {
		t.Fatalf("hits %d codes %v", hits.Load(), codes)
	}
}

func TestChartFetchSurvivesClosingTheFirstPage(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			w.Header().Set("ETag", `"shared-bundle"`)
			_, _ = w.Write([]byte("export const loaded = true;"))
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	defer close(release)
	transport := newTVProxy(upstream.URL, "").Transport
	address := upstream.URL + "/charting_library/bundles/library.02a5618b954328b298b8.js"
	ctx, cancel := context.WithCancel(context.Background())
	first, _ := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	firstDone := make(chan error, 1)
	go func() {
		resp, err := transport.RoundTrip(first)
		if resp != nil {
			resp.Body.Close()
		}
		firstDone <- err
	}()
	<-started
	cancel()
	select {
	case err := <-firstDone:
		if err != context.Canceled {
			t.Fatalf("closed page: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("closing a page did not release its waiter")
	}
	// A replacement page and a cancelled waiting page share the same fetch.
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waitCancel()
	waiter, _ := http.NewRequestWithContext(waitCtx, http.MethodGet, address, nil)
	if _, err := transport.RoundTrip(waiter); err != context.Canceled {
		t.Fatalf("cancelled waiter: %v", err)
	}
	second, _ := http.NewRequest(http.MethodGet, address, nil)
	go func() { release <- struct{}{} }()
	resp, err := transport.RoundTrip(second)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != "export const loaded = true;" || hits.Load() != 1 {
		t.Fatalf("reopened chart: status %d, body %q, error %v, upstream requests %d", resp.StatusCode, body, err, hits.Load())
	}
}

func TestChartCacheMissDoesNotShareAConditionalEmptyBody(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("ETag", `"current"`)
		if r.Header.Get("If-None-Match") != "" || r.Header.Get("If-Modified-Since") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte("current bundle"))
	}))
	defer upstream.Close()
	proxy := newTVProxy(upstream.URL, "")
	req := httptest.NewRequest(http.MethodGet, "/charting_library/bundles/library.02a5618b954328b298b8.js", nil)
	req.Header.Set("If-None-Match", `"current"`)
	req.Header.Set("If-Modified-Since", time.Now().Format(http.TimeFormat))
	conditional := httptest.NewRecorder()
	proxy.ServeHTTP(conditional, req)
	req.Header.Del("If-None-Match")
	req.Header.Del("If-Modified-Since")
	newPage := httptest.NewRecorder()
	proxy.ServeHTTP(newPage, req)
	if conditional.Code != http.StatusNotModified || newPage.Code != http.StatusOK || newPage.Body.String() != "current bundle" || hits.Load() != 1 {
		t.Fatalf("conditional=%d new=%d body=%q hits=%d", conditional.Code, newPage.Code, newPage.Body.String(), hits.Load())
	}
}

func TestChartEncodingNegotiation(t *testing.T) {
	for accept, want := range map[string]string{
		"": "identity", "br, zstd": "identity", "gzip, br, zstd": "gzip",
		"gzip;q=0.5, br": "gzip", "gzip; q=0, identity": "identity",
		"GZIP; q=1": "gzip", "*;q=0.5": "gzip", "*;q=0": "",
		"gzip;q=0, identity;q=0, br": "",
	} {
		if got := chartRequestEncoding(accept); got != want {
			t.Errorf("Accept-Encoding %q: got %q, want %q", accept, got, want)
		}
	}
}

func TestChartCacheSharesGzipAcrossBrowserEncodingLists(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// A CDN may prefer Brotli when offered. Prefetch and browser requests
		// must agree on a representation our cache can decode and reuse.
		if strings.Contains(r.Header.Get("Accept-Encoding"), "br") {
			w.Header().Set("Content-Encoding", "br")
			_, _ = w.Write([]byte("brotli representation"))
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		zip := gzip.NewWriter(w)
		_, _ = zip.Write([]byte("shared chart bundle"))
		_ = zip.Close()
	}))
	defer upstream.Close()
	proxy := newTVProxy(upstream.URL, "")
	for _, accept := range []string{"gzip, deflate, br, zstd", "br, gzip;q=0.5", "gzip"} {
		req := httptest.NewRequest(http.MethodGet, "/charting_library/bundles/library.02a5618b954328b298b8.js", nil)
		req.Header.Set("Accept-Encoding", accept)
		got := httptest.NewRecorder()
		proxy.ServeHTTP(got, req)
		if got.Code != http.StatusOK || got.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("%s: status=%d encoding=%s", accept, got.Code, got.Header().Get("Content-Encoding"))
		}
		body, err := decodeChartBody(got.Header(), got.Body.Bytes())
		if err != nil || string(body) != "shared chart bundle" {
			t.Fatalf("%s: body=%q error=%v", accept, body, err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("compatible encoding lists made %d upstream requests", hits.Load())
	}
}

func TestChartEntryFreshnessRespectsUpstream(t *testing.T) {
	for _, test := range []struct {
		control string
		age     string
		want    time.Duration
	}{
		{"max-age=3600", "600", chartEntryTTL},
		{"max-age=60", "40", 20 * time.Second},
		{"max-age=60", "80", 0},
		{"no-cache, max-age=3600", "", 0},
		{"no-store", "", 0},
	} {
		header := http.Header{"Cache-Control": {test.control}, "Age": {test.age}}
		if got := chartEntryFreshness(header); got != test.want {
			t.Errorf("%s age %s: %s, want %s", test.control, test.age, got, test.want)
		}
	}
}

func TestChartWarmupReportsUpstreamFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	files, err := warmChartLibrary(context.Background(), newTVProxy(upstream.URL, ""))
	if files != 0 || err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("warmup: files=%d, error=%v", files, err)
	}
}
