package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workbench/internal/contracts"
	"workbench/internal/foundation/events"
)

type telegramTransport func(*http.Request) (*http.Response, error)

func (f telegramTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func telegramResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func setupTelegram(t *testing.T) (*Telegram, *Service, *time.Time) {
	t.Helper()
	service, db := openService(t)
	now := time.Date(2026, 9, 22, 14, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	channel := NewTelegram(db.SQL(), "https://workbench.example", func(contracts.ModuleID) bool { return true }, nil)
	channel.now = func() time.Time { return now }
	if err := channel.save(context.Background(), "telegram", telegramConfig{Enabled: true, BotToken: "123:private-token", ChatID: "-100123", ActivatedAt: now.Add(-time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	return channel, service, &now
}

func telegramNotice(now time.Time) contracts.Notification {
	return contracts.Notification{SourceModule: "investment", Title: "AAPL 上涨达到 3%", Content: "最新价：103 USD", CreatedAt: now,
		ActionRoute: "/investment?tab=monitor", IdempotencyKey: "investment:monitor:rule:2026-09-22"}
}

func TestTelegramPlaintextRoutingAndNoSecretExposure(t *testing.T) {
	channel, _, now := setupTelegram(t)
	calls := 0
	channel.client.Transport = telegramTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/bot123:private-token/sendMessage" || r.Header.Get("Cookie") != "" {
			t.Error("unexpected Telegram request")
		}
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["chat_id"] != "-100123" || payload["parse_mode"] != nil || !strings.Contains(payload["text"].(string), "https://workbench.example/investment?tab=monitor") {
			t.Error("incorrect Telegram content", payload)
		}
		return telegramResponse(200, `{"ok":true,"result":{"message_id":42}}`), nil
	})
	note := telegramNotice(*now)
	if err := channel.Deliver(context.Background(), note); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"todo", "old", "disabled-module", "disabled-channel"} {
		other := note
		switch kind {
		case "todo":
			other.SourceModule = "todo"
		case "old":
			other.CreatedAt = now.Add(-time.Hour)
		case "disabled-module":
			channel.allowed = func(contracts.ModuleID) bool { return false }
		case "disabled-channel":
			channel.allowed = nil
			_ = channel.save(context.Background(), "telegram", telegramConfig{})
		}
		if err := channel.Deliver(context.Background(), other); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("forwarded unselected notifications", calls)
	}
	w := httptest.NewRecorder()
	channel.Settings(w, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(w.Body.String(), "private-token") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("settings leaked or cached credentials")
	}
}

func TestTelegramDurableRetryAcrossChannelRestart(t *testing.T) {
	channel, service, now := setupTelegram(t)
	_, err := service.Create(context.Background(), contracts.NewNotification{SourceModule: "investment", Severity: contracts.NotificationWarning, Title: "Price alert", Content: "AAPL", IdempotencyKey: "investment:monitor:rule:day"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	transport := telegramTransport(func(*http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return telegramResponse(503, `{"ok":false,"description":"do not expose private-token"}`), nil
		}
		return telegramResponse(200, `{"ok":true}`), nil
	})
	channel.client.Transport = transport
	run := func(current *Telegram) {
		dispatcher, err := events.NewDispatcher(current.db, events.NewStore(current.db), []contracts.EventConsumer{current.Consumer()}, func(contracts.ModuleID) bool { return true }, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.DispatchOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	run(channel)
	var state, lastError string
	if err := channel.db.QueryRow("SELECT status,last_error FROM event_deliveries WHERE consumer_id=?", telegramConsumerID).Scan(&state, &lastError); err != nil || state != "retry" || strings.Contains(lastError, "private-token") {
		t.Fatal(state, lastError, err)
	}
	if _, err := channel.db.Exec("UPDATE event_deliveries SET next_attempt_at=0 WHERE consumer_id=?", telegramConsumerID); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(5 * time.Second)
	restarted := NewTelegram(channel.db, channel.publicURL, nil, nil)
	restarted.now = channel.now
	restarted.client.Transport = transport
	run(restarted)
	run(restarted)
	if err := channel.db.QueryRow("SELECT status FROM event_deliveries WHERE consumer_id=?", telegramConsumerID).Scan(&state); err != nil || state != "succeeded" || calls != 2 {
		t.Fatal("persistent retry/ack failed", state, calls, err)
	}
}

func TestTelegramRateLimitIsolatedBetweenBotsAndTestDoesNotSave(t *testing.T) {
	channel, _, now := setupTelegram(t)
	calls := 0
	channel.client.Transport = telegramTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if strings.Contains(r.URL.Path, "bot456:") {
			return telegramResponse(429, `{"ok":false,"error_code":429,"parameters":{"retry_after":3600}}`), nil
		}
		return telegramResponse(200, `{"ok":true}`), nil
	})
	w := httptest.NewRecorder()
	channel.TestSettings(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"enabled":true,"botToken":"456:unsaved-token","chatId":"12345"}`)))
	if w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := channel.Deliver(context.Background(), telegramNotice(*now)); err != nil {
		t.Fatal("test bot rate limit leaked to saved bot", err)
	}
	var config telegramConfig
	_ = channel.load(context.Background(), "telegram", &config)
	if config.BotToken != "123:private-token" || calls != 2 {
		t.Fatal("test changed saved settings")
	}
	err := channel.sendRateLimited(context.Background(), telegramConfig{BotToken: "456:unsaved-token", ChatID: "12345"}, "test")
	var retry contracts.RetryAfterError
	if !errors.As(err, &retry) || retry.RetryAfter() != time.Hour || calls != 2 {
		t.Fatal("ignored bot-specific Retry-After", err)
	}
}

func TestTelegramExpiredRetryStopsAsFailure(t *testing.T) {
	channel, service, now := setupTelegram(t)
	_, err := service.Create(context.Background(), contracts.NewNotification{SourceModule: "investment", Severity: contracts.NotificationWarning, Title: "Price alert", Content: "AAPL", IdempotencyKey: "investment:monitor:rule:day"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	channel.client.Transport = telegramTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return telegramResponse(429, `{"ok":false,"parameters":{"retry_after":3600}}`), nil
	})
	dispatcher, err := events.NewDispatcher(channel.db, events.NewStore(channel.db), []contracts.EventConsumer{channel.Consumer()}, func(contracts.ModuleID) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(16 * time.Minute)
	_, _ = channel.db.Exec("UPDATE event_deliveries SET next_attempt_at=0 WHERE consumer_id=?", telegramConsumerID)
	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := channel.db.QueryRow("SELECT status FROM event_deliveries WHERE consumer_id=?", telegramConsumerID).Scan(&state); err != nil || state != "dead" || calls != 1 {
		t.Fatal("expired unsent alert reported successful", state, calls, err)
	}
}

func TestTelegramStatusFailureDoesNotResendSuccess(t *testing.T) {
	channel, _, now := setupTelegram(t)
	_, err := channel.db.Exec(`CREATE TRIGGER reject_telegram_status BEFORE INSERT ON workspace_settings WHEN NEW.section='telegram_status' BEGIN SELECT RAISE(FAIL,'status fixture failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	channel.client.Transport = telegramTransport(func(*http.Request) (*http.Response, error) { return telegramResponse(200, `{"ok":true}`), nil })
	if err := channel.Deliver(context.Background(), telegramNotice(*now)); err != nil {
		t.Fatal("auxiliary status failure must not retry a sent message", err)
	}
}

func TestTelegramSettingsRetentionClearAndActivation(t *testing.T) {
	channel, _, now := setupTelegram(t)
	put := func(body string, want int) {
		w := httptest.NewRecorder()
		channel.SaveSettings(w, httptest.NewRequest("PUT", "/", strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("settings %d: %s", w.Code, w.Body.String())
		}
	}
	put(`{"enabled":true,"botToken":"","chatId":"-100123"}`, 200)
	var config telegramConfig
	_ = channel.load(context.Background(), "telegram", &config)
	if config.BotToken != "123:private-token" || config.ActivatedAt != now.Add(-time.Minute).UnixMilli() {
		t.Fatal("blank token/reset changed routing")
	}
	put(`{"enabled":true,"chatId":"../../bad"}`, 400)
	put(`{"enabled":true,"chatId":"0"}`, 400)
	put(`{"enabled":true,"botToken":"bad/token","chatId":"123"}`, 400)
	put(`{"enabled":true,"clearBotToken":true,"chatId":"123"}`, 400)
	put(`{"enabled":false,"clearBotToken":true,"chatId":"123"}`, 200)
	_ = channel.load(context.Background(), "telegram", &config)
	if config.Enabled || config.BotToken != "" {
		t.Fatal("clear token failed")
	}
	put(`{"enabled":true,"botToken":"789:new-token","chatId":"123"}`, 200)
	_ = channel.load(context.Background(), "telegram", &config)
	if config.ActivatedAt != now.UnixMilli() {
		t.Fatal("reenable must not replay disabled-period alerts")
	}
}

func TestTelegramTransportAndPermanentErrorsAreSanitized(t *testing.T) {
	channel, _, _ := setupTelegram(t)
	for _, code := range []int{0, 400, 401, 403, 404} {
		channel.client.Transport = telegramTransport(func(*http.Request) (*http.Response, error) {
			if code == 0 {
				return nil, errors.New("https://api.telegram.org/bot123:private-token/sendMessage")
			}
			return telegramResponse(code, `{"ok":false,"description":"private-token upstream leak"}`), nil
		})
		err := channel.send(context.Background(), telegramConfig{BotToken: "123:private-token", ChatID: "123"}, "hello")
		if err == nil || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "https://") {
			t.Fatal("unsafe upstream error", err)
		}
		if code != 0 {
			var permanent contracts.PermanentDeliveryError
			if !errors.As(err, &permanent) || !permanent.Permanent() {
				t.Fatal("permanent error will be retried", err)
			}
		}
	}
	if got := telegramText(strings.Repeat("😀", 2100)); len([]rune(got)) != 2001 {
		t.Fatal("UTF-16 text limit not respected")
	}
}
