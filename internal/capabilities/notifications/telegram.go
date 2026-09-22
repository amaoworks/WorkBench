package notifications

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"workbench/internal/contracts"
)

const telegramConsumerID = "core.notification.telegram"

type telegramConfig struct {
	Enabled     bool   `json:"enabled"`
	BotToken    string `json:"botToken"`
	ChatID      string `json:"chatId"`
	ActivatedAt int64  `json:"activatedAt"`
}

type telegramStatus struct {
	LastAttemptAt *time.Time `json:"lastAttemptAt"`
	LastSuccessAt *time.Time `json:"lastSuccessAt"`
	LastError     string     `json:"lastError"`
}

// Telegram uses the existing durable event deliveries. A separate dispatcher
// keeps HTTP timeouts and per-chat pacing out of the browser SSE delivery path.
type Telegram struct {
	db        *sql.DB
	logger    *slog.Logger
	publicURL string
	allowed   func(contracts.ModuleID) bool
	mu        sync.Mutex // serializes configuration, sends, tests and rate-limit state
	client    *http.Client
	baseURL   string
	now       func() time.Time
	rates     map[string]*telegramRate
}

type telegramRate struct{ nextSend, blockedUntil time.Time }

func NewTelegram(db *sql.DB, publicURL string, allowed func(contracts.ModuleID) bool, logger *slog.Logger) *Telegram {
	if logger == nil {
		logger = slog.Default()
	}
	return &Telegram{db: db, logger: logger, publicURL: strings.TrimRight(publicURL, "/"), allowed: allowed, rates: make(map[string]*telegramRate),
		client:  &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		baseURL: "https://api.telegram.org", now: time.Now}
}

func (*Telegram) ID() string { return "telegram" }

func (t *Telegram) Consumer() contracts.EventConsumer {
	return contracts.EventConsumer{ID: telegramConsumerID, Module: "core", Topics: []string{"core.notification.created"}, Timeout: 15 * time.Second, MaxAttempts: 12,
		Handler: func(ctx context.Context, event contracts.Event) error {
			var payload struct {
				NotificationID string `json:"notificationId"`
			}
			if json.Unmarshal(event.Payload, &payload) != nil || payload.NotificationID == "" {
				return &telegramError{message: "通知事件格式无效", permanent: true}
			}
			note, err := scanNotification(t.db.QueryRowContext(ctx, notificationSelect+" WHERE id = ?", payload.NotificationID))
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return errors.New("无法读取待推送通知")
			}
			return t.Deliver(ctx, note)
		}}
}

func (t *Telegram) load(ctx context.Context, section string, target any) error {
	var raw string
	err := t.db.QueryRowContext(ctx, "SELECT value FROM workspace_settings WHERE section = ?", section).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), target)
}

func (t *Telegram) save(ctx context.Context, section string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = t.db.ExecContext(ctx, "INSERT INTO workspace_settings(section,value) VALUES(?,?) ON CONFLICT(section) DO UPDATE SET value=excluded.value", section, string(raw))
	return err
}

