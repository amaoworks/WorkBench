package notifications

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"workbench/internal/foundation/httpapi"
)

var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var telegramChatPattern = regexp.MustCompile(`^@[A-Za-z][A-Za-z0-9_]{4,31}$`)

func (t *Telegram) readInput(w http.ResponseWriter, r *http.Request, previous telegramConfig) (telegramConfig, error) {
	var input struct {
		Enabled       *bool  `json:"enabled"`
		BotToken      string `json:"botToken"`
		ChatID        string `json:"chatId"`
		ClearBotToken bool   `json:"clearBotToken"`
	}
	if httpapi.Decode(w, r, &input, 4096) != nil || input.Enabled == nil {
		return telegramConfig{}, errors.New("Telegram 配置格式不正确")
	}
	config := telegramConfig{Enabled: *input.Enabled, BotToken: strings.TrimSpace(input.BotToken), ChatID: strings.TrimSpace(input.ChatID), ActivatedAt: previous.ActivatedAt}
	if input.ClearBotToken && config.BotToken != "" {
		return config, errors.New("不能同时清除和替换 Bot Token")
	}
	if config.BotToken == "" && !input.ClearBotToken {
		config.BotToken = previous.BotToken
	}
	if len(config.BotToken) > 512 || (config.BotToken != "" && !telegramTokenPattern.MatchString(config.BotToken)) {
		return config, errors.New("Bot Token 格式无效")
	}
	if config.ChatID != "" {
		id, err := strconv.ParseInt(config.ChatID, 10, 64)
		if (err != nil || id == 0) && !telegramChatPattern.MatchString(config.ChatID) {
			return config, errors.New("Chat ID 应为数字 ID（群组可为负数）或 @频道用户名")
		}
	}
	if config.Enabled && (config.BotToken == "" || config.ChatID == "") {
		return config, errors.New("启用前请填写 Bot Token 和 Chat ID")
	}
	if config.Enabled && (!previous.Enabled || config.BotToken != previous.BotToken || config.ChatID != previous.ChatID) {
		config.ActivatedAt = t.now().UnixMilli()
	}
	return config, nil
}

func (t *Telegram) Settings(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	var config telegramConfig
	var status telegramStatus
	if err := t.load(r.Context(), "telegram", &config); err != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取 Telegram 配置")
		return
	}
	if err := t.load(r.Context(), "telegram_status", &status); err != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取投递状态")
		return
	}
	var failed int
	if err := t.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM event_deliveries d JOIN events_log e ON e.id=d.event_id
		WHERE d.consumer_id=? AND d.status='dead' AND e.occurred_at>=?`, telegramConsumerID, config.ActivatedAt).Scan(&failed); err != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取投递失败记录")
		return
	}
	httpapi.Write(w, 200, map[string]any{"enabled": config.Enabled, "chatId": config.ChatID, "hasBotToken": config.BotToken != "", "status": status, "failedDeliveries": failed})
}

func (t *Telegram) SaveSettings(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var previous telegramConfig
	if err := t.load(r.Context(), "telegram", &previous); err != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取 Telegram 配置")
		return
	}
	config, err := t.readInput(w, r, previous)
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", err.Error())
		return
	}
	if err := t.save(r.Context(), "telegram", config); err != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法保存 Telegram 配置")
		return
	}
	httpapi.Write(w, 200, map[string]bool{"saved": true})
}

func (t *Telegram) TestSettings(w http.ResponseWriter, r *http.Request) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var previous telegramConfig
	if err := t.load(r.Context(), "telegram", &previous); err != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取 Telegram 配置")
		return
	}
	config, err := t.readInput(w, r, previous)
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", err.Error())
		return
	}
	if config.BotToken == "" || config.ChatID == "" {
		httpapi.Error(w, 400, "invalid_settings", "测试发送需要 Bot Token 和 Chat ID")
		return
	}
	send := t.sendRateLimited
	if config.BotToken == previous.BotToken && config.ChatID == previous.ChatID {
		send = t.sendTracked
	}
	if err := send(r.Context(), config, "Workbench 测试通知\nTelegram 推送连接成功。"); err != nil {
		httpapi.Error(w, 502, "telegram_test_failed", err.Error())
		return
	}
	httpapi.Write(w, 200, map[string]bool{"ok": true})
}
