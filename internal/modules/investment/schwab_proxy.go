package investment

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"workbench/internal/foundation/httpapi"
)

const maxSchwabProxyBody = 1 << 20

func (m *Module) proxySchwab(apiPrefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := chi.URLParam(r, "*")
		if !validProxyPath(rest) {
			httpapi.Error(w, http.StatusBadRequest, "invalid_request", "无效的 Schwab 路径")
			return
		}
		token, err := m.ensureAccessToken(r.Context())
		if err != nil {
			httpapi.Error(w, http.StatusConflict, "schwab_disconnected", err.Error())
			return
		}
		target := strings.TrimRight(m.schwabAPI, "/") + apiPrefix + "/" + rest
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		var body io.Reader = http.MaxBytesReader(w, r.Body, maxSchwabProxyBody)
		if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
			body = http.NoBody
		}
		req, err := http.NewRequestWithContext(r.Context(), r.Method, target, body)
		if err != nil {
			httpapi.Error(w, http.StatusBadGateway, "schwab_proxy_failed", "无法创建 Schwab 请求")
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		if accept := r.Header.Get("Accept"); accept != "" {
			req.Header.Set("Accept", accept)
		} else {
			req.Header.Set("Accept", "application/json")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := m.httpClient.Do(req)
		if err != nil {
			httpapi.Error(w, http.StatusBadGateway, "schwab_proxy_failed", "请求 Schwab 失败")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			// Refresh the connection state, but never replay an order or other request.
			if err := m.refreshRejectedAccessToken(r.Context(), token); err != nil {
				code := "schwab_refresh_failed"
				if errors.Is(err, errSchwabReauthorizationRequired) {
					code = "schwab_reauthorization_required"
				}
				httpapi.Error(w, http.StatusConflict, code, err.Error())
				return
			}
		}
		copySchwabHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, io.LimitReader(resp.Body, 16<<20))
	}
}

func copySchwabHeaders(dst, src http.Header) {
	for key, values := range src {
		switch strings.ToLower(key) {
		case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailers", "transfer-encoding", "upgrade", "set-cookie":
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
