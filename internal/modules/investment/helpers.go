package investment

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"
)

func validProxyPath(path string) bool {
	if path == "" || strings.Contains(path, "\\") || strings.Contains(path, "..") || strings.Contains(path, "://") {
		return false
	}
	return !strings.HasPrefix(path, "/")
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func unixMilli(value int64) time.Time {
	if value == 0 {
		return time.Time{}
	}
	return time.UnixMilli(value).UTC()
}

func timeUnixMilli(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.UTC().UnixMilli()
}

func trimErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 300 {
		return text[:300]
	}
	if text == "" {
		return "empty body"
	}
	return text
}
