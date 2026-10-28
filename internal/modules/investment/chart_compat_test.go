package investment

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTVNightQuoteCompatFreshAndDiskCachedBundles(t *testing.T) {
	const source = `function(e){const t=e.values.last_price;return{quotes:{...r.current.detailsInfoProps.quotes,last_price:t}}}`
	assetPath := "/charting_library/bundles/" + detailsQuotesPatch.Bundle
	for _, encoding := range []string{"identity", "GZip"} {
		for _, disk := range []bool{false, true} {
			name := encoding + "/fresh"
			if disk {
				name = encoding + "/disk"
			}
			t.Run(name, func(t *testing.T) {
				header := make(http.Header)
				header.Set("Content-Type", "text/javascript")
				header.Set("ETag", `"upstream"`)
				header.Set("Digest", "upstream-digest")
				body := []byte(source)
				if encoding == "GZip" {
					var compressed bytes.Buffer
					zip := gzip.NewWriter(&compressed)
					_, _ = zip.Write(body)
					_ = zip.Close()
					body = compressed.Bytes()
					header.Set("Content-Encoding", encoding)
				}
				var hits atomic.Int32
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					hits.Add(1)
					if r.URL.Path != assetPath {
						t.Errorf("versioned request leaked upstream: %s", r.URL.Path)
					}
					for key, values := range header {
						w.Header()[key] = values
					}
					_, _ = w.Write(body)
				}))
				defer upstream.Close()
				directory := t.TempDir()
				proxy := newTVProxy(upstream.URL, directory)
				transport := proxy.Transport.(*chartLibraryTransport)
				cacheEncoding := strings.ToLower(encoding)
				if disk {
					if err := writeChartCacheFile(directory, assetPath, cacheEncoding, &chartCacheEntry{header: header, body: body}); err != nil {
						t.Fatal(err)
					}
					if err := transport.loadDisk(); err != nil {
						t.Fatal(err)
					}
				}
				get := func(version, etag string) *httptest.ResponseRecorder {
					req := httptest.NewRequest(http.MethodGet, strings.Replace(assetPath, "/charting_library/", "/charting_library/"+version+"/", 1), nil)
					req.Header.Set("Accept-Encoding", cacheEncoding)
					req.Header.Set("If-None-Match", etag)
					req.Header.Set("Range", "bytes=0-")
					req.Header.Set("If-Range", `"upstream"`)
					response := httptest.NewRecorder()
					proxy.ServeHTTP(response, req)
					return response
				}
				response := get("quotes-v2", `"upstream"`)
				if response.Code != http.StatusOK {
					t.Fatalf("old upstream ETag must not validate patched body: %d", response.Code)
				}
				decoded, err := decodeChartBody(response.Header(), response.Body.Bytes())
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"ntc", "nch", "nchp", "ntc_time"} {
					if !bytes.Contains(decoded, []byte(field+":e.values."+field)) {
						t.Fatalf("Details did not receive %s: %s", field, decoded)
					}
				}
				if response.Header().Get("Digest") != "" || response.Header().Get("ETag") == `"upstream"` || response.Result().ContentLength != int64(response.Body.Len()) {
					t.Fatalf("patched representation headers: %v", response.Header())
				}
				legacy := get("quotes-v1", "")
				if legacy.Code != http.StatusOK || !bytes.Equal(legacy.Body.Bytes(), response.Body.Bytes()) {
					t.Fatalf("legacy library path: %d %s", legacy.Code, legacy.Body)
				}
				cached := get("quotes-v2", response.Header().Get("ETag"))
				if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
					t.Fatalf("patched ETag did not revalidate: %d %s", cached.Code, cached.Body)
				}
				transport.mu.Lock()
				original := transport.cache[assetPath][cacheEncoding]
				transport.mu.Unlock()
				if !bytes.Equal(original.body, body) || original.header.Get("ETag") != `"upstream"` {
					t.Fatal("compat patch changed the upstream cache")
				}
				wantHits := int32(1)
				if disk {
					wantHits = 0
				}
				if hits.Load() != wantHits {
					t.Fatalf("upstream hits=%d, want %d", hits.Load(), wantHits)
				}
			})
		}
	}
}

func TestTVNightQuoteCompatLeavesUnknownBundlesUntouched(t *testing.T) {
	for _, assetPath := range []string{
		"/charting_library/bundles/symbol-details.future12345678.js",
		"/charting_library/bundles/other.c9b8b9183b542f763ac4.js",
	} {
		header := make(http.Header)
		header.Set("ETag", `"original"`)
		body := []byte(detailsQuotesPatch.From)
		got, err := patchTVDetailsQuotes(assetPath, header, body)
		if err != nil || !bytes.Equal(got, body) || header.Get("ETag") != `"original"` {
			t.Fatalf("unrelated bundle changed: path=%s err=%v body=%s", assetPath, err, got)
		}
	}
	// An upstream hotfix or an unexpected shape must also pass through.
	header := make(http.Header)
	for _, source := range []string{"already-fixed", detailsQuotesPatch.From + detailsQuotesPatch.From} {
		got, err := patchTVDetailsQuotes("/charting_library/bundles/"+detailsQuotesPatch.Bundle, header, []byte(source))
		if err != nil || string(got) != source {
			t.Fatalf("unexpected source changed: %v %s", err, got)
		}
	}
}
