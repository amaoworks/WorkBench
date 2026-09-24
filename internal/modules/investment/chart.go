package investment

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

func newTVProxy(origin, cacheDir string) *httputil.ReverseProxy {
	target, err := url.Parse(origin)
	if err != nil {
		target, _ = url.Parse(defaultTVOrigin)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	upstream := http.DefaultTransport.(*http.Transport).Clone()
	upstream.MaxIdleConnsPerHost = 16
	upstream.ResponseHeaderTimeout = 15 * time.Second
	proxy.Transport = &chartLibraryTransport{base: upstream, dir: cacheDir}
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		req.Header.Del("X-CSRF-Token")
		// Keep unsupported asset requests explicit; cacheable chart resources
		// negotiate a supported representation in chartLibraryTransport.
		if req.Header.Get("Accept-Encoding") == "" {
			req.Header.Set("Accept-Encoding", "identity")
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		resp.Header.Del("X-Frame-Options")
		resp.Header.Del("Content-Security-Policy")
		resp.Header.Del("Strict-Transport-Security")
		resp.Header.Del("Access-Control-Allow-Origin")
		resp.Header.Del("Set-Cookie")
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "charting library unavailable", http.StatusBadGateway)
	}
	return proxy
}

func (m *Module) proxyChartingLibrary(w http.ResponseWriter, r *http.Request) {
	rest := chi.URLParam(r, "*")
	if !validProxyPath(rest) && rest != "" {
		http.NotFound(w, r)
		return
	}
	cloned := r.Clone(r.Context())
	cloned.URL.Path = "/charting_library/" + rest
	cloned.URL.RawPath = ""
	cloned.RequestURI = ""
	cloned.Host = ""
	cloned.Header.Del("Cookie")
	cloned.Header.Del("Authorization")
	cloned.Header.Del("X-CSRF-Token")
	m.tvProxy.ServeHTTP(w, cloned)
}

func (m *Module) serveTerminal(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/investment/terminal")
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		name = "index.html"
	}
	name = path.Clean(name)
	if name == "." || strings.HasPrefix(name, "..") {
		http.NotFound(w, r)
		return
	}
	assets := m.terminalAssets
	if assets == nil {
		var err error
		assets, err = terminalChartAssets()
		if err != nil {
			http.Error(w, "terminal chart assets unavailable", http.StatusInternalServerError)
			return
		}
	}
	payload, err := fs.ReadFile(assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch path.Ext(name) {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
	case ".js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(payload)))
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", fmt.Sprintf(`"%x"`, sha256.Sum256(payload)))
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(payload))
}
