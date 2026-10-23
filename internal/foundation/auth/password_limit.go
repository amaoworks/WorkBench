package auth

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	maxPasswordConcurrency = 2
	loginBurst             = 5
	loginRefillInterval    = 3 * time.Second
)

var (
	errPasswordBusy     = errors.New("password computation capacity reached")
	errLoginRateLimited = errors.New("login rate limit reached")
)

type passwordLimit struct {
	mu       sync.Mutex
	active   int
	tokens   int
	refillAt time.Time
	now      func() time.Time
}

func newPasswordLimit() *passwordLimit {
	return &passwordLimit{tokens: loginBurst, refillAt: time.Now(), now: time.Now}
}

// acquire reserves capacity without queuing. Only login attempts spend tokens.
func (l *passwordLimit) acquire(login bool) (retryAfter int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	if intervals := now.Sub(l.refillAt) / loginRefillInterval; intervals > 0 {
		if intervals >= loginBurst {
			l.tokens = loginBurst
		} else {
			l.tokens = min(loginBurst, l.tokens+int(intervals))
		}
		l.refillAt = l.refillAt.Add(intervals * loginRefillInterval)
	}
	if login && l.tokens == 0 {
		remaining := loginRefillInterval - now.Sub(l.refillAt)
		return max(1, int((remaining+time.Second-1)/time.Second)), errLoginRateLimited
	}
	if l.active == maxPasswordConcurrency {
		return 1, errPasswordBusy
	}
	if login {
		l.tokens--
	}
	l.active++
	return 0, nil
}

func (l *passwordLimit) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
}

func writePasswordLimit(w http.ResponseWriter, retryAfter int, err error) {
	code, message := "authentication_busy", "password computation capacity reached"
	if errors.Is(err, errLoginRateLimited) {
		code, message = "login_rate_limited", "too many login attempts"
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	writeJSON(w, http.StatusTooManyRequests, map[string]string{"code": code, "message": message})
}
