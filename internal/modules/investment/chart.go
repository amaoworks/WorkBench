package investment

import (
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
)

func newTVProxy(origin string) *httputil.ReverseProxy {
	target, err := url.Parse(origin)
	if err != nil {
		target, _ = url.Parse(defaultTVOrigin)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(req *http.Request) {
		director(req)
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host
		req.Header.Del("Cookie")
		req.Header.Del("Authorization")
		req.Header.Del("X-CSRF-Token")
		req.Header.Set("Accept-Encoding", "identity")
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
	payload, err := fs.ReadFile(chartAssets, path.Join("chart", name))
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
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
