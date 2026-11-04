package notifications

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"workbench/internal/contracts"
	dbsqlc "workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/httpapi"
)

var telegramTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)
var telegramChatPattern = regexp.MustCompile(`^@[A-Za-z][A-Za-z0-9_]{4,31}$`)

func validTelegramChat(chat string) bool {
	if chat == "" {
		return true
	}
	id, err := strconv.ParseInt(chat, 10, 64)
	return (err == nil && id != 0) || telegramChatPattern.MatchString(chat)
}

func telegramAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !contracts.IsAdmin(r.Context()) {
		httpapi.Error(w, 403, "admin_required", "administrator permission required")
		return false
	}
	return true
}

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
	config := telegramConfig{Enabled: *input.Enabled, BotToken: strings.TrimSpace(input.BotToken), ChatID: strings.TrimSpace(input.ChatID), ActivatedAt: previous.ActivatedAt, Generation: previous.Generation}
	if input.ClearBotToken && config.BotToken != "" {
		return config, errors.New("不能同时清除和替换 Bot Token")
	}
	if config.BotToken == "" && !input.ClearBotToken {
		config.BotToken = previous.BotToken
	}
	if len(config.BotToken) > 512 || (config.BotToken != "" && !telegramTokenPattern.MatchString(config.BotToken)) {
		return config, errors.New("Bot Token 格式无效")
	}
	if !validTelegramChat(config.ChatID) {
		return config, errors.New("Chat ID 格式无效")
	}
	if config.Enabled && config.BotToken == "" {
		return config, errors.New("启用前请填写 Bot Token")
	}
	if config.BotToken != previous.BotToken {
		config.Generation++
	}
	if config.Enabled && (!previous.Enabled || config.BotToken != previous.BotToken) {
		config.ActivatedAt = t.now().UnixMilli()
	}
	return config, nil
}

func (t *Telegram) Settings(w http.ResponseWriter, r *http.Request) {
	if !telegramAdmin(w, r) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	var config telegramConfig
	if t.load(r.Context(), "telegram", &config) != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取 Telegram 配置")
		return
	}
	recipient, err := t.recipient(r.Context(), contracts.UserID(r.Context()))
	if err != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取接收设置")
		return
	}
	var status telegramStatus
	if json.Unmarshal([]byte(recipient.StatusJson), &status) != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取投递状态")
		return
	}
	var failed int
	if t.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM event_deliveries d JOIN events_log e ON e.id=d.event_id WHERE d.consumer_id=? AND d.status='dead' AND e.user_id=? AND e.occurred_at>=?`, telegramConsumerID, recipient.UserID, recipient.ActivatedAt).Scan(&failed) != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取投递失败记录")
		return
	}
	httpapi.Write(w, 200, map[string]any{"enabled": config.Enabled, "chatId": recipient.ChatID, "hasBotToken": config.BotToken != "", "status": status, "failedDeliveries": failed})
}

func (t *Telegram) SaveSettings(w http.ResponseWriter, r *http.Request) {
	if !telegramAdmin(w, r) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var previous telegramConfig
	if t.load(r.Context(), "telegram", &previous) != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取 Telegram 配置")
		return
	}
	config, err := t.readInput(w, r, previous)
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", err.Error())
		return
	}
	tx, err := t.db.BeginTx(r.Context(), nil)
	if err != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法保存 Telegram 配置")
		return
	}
	defer tx.Rollback()
	chatID := config.ChatID
	config.ChatID = "" // Personal targets never live in global configuration.
	raw, _ := json.Marshal(config)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO workspace_settings(section,value) VALUES('telegram',?) ON CONFLICT(section) DO UPDATE SET value=excluded.value`, string(raw))
	if err == nil && chatID != "" {
		recipient, loadErr := dbsqlc.New(tx).GetTelegramRecipient(r.Context(), contracts.UserID(r.Context()))
		activation := t.now().UnixMilli()
		if loadErr == nil && recipient.ChatID == chatID && recipient.BotGeneration == config.Generation && recipient.Enabled != 0 {
			activation = recipient.ActivatedAt
		}
		err = dbsqlc.New(tx).SaveTelegramRecipient(r.Context(), dbsqlc.SaveTelegramRecipientParams{UserID: contracts.UserID(r.Context()), Enabled: 1, ChatID: chatID, ActivatedAt: activation, BotGeneration: config.Generation})
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		_ = tx.Rollback()
		httpapi.Error(w, 500, "telegram_settings_failed", "无法保存 Telegram 配置")
		return
	}
	httpapi.Write(w, 200, map[string]bool{"saved": true})
}

func (t *Telegram) TestSettings(w http.ResponseWriter, r *http.Request) {
	if !telegramAdmin(w, r) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	var previous telegramConfig
	if t.load(r.Context(), "telegram", &previous) != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取 Telegram 配置")
		return
	}
	config, err := t.readInput(w, r, previous)
	if err != nil {
		httpapi.Error(w, 400, "invalid_settings", err.Error())
		return
	}
	if config.ChatID == "" || config.BotToken == "" {
		httpapi.Error(w, 400, "invalid_settings", "测试发送需要 Bot Token 和 Chat ID")
		return
	}
	if err = t.sendRateLimited(r.Context(), config, "Workbench 测试通知\nTelegram 推送连接成功。"); err != nil {
		httpapi.Error(w, 502, "telegram_test_failed", err.Error())
		return
	}
	httpapi.Write(w, 200, map[string]bool{"ok": true})
}

