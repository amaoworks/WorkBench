package investment

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const chartWarmAccept = "gzip"
const chartWarmWorkers = 4

var bundlePath = regexp.MustCompile(`bundles/([A-Za-z0-9._-]+\.(?:js|css))`)

// WarmChartLibrary loads any bundles saved from an earlier process, then fills
// gaps from TradingView before the browser walks the same dependency chain.
func (m *Module) WarmChartLibrary(ctx context.Context) {
	go func() {
		transport, ok := m.tvProxy.Transport.(*chartLibraryTransport)
		if ok {
			if err := transport.loadDisk(); err != nil && m.logger != nil {
				m.logger.Warn("chart library cache load failed", "component", "investment", "error", err)
			}
		}
		started := time.Now()
		files, err := warmChartLibrary(ctx, m.tvProxy)
		if m.logger == nil || ctx.Err() != nil {
			return
		}
		attrs := []any{"component", "investment", "files", files, "durationMs", time.Since(started).Milliseconds()}
		if err != nil {
			m.logger.Warn("chart library cache warm incomplete", append(attrs, "error", err)...)
			return
		}
		m.logger.Info("chart library cache warmed", attrs...)
	}()
}

func warmChartLibrary(ctx context.Context, proxy *httputil.ReverseProxy) (int, error) {
	type result struct {
		path string
		body []byte
		err  error
	}
	seen := map[string]struct{}{}
	pending := []string{"/charting_library/charting_library.esm.js", "/charting_library/sameorigin.html"}
	for _, path := range pending {
		seen[path] = struct{}{}
	}
	files := 0
	var firstErr error
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return files, err
		}
		batch := pending
		pending = nil
		jobs := make(chan string)
		results := make(chan result, len(batch))
		var group sync.WaitGroup
		count := chartWarmWorkers
		if count > len(batch) {
			count = len(batch)
		}
		for i := 0; i < count; i++ {
			group.Add(1)
			go func() {
				defer group.Done()
				for path := range jobs {
					body, err := fetchChart(ctx, proxy, path)
					results <- result{path: path, body: body, err: err}
				}
			}()
		}
		go func() {
			for _, path := range batch {
				jobs <- path
			}
			close(jobs)
			group.Wait()
			close(results)
		}()
		for item := range results {
			if item.err != nil {
				if firstErr == nil && ctx.Err() == nil {
					firstErr = item.err
				}
				continue
			}
			if hashedChartBundle.MatchString(item.path) {
				files++
			}
			// The loader lists the resources needed to paint the first chart.
			// Runtime chunk maps include hundreds of optional tools; fetching all
			// of them competes with foreground loading and grows the disk cache.
			if !chartEntryPoint(item.path) {
				continue
			}
			for _, next := range discoverChartBundles(item.body) {
				full := "/charting_library/" + next
				if !hashedChartBundle.MatchString(full) {
					continue
				}
				if _, ok := seen[full]; ok {
					continue
				}
				seen[full] = struct{}{}
				pending = append(pending, full)
			}
		}
	}
	return files, firstErr
}

func fetchChart(ctx context.Context, proxy *httputil.ReverseProxy, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", chartWarmAccept)
	recorder := &chartCapture{header: make(http.Header)}
	proxy.ServeHTTP(recorder, req)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if recorder.status != http.StatusOK {
		return nil, fmt.Errorf("chart prefetch %s: HTTP %d", path, recorder.status)
	}
	if !chartEntryPoint(path) {
		return nil, nil
	}
	return decodeChartBody(recorder.header, recorder.body.Bytes())
}

type chartCapture struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (c *chartCapture) Header() http.Header { return c.header }
func (c *chartCapture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}
func (c *chartCapture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	return c.body.Write(p)
}

func decodeChartBody(header http.Header, body []byte) ([]byte, error) {
	if !strings.EqualFold(header.Get("Content-Encoding"), "gzip") {
		return body, nil
	}
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(io.LimitReader(reader, chartMaxAssetBytes))
}

func discoverChartBundles(source []byte) []string {
	if len(source) == 0 {
		return nil
	}
	text := string(source)
	found := map[string]struct{}{}
	for _, match := range bundlePath.FindAllStringSubmatch(text, -1) {
		name := strings.ReplaceAll(match[1], "__LANG__", "zh")
		found["bundles/"+name] = struct{}{}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	return names
}

type chartDiskMeta struct {
	Path     string              `json:"path"`
	Encoding string              `json:"encoding"`
	Header   map[string][]string `json:"header"`
}

func writeChartCacheFile(dir, path, encoding string, entry *chartCacheEntry) error {
	if !hashedChartBundle.MatchString(path) || entry == nil {
		return nil
	}
	name := chartCacheName(path, encoding)
	target := filepath.Join(dir, name)
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	meta := chartDiskMeta{Path: path, Encoding: encoding, Header: entry.header}
	payload, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if err := writeChartCacheAtomic(filepath.Join(target, "body"), entry.body); err != nil {
		return err
	}
	return writeChartCacheAtomic(filepath.Join(target, "meta.json"), payload)
}

func (t *chartLibraryTransport) loadDisk() error {
	if t == nil || t.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(t.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		base := filepath.Join(t.dir, entry.Name())
		metaBytes, err := os.ReadFile(filepath.Join(base, "meta.json"))
		if err != nil {
			continue
		}
		var meta chartDiskMeta
		if err := json.Unmarshal(metaBytes, &meta); err != nil || !hashedChartBundle.MatchString(meta.Path) || meta.Encoding == "" {
			continue
		}
		body, err := os.ReadFile(filepath.Join(base, "body"))
		if err != nil {
			continue
		}
		t.storeEntry(meta.Path, meta.Encoding, &chartCacheEntry{header: meta.Header, body: body})
	}
	return nil
}

func (t *chartLibraryTransport) storeEntry(path, encoding string, entry *chartCacheEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cache == nil {
		t.cache = map[string]map[string]*chartCacheEntry{}
	}
	if t.cache[path] == nil {
		t.cache[path] = map[string]*chartCacheEntry{}
	}
	t.cache[path][encoding] = entry
}

func chartCacheName(path, encoding string) string {
	sum := sha256.Sum256([]byte(path + "\n" + encoding))
	return hex.EncodeToString(sum[:])
}

func writeChartCacheAtomic(path string, body []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".chart-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