func (t *Telegram) Deliver(ctx context.Context, note contracts.Notification) error {
	// Only explicitly selected investment price alerts are forwarded. Enabling
	// Telegram does not export Todo messages or replay an old notification inbox.
	if note.SourceModule != "investment" || !strings.HasPrefix(note.IdempotencyKey, "investment:monitor:") {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var config telegramConfig
	if err := t.load(ctx, "telegram", &config); err != nil {
		return errors.New("无法读取 Telegram 配置")
	}
	now := t.now()
	if !config.Enabled || note.CreatedAt.UnixMilli() < config.ActivatedAt || (t.allowed != nil && !t.allowed(note.SourceModule)) {
		return nil
	}
	if now.Sub(note.CreatedAt) > 15*time.Minute || (note.ExpiresAt != nil && !now.Before(*note.ExpiresAt)) {
		return &telegramError{message: "预警已超过投递有效期，停止补发，请查看站内记录", permanent: true}
	}
	content := note.Title + "\n\n" + note.Content
	if t.publicURL != "" && note.ActionRoute != "" {
		content += "\n\n" + t.publicURL + note.ActionRoute
	}
	return t.sendTracked(ctx, config, content)
}

func (t *Telegram) sendTracked(ctx context.Context, config telegramConfig, content string) error {
	var status telegramStatus
	if err := t.load(ctx, "telegram_status", &status); err != nil {
		return errors.New("无法读取 Telegram 投递状态")
	}
	now := t.now().UTC()
	status.LastAttemptAt = &now
	err := t.sendRateLimited(ctx, config, content)
	status.LastError = ""
	if err == nil {
		status.LastSuccessAt = &now
	} else {
		status.LastError = err.Error()
	}
	statusCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if saveErr := t.save(statusCtx, "telegram_status", status); saveErr != nil {
		// An auxiliary status write must never turn confirmed delivery into a
		// retry. The durable event completion is handled by the dispatcher.
		t.logger.Warn("cannot persist Telegram status")
	}
	return err
}

func (t *Telegram) sendRateLimited(ctx context.Context, config telegramConfig, content string) error {
	botID := strings.SplitN(config.BotToken, ":", 2)[0]
	rate := t.rates[botID]
	if rate == nil {
		rate = &telegramRate{}
		t.rates[botID] = rate
	}
	if delay := rate.blockedUntil.Sub(t.now()); delay > 0 {
		return &telegramError{message: "Telegram 请求受限，等待重试", retryAfter: delay}
	}
	if wait := rate.nextSend.Sub(t.now()); wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return errors.New("Telegram 发送已取消")
		case <-timer.C:
		}
	}
	err := t.send(ctx, config, content)
	rate.nextSend = t.now().Add(3100 * time.Millisecond)
	var limit *telegramError
	if errors.As(err, &limit) && limit.retryAfter > 0 {
		rate.blockedUntil = t.now().Add(limit.retryAfter)
	}
	return err
}

type telegramError struct {
	message    string
	retryAfter time.Duration
	permanent  bool
}

func (e *telegramError) Error() string             { return e.message }
func (e *telegramError) RetryAfter() time.Duration { return e.retryAfter }
func (e *telegramError) Permanent() bool           { return e.permanent }

func telegramText(value string) string {
	// Telegram counts UTF-16 units for text limits. Leave room for an ellipsis.
	units := 0
	for i, char := range value {
		units++
		if char > 0xffff {
			units++
		}
		if units > 4000 {
			return value[:i] + "…"
		}
	}
	return value
}

func (t *Telegram) send(ctx context.Context, config telegramConfig, content string) error {
	raw, _ := json.Marshal(map[string]any{"chat_id": config.ChatID, "text": telegramText(content), "link_preview_options": map[string]bool{"is_disabled": true}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+"/bot"+config.BotToken+"/sendMessage", bytes.NewReader(raw))
	if err != nil {
		return errors.New("无法创建 Telegram 请求")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	// Request URLs contain the token: never propagate a transport error or the
	// upstream body/description to logs, the database or an API response.
	if err != nil {
		return errors.New("无法连接 Telegram，请检查服务器网络")
	}
	defer resp.Body.Close()
	var result struct {
		OK         bool `json:"ok"`
		ErrorCode  int  `json:"error_code"`
		Parameters struct {
			RetryAfter int64 `json:"retry_after"`
		} `json:"parameters"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&result)
	code := resp.StatusCode
	if result.ErrorCode != 0 {
		code = result.ErrorCode
	}
	if code == 429 {
		seconds := result.Parameters.RetryAfter
		if header, err := strconv.ParseInt(resp.Header.Get("Retry-After"), 10, 64); err == nil {
			seconds = max(seconds, header)
		}
		seconds = max(1, min(seconds, 86400))
		return &telegramError{message: "Telegram 请求受限，等待重试", retryAfter: time.Duration(seconds) * time.Second}
	}
	if code == 400 || code == 401 || code == 403 || code == 404 {
		return &telegramError{message: "Telegram 拒绝发送，请检查 Bot Token、Chat ID 以及机器人会话/群组权限", permanent: true}
	}
	if code < 200 || code >= 300 || decodeErr != nil || !result.OK {
		return errors.New("Telegram 发送失败，稍后重试")
	}
	return nil
}
