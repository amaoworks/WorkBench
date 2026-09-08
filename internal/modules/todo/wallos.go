package todo

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	_ "time/tzdata"

	"workbench/internal/contracts"
	"workbench/internal/foundation/httpapi"
	"workbench/internal/foundation/identity"
	todosqlc "workbench/internal/modules/todo/sqlc"
)

type wallosConfig struct {
	Enabled      bool   `json:"enabled"`
	BaseURL      string `json:"baseUrl"`
	APIKey       string `json:"apiKey,omitempty"`
	DaysBefore   int    `json:"daysBefore"`
	ReminderHour int    `json:"reminderHour"`
	TimeZone     string `json:"timeZone"`
}

type wallosSettings struct {
	wallosConfig
	HasAPIKey bool       `json:"hasApiKey"`
	LastSync  *time.Time `json:"lastSync,omitempty"`
	LastError string     `json:"lastError"`
}

func (m *Module) loadWallos(ctx context.Context) (wallosSettings, error) {
	s := wallosSettings{wallosConfig: wallosConfig{DaysBefore: 3, ReminderHour: 9, TimeZone: "Asia/Shanghai"}}
	row, err := m.queries.GetWallosSettings(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(row.Config), &s.wallosConfig); err != nil {
		return s, err
	}
	s.HasAPIKey = s.APIKey != ""
	s.LastSync, s.LastError = nullableTime(row.LastSync), row.LastError
	return s, nil
}

func (m *Module) getWallos(w http.ResponseWriter, r *http.Request) {
	s, err := m.loadWallos(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "wallos_settings_failed", "无法读取 Wallos 配置")
		return
	}
	s.APIKey = ""
	w.Header().Set("Cache-Control", "no-store")
	httpapi.Write(w, 200, s)
}

func validateWallos(c *wallosConfig) error {
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.APIKey = strings.TrimSpace(c.APIKey)
	if c.BaseURL != "" {
		u, err := url.Parse(c.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("Wallos 地址必须是完整的 HTTP(S) 地址，不能包含凭据、查询参数或片段")
		}
	}
	if len(c.BaseURL) > 2048 || len(c.APIKey) > 4096 {
		return errors.New("地址或 API Key 过长")
	}
	if c.Enabled && (c.BaseURL == "" || c.APIKey == "") {
		return errors.New("启用联动需要 Wallos 地址和 API Key")
	}
	if c.DaysBefore < 0 || c.DaysBefore > 90 || c.ReminderHour < 0 || c.ReminderHour > 23 {
		return errors.New("提前天数须为 0–90，提醒小时须为 0–23")
	}
	if c.TimeZone == "" || c.TimeZone == "Local" {
		return errors.New("请填写明确的 IANA 时区，例如 Asia/Shanghai")
	}
	if _, err := time.LoadLocation(c.TimeZone); err != nil {
		return errors.New("无效的 IANA 时区")
	}
	return nil
}

func (m *Module) saveWallos(w http.ResponseWriter, r *http.Request) {
	var c wallosConfig
	if httpapi.Decode(w, r, &c, 16*1024) != nil {
		httpapi.Error(w, 400, "invalid_request", "无效的 Wallos 配置")
		return
	}
	m.wallosMu.Lock()
	defer m.wallosMu.Unlock()
	old, err := m.loadWallos(r.Context())
	if err != nil {
		httpapi.Error(w, 500, "wallos_settings_failed", "无法读取配置")
		return
	}
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if strings.TrimSpace(c.APIKey) == "" {
		if c.BaseURL != old.BaseURL && old.HasAPIKey && c.BaseURL != "" {
			httpapi.Error(w, 400, "invalid_request", "更换 Wallos 地址时请重新填写 API Key")
			return
		}
		if c.BaseURL == old.BaseURL {
			c.APIKey = old.APIKey
		}
	}
	if err := validateWallos(&c); err != nil {
		httpapi.Error(w, 400, "invalid_request", err.Error())
		return
	}
	data, _ := json.Marshal(c)
	if err := m.queries.SaveWallosSettings(r.Context(), string(data)); err != nil {
		httpapi.Error(w, 500, "wallos_settings_failed", "无法保存配置")
		return
	}
	c.APIKey = ""
	httpapi.Write(w, 200, map[string]bool{"saved": true})
}

type wallosSubscription struct {
	ID            json.Number `json:"id"`
	Name          string      `json:"name"`
	NextPayment   string      `json:"next_payment"`
	Inactive      json.Number `json:"inactive"`
	Category      string      `json:"category_name"`
	PaymentMethod string      `json:"payment_method_name"`
	Price         json.Number `json:"price"`
	CurrencyID    json.Number `json:"currency_id"`
	Amount        string      `json:"-"`
}

// Wallos accepts form POSTs. Keep the API key out of URLs and never follow
// redirects, which could forward the form body to a different server.
func wallosRequest(ctx context.Context, c wallosConfig, endpoint string) ([]byte, error) {
	form := url.Values{"api_key": {c.APIKey}, "state": {"0"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, errors.New("无法创建 Wallos 请求")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, errors.New("连接 Wallos 失败，请检查地址、网络和证书")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Wallos 返回 HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 4*1024*1024+1))
	if err != nil || len(data) > 4*1024*1024 {
		return nil, errors.New("Wallos 响应读取失败或超过 4 MiB")
	}
	return data, nil
}

