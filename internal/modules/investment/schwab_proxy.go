package investment

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
	"workbench/internal/foundation/identity"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

const maxSchwabProxyBody = 1 << 20

func (m *Module) proxySchwab(apiPrefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := chi.URLParam(r, "*")
		if _, err := m.requireUser(r.Context()); err != nil {
			httpapi.Error(w, 401, "authentication_required", "请登录")
			return
		}
		if !validProxyPath(rest) || !allowedSchwabEndpoint(apiPrefix, rest, r.Method) {
			httpapi.Error(w, http.StatusBadRequest, "invalid_request", "无效的 Schwab 路径")
			return
		}
		token, err := m.ensureAccessToken(r.Context())
		if err != nil {
			httpapi.Error(w, http.StatusConflict, "schwab_disconnected", err.Error())
			return
		}
		parts := strings.Split(rest, "/")
		if apiPrefix == "/trader/v1" && len(parts) > 1 && parts[0] == "accounts" && parts[1] != "accountNumbers" {
			identities, err := m.accountIdentities(r.Context(), token)
			if err != nil {
				httpapi.Error(w, 502, "schwab_accounts_failed", "无法校验账户")
				return
			}
			allowed := false
			for _, hash := range identities {
				if hash == parts[1] {
					allowed = true
					break
				}
			}
			if !allowed {
				httpapi.Error(w, 403, "schwab_account_forbidden", "账户不可用")
				return
			}
		}
		writing := r.Method != http.MethodGet
		auditID := ""
		if writing {
			auditID, err = identity.New()
			if err == nil {
				err = m.queries.CreateTradeAudit(r.Context(), investmentsqlc.CreateTradeAuditParams{ID: auditID, UserID: contracts.UserID(r.Context()), AccountHash: parts[1], Method: r.Method, Path: rest, CreatedAt: m.now().UTC().UnixMilli()})
			}
			if err != nil {
				httpapi.Error(w, 500, "trade_audit_failed", "无法记录交易请求")
				return
			}
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
			if writing {
				m.finishTradeAudit(r.Context(), auditID, "unknown", 0)
			}
			httpapi.Error(w, http.StatusBadGateway, "schwab_proxy_failed", "请求 Schwab 失败")
			return
		}
		defer resp.Body.Close()
		if writing {
			m.finishTradeAudit(r.Context(), auditID, "responded", resp.StatusCode)
		}
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

func (m *Module) finishTradeAudit(ctx context.Context, id, outcome string, status int) {
	// Preserve an indeterminate outcome even if the caller closed the tab.
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.queries.FinishTradeAudit(auditCtx, investmentsqlc.FinishTradeAuditParams{ID: id, UserID: contracts.UserID(ctx), Outcome: outcome, HttpStatus: int64(status), UpdatedAt: m.now().UTC().UnixMilli()}); err != nil {
		m.logger.Error("Schwab trade audit update failed", "audit_id", id, "user_id", contracts.UserID(ctx), "outcome", outcome, "status", status, "error", err)
	}
}

func allowedSchwabEndpoint(prefix, path, method string) bool {
	parts := strings.Split(path, "/")
	if prefix == "/marketdata/v1" {
		if method != http.MethodGet {
			return false
		}
		switch parts[0] {
		case "quotes", "pricehistory", "instruments", "chains", "expirationchain", "markets":
			return len(parts) == 1
		}
		return false
	}
	if prefix != "/trader/v1" || len(parts) == 0 || parts[0] != "accounts" {
		return false
	}
	if len(parts) == 1 || (len(parts) == 2 && parts[1] == "accountNumbers") {
		return method == http.MethodGet
	}
	if len(parts) == 2 {
		return method == http.MethodGet
	}
	switch parts[2] {
	case "orders":
		if len(parts) == 3 {
			return method == http.MethodGet || method == http.MethodPost
		}
		if len(parts) == 4 && parts[3] != "" {
			return method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
		}
	case "transactions":
		return method == http.MethodGet && len(parts) == 3
	}
	return false
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
