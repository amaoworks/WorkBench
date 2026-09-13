package modules

import (
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
)

type proxyKind int

const (
	proxyUI proxyKind = iota
	proxySettings
	proxyAPI
)

type socketSet struct {
	mu         sync.Mutex
	members    map[contracts.ModuleID]map[*websocket.Conn]struct{}
	generation map[contracts.ModuleID]uint64
}

func newSocketSet() *socketSet {
	return &socketSet{
		members:    make(map[contracts.ModuleID]map[*websocket.Conn]struct{}),
		generation: make(map[contracts.ModuleID]uint64),
	}
}

func (s *socketSet) current(id contracts.ModuleID) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation[id]
}

func (s *socketSet) addIfCurrent(id contracts.ModuleID, conn *websocket.Conn, gen uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation[id] != gen {
		return false
	}
	set, ok := s.members[id]
	if !ok {
		set = make(map[*websocket.Conn]struct{})
		s.members[id] = set
	}
	set[conn] = struct{}{}
	return true
}

func (s *socketSet) remove(id contracts.ModuleID, conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if set, ok := s.members[id]; ok {
		delete(set, conn)
		if len(set) == 0 {
			delete(s.members, id)
		}
	}
}

func (s *socketSet) closeModule(id contracts.ModuleID) {
	s.mu.Lock()
	s.generation[id]++
	set := s.members[id]
	delete(s.members, id)
	s.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for conn := range set {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "module disabled"), deadline)
		_ = conn.Close()
	}
}

func (s *socketSet) closeAll() {
	s.mu.Lock()
	all := s.members
	s.members = make(map[contracts.ModuleID]map[*websocket.Conn]struct{})
	for id := range s.generation {
		s.generation[id]++
	}
	s.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for _, set := range all {
		for conn := range set {
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "workbench stopping"), deadline)
			_ = conn.Close()
		}
	}
}

type proxyTransports struct {
	mu    sync.Mutex
	items map[string]*http.Transport
}

func newProxyTransports() *proxyTransports {
	return &proxyTransports{items: make(map[string]*http.Transport)}
}

func (p *proxyTransports) get(rec externalRecord, timeout time.Duration) *http.Transport {
	key := rec.BaseURL + "|"
	if rec.AllowNonLocal {
		key += "1"
	} else {
		key += "0"
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if transport, ok := p.items[key]; ok {
		return transport
	}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         restrictedDialer(rec.AllowNonLocal, timeout),
		TLSHandshakeTimeout: timeout,
		ForceAttemptHTTP2:   false,
		IdleConnTimeout:     30 * time.Second,
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 2,
	}
	p.items[key] = transport
	return transport
}

func (p *proxyTransports) drop(baseURL string) {
	if p == nil {
		return
	}
	prefix := baseURL + "|"
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, transport := range p.items {
		if strings.HasPrefix(key, prefix) {
			transport.CloseIdleConnections()
			delete(p.items, key)
		}
	}
}

func (p *proxyTransports) closeAll() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, transport := range p.items {
		transport.CloseIdleConnections()
	}
	p.items = make(map[string]*http.Transport)
}

func (r *Registry) UIHandler() http.Handler       { return r.proxyHandler(proxyUI) }
func (r *Registry) SettingsHandler() http.Handler { return r.proxyHandler(proxySettings) }
func (r *Registry) APIProxyHandler() http.Handler { return r.proxyHandler(proxyAPI) }

func (r *Registry) proxyHandler(kind proxyKind) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		id := contracts.ModuleID(chi.URLParam(req, "id"))
		if id == "" {
			id = contracts.ModuleID(req.PathValue("id"))
		}
		rec, ok := r.snapshotExternal(id)
		if !ok {
			httpapi.Error(w, http.StatusNotFound, "module_not_found", "模块不存在")
			return
		}
		if kind == proxySettings {
			if req.Method != http.MethodGet && req.Method != http.MethodHead {
				httpapi.Error(w, http.StatusMethodNotAllowed, "method_not_allowed", "设置资源仅允许 GET 或 HEAD")
				return
			}
		} else if !rec.businessAllowed() {
			httpapi.Error(w, http.StatusServiceUnavailable, "module_disabled", "module is disabled")
			return
		}
		rest := chi.URLParam(req, "*")
		if rest == "" {
			rest = req.PathValue("*")
		}
		remotePath, err := boundRemotePath(kind, rest)
		if err != nil {
			httpapi.Error(w, http.StatusForbidden, "path_forbidden", "不允许的代理路径")
			return
		}
		if kind == proxyAPI && websocket.IsWebSocketUpgrade(req) {
			r.proxyWebSocket(w, req, rec, remotePath)
			return
		}
		r.forwardHTTP(w, req, rec, remotePath, kind)
	})
}