func (t *Telegram) RecipientSettings(w http.ResponseWriter, r *http.Request) {
	t.recipientSettings(w, r, contracts.UserID(r.Context()))
}

func (t *Telegram) RecipientMappingSettings(w http.ResponseWriter, r *http.Request) {
	if !telegramAdmin(w, r) {
		return
	}
	t.recipientSettings(w, r, r.PathValue("userID"))
}

func (t *Telegram) recipientSettings(w http.ResponseWriter, r *http.Request, userID string) {
	if contracts.UserID(r.Context()) == "" || userID == "" {
		httpapi.Error(w, 401, "authentication_required", "authentication required")
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	var config telegramConfig
	recipient, err := t.recipient(r.Context(), userID)
	if err != nil || t.load(r.Context(), "telegram", &config) != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取接收设置")
		return
	}
	var status telegramStatus
	if json.Unmarshal([]byte(recipient.StatusJson), &status) != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取投递状态")
		return
	}
	httpapi.Write(w, 200, map[string]any{"enabled": recipient.Enabled != 0, "chatId": recipient.ChatID, "configured": config.Enabled && config.BotToken != "" && recipient.ChatID != "" && recipient.BotGeneration == config.Generation, "hasBotToken": config.BotToken != "", "status": status})
}

func (t *Telegram) SaveRecipientSettings(w http.ResponseWriter, r *http.Request) {
	t.saveRecipient(w, r, contracts.UserID(r.Context()), contracts.IsAdmin(r.Context()))
}

func (t *Telegram) SaveRecipientMapping(w http.ResponseWriter, r *http.Request) {
	if !telegramAdmin(w, r) {
		return
	}
	t.saveRecipient(w, r, r.PathValue("userID"), true)
}

func (t *Telegram) saveRecipient(w http.ResponseWriter, r *http.Request, userID string, mapping bool) {
	if contracts.UserID(r.Context()) == "" || userID == "" {
		httpapi.Error(w, 401, "authentication_required", "authentication required")
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.recipientActive(r.Context(), userID) {
		httpapi.Error(w, 404, "user_not_found", "user is inactive")
		return
	}
	var input struct {
		Enabled *bool   `json:"enabled"`
		ChatID  *string `json:"chatId"`
	}
	if httpapi.Decode(w, r, &input, 4096) != nil || input.Enabled == nil {
		httpapi.Error(w, 400, "invalid_settings", "接收设置格式不正确")
		return
	}
	recipient, err := t.recipient(r.Context(), userID)
	var config telegramConfig
	if err != nil || t.load(r.Context(), "telegram", &config) != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取接收设置")
		return
	}
	chatID := recipient.ChatID
	generation := recipient.BotGeneration
	if input.ChatID != nil {
		if !mapping {
			httpapi.Error(w, 403, "admin_required", "接收目标由管理员分配")
			return
		}
		chatID = strings.TrimSpace(*input.ChatID)
		if !validTelegramChat(chatID) {
			httpapi.Error(w, 400, "invalid_settings", "Chat ID 格式无效")
			return
		}
		generation = config.Generation
	}
	if *input.Enabled && (chatID == "" || generation != config.Generation) {
		httpapi.Error(w, 400, "recipient_not_configured", "请联系管理员分配接收目标")
		return
	}
	activatedAt := recipient.ActivatedAt
	if (*input.Enabled && recipient.Enabled == 0) || chatID != recipient.ChatID || generation != recipient.BotGeneration {
		activatedAt = t.now().UnixMilli()
	}
	err = dbsqlc.New(t.db).SaveTelegramRecipient(r.Context(), dbsqlc.SaveTelegramRecipientParams{UserID: userID, Enabled: boolInt64(*input.Enabled), ChatID: chatID, ActivatedAt: activatedAt, BotGeneration: generation})
	if err != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法保存接收设置")
		return
	}
	httpapi.Write(w, 200, map[string]bool{"saved": true})
}

func (t *Telegram) TestRecipientSettings(w http.ResponseWriter, r *http.Request) {
	userID := contracts.UserID(r.Context())
	if userID == "" {
		httpapi.Error(w, 401, "authentication_required", "authentication required")
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.recipientActive(r.Context(), userID) {
		httpapi.Error(w, 401, "authentication_required", "authentication required")
		return
	}
	var config telegramConfig
	recipient, err := t.recipient(r.Context(), userID)
	if err != nil || t.load(r.Context(), "telegram", &config) != nil {
		httpapi.Error(w, 500, "telegram_settings_failed", "无法读取接收设置")
		return
	}
	if !config.Enabled || config.BotToken == "" || recipient.ChatID == "" || recipient.BotGeneration != config.Generation {
		httpapi.Error(w, 400, "recipient_not_configured", "接收目标尚未配置")
		return
	}
	config.ChatID = recipient.ChatID
	if err = t.sendTracked(r.Context(), config, "Workbench 测试通知\nTelegram 推送连接成功。"); err != nil {
		httpapi.Error(w, 502, "telegram_test_failed", err.Error())
		return
	}
	httpapi.Write(w, 200, map[string]bool{"ok": true})
}