func fetchWallos(ctx context.Context, c wallosConfig) ([]wallosSubscription, error) {
	data, err := wallosRequest(ctx, c, "/api/subscriptions/get_subscriptions.php")
	if err != nil {
		return nil, err
	}
	var body struct {
		Success       bool                 `json:"success"`
		Subscriptions []wallosSubscription `json:"subscriptions"`
	}
	if json.Unmarshal(data, &body) != nil || !body.Success || body.Subscriptions == nil {
		return nil, errors.New("Wallos 响应无效，请检查 API Key 和 API 版本")
	}
	if len(body.Subscriptions) > 10000 {
		return nil, errors.New("Wallos 订阅数量超过 10000")
	}
	var currencies []wallosCurrency
	for _, sub := range body.Subscriptions {
		if sub.Price != "" {
			currencies, err = fetchWallosCurrencies(ctx, c)
			if err != nil {
				return nil, err
			}
			break
		}
	}
	for i := range body.Subscriptions {
		body.Subscriptions[i].Amount = wallosAmount(body.Subscriptions[i], currencies)
	}
	return body.Subscriptions, nil
}

func (m *Module) syncWallos(ctx context.Context) (int, error) {
	m.wallosMu.Lock()
	defer m.wallosMu.Unlock()
	s, err := m.loadWallos(ctx)
	if err != nil {
		return 0, err
	}
	if !s.Enabled {
		return 0, nil
	}
	if err := validateWallos(&s.wallosConfig); err != nil {
		return 0, err
	}
	subs, err := fetchWallos(ctx, s.wallosConfig)
	created := 0
	if err == nil {
		created, err = m.importWallos(ctx, s.wallosConfig, subs)
	}
	status := todosqlc.SetWallosSyncStatusParams{LastSync: toNullTime(s.LastSync)}
	if err != nil {
		status.LastError = err.Error()
	} else {
		status.LastSync = sql.NullInt64{Int64: m.now().UTC().UnixMilli(), Valid: true}
	}
	if saveErr := m.queries.SetWallosSyncStatus(ctx, status); saveErr != nil {
		return created, errors.Join(err, saveErr)
	}
	return created, err
}

func (m *Module) syncWallosHTTP(w http.ResponseWriter, r *http.Request) {
	n, err := m.syncWallos(r.Context())
	if err != nil {
		httpapi.Error(w, 502, "wallos_sync_failed", err.Error())
		return
	}
	httpapi.Write(w, 200, map[string]int{"created": n})
}

func (m *Module) importWallos(ctx context.Context, c wallosConfig, subs []wallosSubscription) (int, error) {
	loc, err := time.LoadLocation(c.TimeZone)
	if err != nil {
		return 0, err
	}
	now := m.now().UTC()
	localNow := now.In(loc)
	// Identity is scoped to the configured instance. No remote content or secret
	// is included in the occurrence key.
	source := fmt.Sprintf("%x", sha256.Sum256([]byte(c.BaseURL)))
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	q := m.queries.WithTx(tx)
	created := 0
	for _, sub := range subs {
		if sub.Inactive != "" && sub.Inactive != "0" {
			continue
		}
		id, idErr := sub.ID.Int64()
		payment, dateErr := time.ParseInLocation("2006-01-02", sub.NextPayment, loc)
		title := "Wallos续费提醒 - " + strings.TrimSpace(sub.Name)
		if idErr != nil || id <= 0 || dateErr != nil || strings.TrimSpace(sub.Name) == "" || len(title) > 300 {
			return 0, errors.New("Wallos 订阅包含无效 ID、名称或付款日期，未写入本批待办")
		}
		day := payment.AddDate(0, 0, -c.DaysBefore)
		due := time.Date(day.Year(), day.Month(), day.Day(), c.ReminderHour, 0, 0, 0, loc).UTC()
		// Populate the current month's payment list before reminders are due.
		// Also admit reminders already due across a month boundary (e.g. an
		// October 1 payment with a September 28 reminder).
		inCurrentMonth := payment.Year() == localNow.Year() && payment.Month() == localNow.Month()
		if !inCurrentMonth && due.After(now) {
			continue
		}
		taskID, err := identity.New()
		if err != nil {
			return 0, err
		}
		n, err := q.ClaimWallosOccurrence(ctx, todosqlc.ClaimWallosOccurrenceParams{Source: source, SubscriptionID: sub.ID.String(), PaymentDate: sub.NextPayment, TaskID: taskID})
		if err != nil {
			return 0, err
		}
		description := wallosDescription(sub, c.TimeZone)
		if len(description) > 10000 {
			return 0, errors.New("Wallos 订阅说明超过 10000 字节")
		}
		if n == 0 {
			existingID, err := q.GetWallosOccurrenceTask(ctx, todosqlc.GetWallosOccurrenceTaskParams{Source: source, SubscriptionID: sub.ID.String(), PaymentDate: sub.NextPayment})
			if err != nil {
				return 0, err
			}
			changed, err := q.UpdateWallosTaskContent(ctx, todosqlc.UpdateWallosTaskContentParams{ID: existingID, Title: title, Description: description, UpdatedAt: now.UnixMilli()})
			if err != nil {
				return 0, err
			}
			if changed > 0 {
				payload, _ := json.Marshal(map[string]string{"taskId": existingID})
				if _, err := m.events.PublishTx(ctx, tx, contracts.NewEvent{Topic: "todo.task.updated", SchemaVersion: 1, SourceModule: "todo", AggregateID: existingID, Payload: payload}); err != nil {
					return 0, err
				}
			}
			continue
		}
		if err := q.CreateTask(ctx, todosqlc.CreateTaskParams{ID: taskID, Title: title, Description: description, DueAt: toNullTime(&due), CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()}); err != nil {
			return 0, err
		}
		payload, _ := json.Marshal(map[string]string{"taskId": taskID, "title": title})
		if _, err := m.events.PublishTx(ctx, tx, contracts.NewEvent{Topic: "todo.task.created", SchemaVersion: 1, SourceModule: "todo", AggregateID: taskID, Payload: payload}); err != nil {
			return 0, err
		}
		created++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return created, nil
}
