// Package logging owns the workspace-wide, dynamically adjustable log threshold.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("log level must be debug, info, warn or error")
	}
}

// New preserves the supplied logger's output and attributes, but owns its level
// filtering. All derived loggers share the same concurrency-safe threshold.
func New(output *slog.Logger, initial string) (*slog.Logger, *slog.LevelVar, error) {
	level, err := ParseLevel(initial)
	if err != nil {
		return nil, nil, err
	}
	if output == nil {
		output = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	threshold := new(slog.LevelVar)
	threshold.Set(level)
	return slog.New(&levelHandler{Handler: output.Handler(), level: threshold}), threshold, nil
}

type levelHandler struct {
	slog.Handler
	level *slog.LevelVar
}

func (h *levelHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithAttrs(attrs), level: h.level}
}

func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}