func boundRemotePath(kind proxyKind, rest string) (string, error) {
	rest = strings.TrimPrefix(rest, "/")
	base := "/api/"
	switch kind {
	case proxyUI:
		base = "/ui/"
	case proxySettings:
		base = "/settings/"
	}
	joined := path.Clean(base + rest)
	if rest == "" {
		joined = path.Clean(base)
		if kind == proxyAPI {
			joined = "/api"
		}
	}
	prefix := strings.TrimSuffix(base, "/")
	if joined != prefix && !strings.HasPrefix(joined, prefix+"/") {
		return "", errForbiddenPath
	}
	if joined == "/_workbench" || strings.HasPrefix(joined, "/_workbench/") {
		return "", errForbiddenPath
	}
	if strings.Contains(joined, "/_workbench/") || strings.HasSuffix(joined, "/_workbench") {
		return "", errForbiddenPath
	}
	return joined, nil
}

var errForbiddenPath = apiError(403, "path_forbidden", "不允许的代理路径")

func (r *Registry) forwardHTTP(w http.ResponseWriter, req *http.Request, rec externalRecord, remotePath string, kind proxyKind) {
	target, err := url.Parse(rec.BaseURL)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "upstream_unavailable", "external module is unreachable")
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.FlushInterval = -1
	proxy.Transport = r.transports.get(rec, r.currentTimeouts().Control)
	proxy.ErrorHandler = func(rw http.ResponseWriter, _ *http.Request, _ error) {
		httpapi.Error(rw, http.StatusBadGateway, "upstream_unavailable", "external module is unreachable")
	}
	proxy.Director = func(out *http.Request) {
		out.URL.Scheme = target.Scheme
		out.URL.Host = target.Host
		out.URL.Path = remotePath
		out.URL.RawPath = ""
		out.Host = target.Host
		out.RequestURI = ""
		stripBrowserHeaders(out)
		out.Header.Set(authHeader, "Bearer "+rec.ServiceToken)
	}
	proxy.ModifyResponse = func(res *http.Response) error {
		res.Header.Del("Set-Cookie")
		rewriteLocation(res, rec, kind)
		return nil
	}
	proxy.ServeHTTP(w, req)
}

func stripBrowserHeaders(req *http.Request) {
	req.Header.Del("Cookie")
	req.Header.Del("Authorization")
	req.Header.Del("X-Forwarded-For")
	req.Header.Del("X-Forwarded-Host")
	req.Header.Del("X-Forwarded-Proto")
	req.Header.Del("X-Real-IP")
	req.Header.Del("Forwarded")
	req.Header.Del("X-Workbench-Service-Token")
}

func rewriteLocation(res *http.Response, rec externalRecord, kind proxyKind) {
	location := res.Header.Get("Location")
	if location == "" {
		return
	}
	parsed, err := url.Parse(location)
	if err != nil {
		res.Header.Del("Location")
		return
	}
	target, _ := url.Parse(rec.BaseURL)
	if parsed.IsAbs() {
		if parsed.Scheme != target.Scheme || !strings.EqualFold(parsed.Host, target.Host) {
			res.Header.Del("Location")
			return
		}
	}
	remotePath := parsed.Path
	if remotePath == "" {
		remotePath = "/"
	}
	cleaned := path.Clean(remotePath)
	var hostPrefix string
	switch {
	case strings.HasPrefix(cleaned, "/ui/") || cleaned == "/ui":
		hostPrefix = "/modules/" + string(rec.ID) + "/ui"
		cleaned = strings.TrimPrefix(cleaned, "/ui")
	case strings.HasPrefix(cleaned, "/settings/") || cleaned == "/settings":
		if kind != proxySettings && kind != proxyUI {
			res.Header.Del("Location")
			return
		}
		hostPrefix = "/modules/" + string(rec.ID) + "/settings"
		cleaned = strings.TrimPrefix(cleaned, "/settings")
	case strings.HasPrefix(cleaned, "/api/") || cleaned == "/api":
		hostPrefix = "/api/modules/" + string(rec.ID) + "/proxy"
		cleaned = strings.TrimPrefix(cleaned, "/api")
	default:
		res.Header.Del("Location")
		return
	}
	if strings.Contains(cleaned, "/_workbench") {
		res.Header.Del("Location")
		return
	}
	rewritten := hostPrefix + cleaned
	if parsed.RawQuery != "" {
		rewritten += "?" + parsed.RawQuery
	}
	res.Header.Set("Location", rewritten)
}

