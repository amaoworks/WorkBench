package investment

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var hashedChartBundle = regexp.MustCompile(`(?i)/bundles/[^/]*\.[0-9a-f]{8,}\.(?:js|css)$`)

const chartFetchTimeout = 30 * time.Second
const chartEntryTTL = 5 * time.Minute
const chartMaxAssetBytes = 32 << 20

type chartCacheEntry struct {
	header  http.Header
	body    []byte
	expires time.Time // Zero for immutable, content-hashed bundles.
}

type chartFlight struct {
	done   chan struct{}
	entry  *chartCacheEntry
	status int
	err    error
}

type chartLibraryTransport struct {
	base    http.RoundTripper
	dir     string
	mu      sync.Mutex
	cache   map[string]map[string]*chartCacheEntry
	flights map[string]*chartFlight
}

func chartEntryPoint(path string) bool {
	return path == "/charting_library/charting_library.esm.js" || path == "/charting_library/sameorigin.html"
}

func (t *chartLibraryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	if req.Method != http.MethodGet || req.Header.Get("Range") != "" || (!hashedChartBundle.MatchString(req.URL.Path) && !chartEntryPoint(req.URL.Path)) {
		return t.base.RoundTrip(req)
	}
	encoding := chartRequestEncoding(req.Header.Get("Accept-Encoding"))
	if encoding == "" {
		return t.base.RoundTrip(req)
	}
	key := req.URL.Path + "\n" + encoding
	t.mu.Lock()
	if entry := t.cache[req.URL.Path][encoding]; entry != nil && (entry.expires.IsZero() || time.Now().Before(entry.expires)) {
		t.mu.Unlock()
		return entry.response(req, http.StatusOK), nil
	}
	flight := t.flights[key]
	if flight == nil {
		flight = &chartFlight{done: make(chan struct{})}
		if t.flights == nil {
			t.flights = make(map[string]*chartFlight)
		}
		t.flights[key] = flight
		// The fetch belongs to the cache. Leaving an iframe must not cancel a
		// request shared with its replacement or the startup prefetch.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(req.Context()), chartFetchTimeout)
		upstream := req.Clone(ctx)
		upstream.Header.Set("Accept-Encoding", encoding)
		upstream.Header.Del("If-None-Match")
		upstream.Header.Del("If-Modified-Since")
		go func() {
			defer cancel()
			flight.entry, flight.status, flight.err = t.fetch(upstream, encoding)
			t.mu.Lock()
			delete(t.flights, key)
			close(flight.done)
			t.mu.Unlock()
		}()
	}
	t.mu.Unlock()
	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	case <-flight.done:
		if flight.err != nil {
			return nil, flight.err
		}
		return flight.entry.response(req, flight.status), nil
	}
}

// Only request encodings the cache and prefetch reader can both consume. Sending
// a browser's br/zstd list upstream but only looking up gzip defeats the cache.
func chartRequestEncoding(accept string) string {
	qualities := make(map[string]float64)
	for _, item := range strings.Split(strings.ToLower(accept), ",") {
		parts := strings.Split(item, ";")
		quality := 1.0
		for _, parameter := range parts[1:] {
			if name, value, ok := strings.Cut(strings.TrimSpace(parameter), "="); ok && name == "q" {
				quality, _ = strconv.ParseFloat(value, 64)
			}
		}
		qualities[strings.TrimSpace(parts[0])] = quality
	}
	gzipQuality, specified := qualities["gzip"]
	if !specified {
		gzipQuality = qualities["*"]
	}
	if gzipQuality > 0 {
		return "gzip"
	}
	identityQuality, specified := qualities["identity"]
	if specified && identityQuality <= 0 {
		return ""
	}
	if wildcard, ok := qualities["*"]; !specified && ok && wildcard <= 0 {
		return ""
	}
	return "identity"
}

func (t *chartLibraryTransport) fetch(req *http.Request, encoding string) (*chartCacheEntry, int, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, chartMaxAssetBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(body) > chartMaxAssetBytes {
		return nil, 0, fmt.Errorf("chart asset exceeds %d bytes", chartMaxAssetBytes)
	}
	entry := &chartCacheEntry{header: resp.Header.Clone(), body: body}
	actualEncoding := strings.ToLower(resp.Header.Get("Content-Encoding"))
	if actualEncoding != "" && actualEncoding != "identity" && actualEncoding != encoding {
		return nil, 0, fmt.Errorf("unexpected chart encoding %q", actualEncoding)
	}
	if resp.StatusCode == http.StatusOK {
		if chartEntryPoint(req.URL.Path) {
			entry.expires = time.Now().Add(chartEntryFreshness(resp.Header))
		}
		if !strings.Contains(resp.Header.Get("Cache-Control"), "no-store") {
			t.storeEntry(req.URL.Path, encoding, entry)
			if t.dir != "" && hashedChartBundle.MatchString(req.URL.Path) {
				_ = writeChartCacheFile(t.dir, req.URL.Path, encoding, entry)
			}
		}
	}
	return entry, resp.StatusCode, nil
}

// Bound the lifetime of stable filenames so upgrades appear without putting an
// external round trip on every chart opening. Respect a shorter upstream TTL.
func chartEntryFreshness(header http.Header) time.Duration {
	ttl := chartEntryTTL
	age, _ := strconv.Atoi(header.Get("Age"))
	for _, part := range strings.Split(header.Get("Cache-Control"), ",") {
		directive := strings.TrimSpace(part)
		if directive == "no-cache" || directive == "no-store" {
			return 0
		}
		if strings.HasPrefix(directive, "max-age=") {
			seconds, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(directive, "max-age="), `"`))
			if err == nil {
				ttl = min(ttl, time.Duration(max(0, seconds-age))*time.Second)
			}
		}
	}
	return ttl
}

func (e *chartCacheEntry) response(req *http.Request, status int) *http.Response {
	header := e.header.Clone()
	body := e.body
	if status == http.StatusOK && chartEntryPoint(req.URL.Path) && !strings.Contains(header.Get("Cache-Control"), "no-store") {
		// Stable filenames must also revalidate in the browser; an upstream
		// one-hour max-age would otherwise outlive our bounded server cache.
		header.Set("Cache-Control", "no-cache")
	}
	if status == http.StatusOK && ifNoneMatch(req.Header.Get("If-None-Match"), header.Get("ETag")) {
		status = http.StatusNotModified
		body = nil
		header.Del("Content-Encoding")
		header.Del("Content-Length")
	} else {
		header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	return &http.Response{
		Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), StatusCode: status,
		Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)),
		Request: req, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
	}
}

func ifNoneMatch(header, etag string) bool {
	if header == "" || etag == "" {
		return false
	}
	for _, part := range strings.Split(header, ",") {
		candidate := strings.TrimSpace(part)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}
