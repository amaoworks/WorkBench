package auth

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	dbsqlc "workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/httpapi"
)

func nativeTokenEndpoint(r *http.Request) bool {
	return r.Method == http.MethodPost && (r.URL.Path == "/api/auth/token" || r.URL.Path == "/api/auth/token/refresh")
}

func (s *Service) tokenSecurity(next, browser http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/auth/login" && len(r.Header.Values("Authorization")) != 0 {
			httpapi.Error(w, 400, "invalid_token_request", "网页登录不接受 Authorization")
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/auth/browser-session" && len(r.Header.Values("Authorization")) != 0 {
			httpapi.Error(w, 400, "invalid_token_request", "请在 WebView 中打开换票地址")
			return
		}
		if nativeTokenEndpoint(r) {
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" || len(r.Header.Values("Cookie")) != 0 || len(r.Header.Values("Authorization")) != 0 {
				httpapi.Error(w, 400, "invalid_token_request", "请使用无 Cookie 的 JSON 请求")
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		values := r.Header.Values("Authorization")
		if len(values) == 0 {
			browser.ServeHTTP(w, r)
			return
		}
		if len(values) != 1 || len(r.Header.Values("Cookie")) != 0 {
			httpapi.Error(w, 400, "ambiguous_credentials", "请仅提供一种登录凭据")
			return
		}
		parts := strings.Fields(values[0])
		if s.mode != ModePassword || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || len(parts[1]) > 4096 {
			invalidToken(w)
			return
		}
		claims, err := s.validateAccess(r.Context(), parts[1])
		if err != nil {
			invalidToken(w)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceContextKey{}, claims)))
	})
}

func (s *Service) trackBrowser(r *http.Request) error {
	return s.queries.InsertBrowserSession(r.Context(), dbsqlc.InsertBrowserSessionParams{
		ID: uuid.NewString(), TokenHash: tokenHash(s.sessions.Token(r.Context())), CreatedAt: s.now().UnixMilli(), LastSeenAt: s.now().UnixMilli(),
		DeviceSessionID: sql.NullString{String: s.sessions.GetString(r.Context(), "deviceSessionID"), Valid: s.sessions.GetString(r.Context(), "deviceSessionID") != ""},
	})
}

// Streaming handlers retain their request context after the handshake. Poll the
// authoritative login state and close a hijacked connection when it is revoked.
func (s *Service) serveAuthenticated(w http.ResponseWriter, r *http.Request, next http.Handler, version int64) {
	stream := strings.HasSuffix(r.URL.Path, "/stream") || strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	if !stream {
		next.ServeHTTP(w, r)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	tracked := &loginWriter{ResponseWriter: w}
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.liveLogin(ctx, r, version) != nil {
					cancel()
					tracked.closeConnection()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-done }()
	next.ServeHTTP(tracked, r.WithContext(ctx))
}

func (s *Service) liveLogin(ctx context.Context, r *http.Request, version int64) error {
	if claims, ok := r.Context().Value(deviceContextKey{}).(*accessClaims); ok {
		return s.checkDevice(ctx, claims)
	}
	credential, err := s.queries.GetAuthCredential(ctx)
	if err != nil {
		return err
	}
	if credential.UpdatedAt != version {
		return errInvalidToken
	}
	if !s.browserDeviceActive(r) {
		return errInvalidToken
	}
	cookie, err := r.Cookie(s.sessions.Cookie.Name)
	if err != nil {
		return err
	}
	_, err = s.queries.FindSession(ctx, dbsqlc.FindSessionParams{Token: tokenHash(cookie.Value), Expiry: s.now().UnixMilli()})
	if errors.Is(err, sql.ErrNoRows) {
		return errInvalidToken
	}
	return err
}

type loginWriter struct {
	http.ResponseWriter
	mu     sync.Mutex
	conn   net.Conn
	closed bool
}

func (w *loginWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *loginWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *loginWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	w.mu.Lock()
	w.conn = conn
	if w.closed {
		_ = conn.Close()
	}
	w.mu.Unlock()
	return conn, rw, nil
}
func (w *loginWriter) closeConnection() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.conn != nil {
		_ = w.conn.Close()
	}
}