var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(req *http.Request) bool {
		origin := req.Header.Get("Origin")
		if origin == "" {
			return true
		}
		parsed, err := url.Parse(origin)
		return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && strings.EqualFold(parsed.Host, req.Host)
	},
}

// Caller holds r.mu while validating the record and capturing/registering the
// socket generation, so a request cannot combine an old connection with new state.
func (r *Registry) webSocketSnapshotCurrent(rec externalRecord) bool {
	current, ok := r.external[rec.ID]
	return !r.closed.Load() && ok && current.rec.businessAllowed() &&
		current.rec.RegistrationID == rec.RegistrationID &&
		current.rec.ConnectionRevision == rec.ConnectionRevision &&
		current.rec.Epoch == rec.Epoch && current.rec.Generation == rec.Generation
}

func (r *Registry) proxyWebSocket(w http.ResponseWriter, req *http.Request, rec externalRecord, remotePath string) {
	r.mu.RLock()
	gen := r.sockets.current(rec.ID)
	valid := r.webSocketSnapshotCurrent(rec)
	r.mu.RUnlock()
	if !valid {
		httpapi.Error(w, http.StatusServiceUnavailable, "module_disabled", "module is disabled")
		return
	}
	target, err := url.Parse(rec.BaseURL)
	if err != nil {
		httpapi.Error(w, http.StatusBadGateway, "upstream_unavailable", "external module is unreachable")
		return
	}
	scheme := "ws"
	if target.Scheme == "https" {
		scheme = "wss"
	}
	controlTimeout := r.currentTimeouts().Control
	dialer := websocket.Dialer{
		NetDialContext:   restrictedDialer(rec.AllowNonLocal, controlTimeout),
		Proxy:            nil,
		HandshakeTimeout: controlTimeout,
	}
	headers := http.Header{}
	headers.Set(authHeader, "Bearer "+rec.ServiceToken)
	if origin := req.Header.Get("Origin"); origin != "" {
		headers.Set("Origin", rec.BaseURL)
	}
	upstream, resp, err := dialer.DialContext(req.Context(), scheme+"://"+target.Host+remotePath+"?"+req.URL.RawQuery, headers)
	if err != nil {
		if resp != nil {
			defer resp.Body.Close()
		}
		httpapi.Error(w, http.StatusBadGateway, "upstream_unavailable", "external module is unreachable")
		return
	}
	r.mu.RLock()
	valid = r.webSocketSnapshotCurrent(rec) && r.sockets.current(rec.ID) == gen
	r.mu.RUnlock()
	if !valid {
		_ = upstream.Close()
		httpapi.Error(w, http.StatusServiceUnavailable, "module_disabled", "module is disabled")
		return
	}
	client, err := wsUpgrader.Upgrade(w, req, nil)
	if err != nil {
		_ = upstream.Close()
		return
	}
	r.mu.RLock()
	registered := r.webSocketSnapshotCurrent(rec) && r.sockets.addIfCurrent(rec.ID, client, gen)
	r.mu.RUnlock()
	if !registered {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	defer r.sockets.remove(rec.ID, client)
	defer client.Close()
	defer upstream.Close()

	errc := make(chan struct{}, 2)
	copyWS := func(dst, src *websocket.Conn) {
		defer func() { errc <- struct{}{} }()
		for {
			messageType, reader, err := src.NextReader()
			if err != nil {
				return
			}
			writer, err := dst.NextWriter(messageType)
			if err != nil {
				return
			}
			if _, err := io.Copy(writer, reader); err != nil {
				_ = writer.Close()
				return
			}
			if err := writer.Close(); err != nil {
				return
			}
		}
	}
	go copyWS(client, upstream)
	go copyWS(upstream, client)
	select {
	case <-errc:
	case <-r.bgCtx.Done():
	}
}

func (r *Registry) ActiveProxySockets(id contracts.ModuleID) int {
	r.sockets.mu.Lock()
	defer r.sockets.mu.Unlock()
	return len(r.sockets.members[id])
}
