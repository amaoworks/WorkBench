package app

import (
	"bufio"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"workbench/internal/foundation/httpapi"
	"workbench/internal/foundation/logging"
)

type LoggingSettings struct {
	Level string `json:"level"`
}

func (a *App) saveLoggingSettings(w http.ResponseWriter, r *http.Request) {
	var value LoggingSettings
	if err := httpapi.Decode(w, r, &value, 4096); err != nil {
		httpapi.Error(w, 400, "invalid_settings", "日志设置格式不正确")
		return
	}
	level, err := logging.ParseLevel(value.Level)
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", "日志等级须为 debug、info、warn 或 error")
		return
	}
	value.Level = strings.ToLower(level.String())
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	if err := a.persistSettings(r.Context(), "logging", value); err != nil {
		a.logger.Error("log settings persistence failed", "component", "settings")
		httpapi.Error(w, 500, "settings_failed", "无法保存日志设置")
		return
	}
	a.logging = value
	a.logLevel.Set(level)
	a.logger.Info("log level changed", "component", "settings", "logLevel", value.Level)
	httpapi.Write(w, 200, value)
}

func (a *App) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := middleware.GetReqID(r.Context())
		w.Header().Set("X-Request-ID", requestID)
		var upgrade *upgradeWriter
		if _, hijacks := w.(http.Hijacker); hijacks {
			if _, flushes := w.(http.Flusher); flushes {
				upgrade = &upgradeWriter{ResponseWriter: w}
				w = upgrade
			}
		}
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		started := time.Now()
		completed := false
		defer func() {
			status := wrapped.Status()
			if upgrade != nil && upgrade.hijacked && status == 0 {
				status = http.StatusSwitchingProtocols
			} else if status == 0 {
				status = http.StatusOK
			}
			// Route templates exclude account IDs, OAuth query strings and arbitrary
			// untrusted paths. Rejected and unmatched requests have no route yet.
			path := chi.RouteContext(r.Context()).RoutePattern()
			if path == "" {
				path = "unmatched"
			}
			level := slog.LevelInfo
			switch {
			case status >= 500:
				level = slog.LevelError
			case status >= 400:
				level = slog.LevelWarn
			case !completed:
				level = slog.LevelWarn
			case path == "/health/live" || path == "/health/ready":
				level = slog.LevelDebug
			}
			a.logger.Log(r.Context(), level, "HTTP request", "component", "http",
				"requestId", requestID, "method", r.Method, "path", path,
				"status", status, "bytes", wrapped.BytesWritten(),
				"durationMs", time.Since(started).Milliseconds(), "aborted", !completed)
		}()
		next.ServeHTTP(wrapped, r)
		completed = true
	})
}

// WebSocket handshakes write directly to the hijacked connection, bypassing
// WriteHeader. Track the upgrade while preserving HTTP/1 streaming capabilities.
type upgradeWriter struct {
	http.ResponseWriter
	hijacked bool
}

func (w *upgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err == nil {
		w.hijacked = true
	}
	return conn, rw, err
}

func (w *upgradeWriter) Flush()                      { w.ResponseWriter.(http.Flusher).Flush() }
func (w *upgradeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (a *App) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				// Panic values may contain secrets; record the stack without the value.
				a.logger.Error("HTTP handler panic", "component", "http",
					"requestId", middleware.GetReqID(r.Context()), "stack", string(debug.Stack()))
				httpapi.Error(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
