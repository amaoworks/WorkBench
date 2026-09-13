package modules

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"workbench/internal/contracts"
)

const (
	maxControlBody = 1 << 20
	authHeader     = "Authorization"
)

func (r *Registry) fetchManifest(ctx context.Context, origin, token string, allowNonLocal bool) ([]byte, error) {
	status, body, err := r.controlRequest(ctx, origin, token, allowNonLocal, http.MethodGet, "/_workbench/manifest", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, apiError(400, "connection_failed", "无法读取模块描述")
	}
	return body, nil
}

func (r *Registry) controlJSON(ctx context.Context, rec externalRecord, method, path string, payload []byte) (json.RawMessage, error) {
	status, body, err := r.controlRequest(ctx, rec.BaseURL, rec.ServiceToken, rec.AllowNonLocal, method, path, payload)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		code := "upstream_error"
		message := "外部模块请求失败"
		var parsed struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &parsed) == nil {
			if parsed.Code != "" {
				code = parsed.Code
			}
			if parsed.Message != "" {
				message = parsed.Message
			}
		}
		return nil, apiError(status, code, message)
	}
	if len(body) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(body) {
		return nil, apiError(502, "upstream_error", "外部模块返回了无效 JSON")
	}
	return json.RawMessage(body), nil
}

func (r *Registry) putState(ctx context.Context, rec externalRecord) (contracts.ExternalStatus, error) {
	payload, _ := json.Marshal(contracts.ExternalControlState{
		RegistrationID: rec.RegistrationID,
		Generation:     rec.Generation,
		Enabled:        rec.Enabled,
	})
	status, body, err := r.controlRequest(ctx, rec.BaseURL, rec.ServiceToken, rec.AllowNonLocal, http.MethodPut, "/_workbench/state", payload)
	if err != nil {
		return contracts.ExternalStatus{}, err
	}
	if status < 200 || status >= 300 {
		return contracts.ExternalStatus{}, apiError(status, "upstream_error", "无法应用模块状态")
	}
	return parseStatus(body)
}

func (r *Registry) getStatus(ctx context.Context, rec externalRecord) (contracts.ExternalStatus, error) {
	status, body, err := r.controlRequest(ctx, rec.BaseURL, rec.ServiceToken, rec.AllowNonLocal, http.MethodGet, "/_workbench/status", nil)
	if err != nil {
		return contracts.ExternalStatus{}, err
	}
	if status != http.StatusOK {
		return contracts.ExternalStatus{}, apiError(status, "upstream_error", "无法读取模块状态")
	}
	return parseStatus(body)
}

func parseStatus(body []byte) (contracts.ExternalStatus, error) {
	var status contracts.ExternalStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return contracts.ExternalStatus{}, apiError(502, "upstream_error", "外部模块返回了无效状态")
	}
	switch status.Health {
	case "", contracts.HealthUnknown, contracts.HealthReady, contracts.HealthDegraded, contracts.HealthOffline, contracts.HealthIncompatible:
	default:
		status.Health = contracts.HealthUnknown
	}
	return status, nil
}

func (r *Registry) controlRequest(ctx context.Context, origin, token string, allowNonLocal bool, method, path string, payload []byte) (int, []byte, error) {
	if r.closed.Load() {
		return 0, nil, apiError(503, "shutting_down", "工作台正在关闭")
	}
	timeouts := r.currentTimeouts()
	reqCtx, cancel := context.WithTimeout(ctx, timeouts.Control)
	defer cancel()
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, strings.TrimRight(origin, "/")+path, reader)
	if err != nil {
		return 0, nil, apiError(400, "invalid_connection", "无法连接外部模块")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(authHeader, "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := r.httpClient(allowNonLocal, timeouts.Control)
	res, err := client.Do(req)
	if err != nil {
		if isNonLocal(err) {
			return 0, nil, apiError(400, "non_local_denied", "未允许连接非本机地址")
		}
		return 0, nil, apiError(400, "connection_failed", "无法连接外部模块")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxControlBody+1))
	if err != nil {
		return 0, nil, apiError(502, "upstream_error", "无法读取外部模块响应")
	}
	if len(body) > maxControlBody {
		return 0, nil, apiError(502, "upstream_error", "外部模块响应过大")
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return res.StatusCode, body, apiError(400, "invalid_service_token", "服务凭据无效")
	}
	return res.StatusCode, body, nil
}

func (r *Registry) httpClient(allowNonLocal bool, timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = r.currentTimeouts().Control
	}
	if r.client != nil {
		clone := *r.client
		if clone.Timeout == 0 {
			clone.Timeout = timeout
		}
		if clone.CheckRedirect == nil {
			clone.CheckRedirect = ignoreRedirects
		}
		return &clone
	}
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: ignoreRedirects,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           restrictedDialer(allowNonLocal, timeout),
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			DisableKeepAlives:     true,
		},
	}
}

func ignoreRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

func restrictedDialer(allowNonLocal bool, timeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if !allowNonLocal {
			if err := assertLoopbackHost(ctx, host); err != nil {
				return nil, err
			}
		}
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		if !allowNonLocal {
			if tcp, ok := conn.RemoteAddr().(*net.TCPAddr); ok && !tcp.IP.IsLoopback() {
				_ = conn.Close()
				return nil, errNonLocal
			}
		}
		return conn, nil
	}
}

var errNonLocal = apiError(400, "non_local_denied", "未允许连接非本机地址")

func assertLoopbackHost(ctx context.Context, host string) error {
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return nil
		}
		return errNonLocal
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve host: %w", err)
	}
	if len(ips) == 0 {
		return errNonLocal
	}
	for _, ip := range ips {
		if !ip.IP.IsLoopback() {
			return errNonLocal
		}
	}
	return nil
}

func isNonLocal(err error) bool {
	var apiErr *Error
	return errors.As(err, &apiErr) && apiErr.Code == "non_local_denied"
}
