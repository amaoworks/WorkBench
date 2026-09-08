package todo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workbench/internal/contracts"
)

func wallosTestConfig() wallosConfig {
	return wallosConfig{Enabled: true, BaseURL: "https://wallos.example", APIKey: "secret", DaysBefore: 3, ReminderHour: 9, TimeZone: "Asia/Shanghai"}
}

func TestWallosOccurrenceLifecycle(t *testing.T) {
	m, db := openTodoModule(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 7, 1, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	c := wallosTestConfig()
	subs := []wallosSubscription{
		{ID: "1", Name: "月付服务", NextPayment: "2026-09-10", Inactive: "0"},
		{ID: "2", Name: "以后", NextPayment: "2026-11-11", Inactive: "0"},
		{ID: "3", Name: "已停用", NextPayment: "2026-09-10", Inactive: "1"},
	}
	now = now.Add(-time.Second)
	if n, err := m.importWallos(ctx, c, subs); err != nil || n != 1 {
		t.Fatalf("early: %d %v", n, err)
	}
	if err := m.scanDue(ctx, contracts.JobRun{}); err != nil {
		t.Fatal(err)
	}
	var earlyNotifications int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM notifications").Scan(&earlyNotifications); err != nil || earlyNotifications != 0 {
		t.Fatalf("notification before reminder time: %d %v", earlyNotifications, err)
	}
	now = now.Add(time.Second)
	if n, err := m.importWallos(ctx, c, subs); err != nil || n != 0 {
		t.Fatalf("first: %d %v", n, err)
	}
	var id string
	var due int64
	if err := db.SQL().QueryRow("SELECT id, due_at FROM todo_tasks").Scan(&id, &due); err != nil {
		t.Fatal(err)
	}
	if due != now.UnixMilli() {
		t.Fatalf("due = %d, want %d", due, now.UnixMilli())
	}
	complete := true
	if _, err := m.update(ctx, id, UpdateTask{Completed: &complete}); err != nil {
		t.Fatal(err)
	}
	if n, err := m.importWallos(ctx, c, subs); err != nil || n != 0 {
		t.Fatalf("completed: %d %v", n, err)
	}
	if _, err := m.queries.DeleteTask(ctx, id); err != nil {
		t.Fatal(err)
	}
	// A new module instance models an application restart; deduplication is durable.
	restarted, err := New(db.SQL(), m.events, m.notifications)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = m.now
	if n, err := restarted.importWallos(ctx, c, subs); err != nil || n != 1 {
		t.Fatalf("deleted/restarted: %d %v", n, err)
	}
	now = time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC)
	subs = subs[:1]
	subs[0].NextPayment = "2026-10-10"
	if n, err := restarted.importWallos(ctx, c, subs); err != nil || n != 1 {
		t.Fatalf("next cycle: %d %v", n, err)
	}
	for range 2 {
		if err := restarted.scanDue(ctx, contracts.JobRun{}); err != nil {
			t.Fatal(err)
		}
	}
	var notifications int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM notifications").Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if notifications != 2 {
		t.Fatalf("notifications = %d", notifications)
	}
}

func TestWallosTransactionRollback(t *testing.T) {
	for _, badEvent := range []bool{false, true} {
		t.Run(map[bool]string{false: "invalid subscription", true: "event failure"}[badEvent], func(t *testing.T) {
			m, db := openTodoModule(t)
			m.now = func() time.Time { return time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC) }
			subs := []wallosSubscription{{ID: "1", Name: "Test", NextPayment: "2026-09-10"}}
			if badEvent {
				m.events = failingPublisher{}
			} else {
				subs = append(subs, wallosSubscription{ID: "2", Name: "Invalid", NextPayment: "invalid"})
			}
			if _, err := m.importWallos(context.Background(), wallosTestConfig(), subs); err == nil {
				t.Fatal("expected failure")
			}
			for _, table := range []string{"todo_tasks", "todo_wallos_occurrences", "events_log"} {
				var n int
				if err := db.SQL().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 0 {
					t.Fatalf("%s has %d rows after rollback", table, n)
				}
			}
		})
	}
}

func TestWallosMonthlyListAndReminderBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, now, payment         string
		daysBefore                 int
		wantTask, wantNotification bool
	}{
		{"month end payment listed on first day", "2026-09-01T01:00:00Z", "2026-09-30", 3, true, false},
		{"next month not yet needed", "2026-09-01T01:00:00Z", "2026-10-01", 3, false, false},
		{"cross month before reminder", "2026-09-28T00:59:59Z", "2026-10-01", 3, false, false},
		{"cross month at reminder", "2026-09-28T01:00:00Z", "2026-10-01", 3, true, true},
		{"month boundary uses configured timezone", "2026-08-31T16:00:00Z", "2026-09-30", 3, true, false},
		{"before local month boundary", "2026-08-31T15:59:59Z", "2026-09-30", 3, false, false},
		{"same month number next year excluded", "2026-09-01T01:00:00Z", "2027-09-01", 3, false, false},
		{"year boundary reminder", "2026-12-29T01:00:00Z", "2027-01-01", 3, true, true},
		{"overdue payment still returned", "2026-09-01T01:00:00Z", "2026-08-31", 3, true, true},
		{"zero days no early notification", "2026-09-30T00:59:59Z", "2026-09-30", 0, true, false},
		{"zero days at payment reminder", "2026-09-30T01:00:00Z", "2026-09-30", 0, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, db := openTodoModule(t)
			now, err := time.Parse(time.RFC3339, tc.now)
			if err != nil {
				t.Fatal(err)
			}
			m.now = func() time.Time { return now }
			c := wallosTestConfig()
			c.DaysBefore = tc.daysBefore
			ctx := context.Background()
			n, err := m.importWallos(ctx, c, []wallosSubscription{{ID: "1", Name: "订阅", NextPayment: tc.payment}})
			if err != nil || (n == 1) != tc.wantTask {
				t.Fatalf("created=%d err=%v, want task=%v", n, err, tc.wantTask)
			}
			for range 2 {
				if err := m.scanDue(ctx, contracts.JobRun{}); err != nil {
					t.Fatal(err)
				}
			}
			var count int
			if err := db.SQL().QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count > 1 || (count == 1) != tc.wantNotification {
				t.Fatalf("notifications=%d, want notification=%v", count, tc.wantNotification)
			}
		})
	}
}

func TestWallosDSTReminderHour(t *testing.T) {
	m, db := openTodoModule(t)
	c := wallosTestConfig()
	c.TimeZone = "America/New_York"
	c.DaysBefore = 0
	m.now = func() time.Time { return time.Date(2026, 3, 8, 13, 0, 0, 0, time.UTC) }
	if n, err := m.importWallos(context.Background(), c, []wallosSubscription{{ID: "1", Name: "DST", NextPayment: "2026-03-08"}}); err != nil || n != 1 {
		t.Fatalf("DST: %d %v", n, err)
	}
	var due int64
	if err := db.SQL().QueryRow("SELECT due_at FROM todo_tasks").Scan(&due); err != nil {
		t.Fatal(err)
	}
	if due != m.now().UnixMilli() {
		t.Fatalf("DST reminder = %v", time.UnixMilli(due))
	}
}

func TestWallosHTTPContractAndFailure(t *testing.T) {
	for _, scenario := range []string{"success", "unauthorized", "redirect", "invalid", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/wallos/api/subscriptions/get_subscriptions.php" || r.Method != "POST" || r.URL.RawQuery != "" {
					t.Error("unexpected Wallos request")
				}
				if err := r.ParseForm(); err != nil || r.PostForm.Get("api_key") != "secret" || r.PostForm.Get("state") != "0" {
					t.Error("incorrect form")
				}
				switch scenario {
				case "success":
					_, _ = w.Write([]byte(`{"success":true,"subscriptions":[{"id":"1","name":"Service","next_payment":"2026-09-10","inactive":"0"}]}`))
				case "unauthorized":
					_, _ = w.Write([]byte(`{"success":false,"title":"secret"}`))
				case "redirect":
					w.Header().Set("Location", "/secret")
					w.WriteHeader(307)
				case "invalid":
					_, _ = w.Write([]byte(`{"success":true}`))
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat(" ", 4*1024*1024+1)))
				}
			}))
			defer s.Close()
			c := wallosTestConfig()
			c.BaseURL = s.URL + "/wallos"
			subs, err := fetchWallos(context.Background(), c)
			if scenario == "success" {
				if err != nil || len(subs) != 1 {
					t.Fatalf("%v %v", subs, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			if calls != 1 {
				t.Fatalf("requests = %d", calls)
			}
		})
	}
}

func TestWallosSettingsAndSyncStatus(t *testing.T) {
	m, _ := openTodoModule(t)
	c := wallosTestConfig()
	save := func(c wallosConfig) *httptest.ResponseRecorder {
		data, _ := json.Marshal(c)
		w := httptest.NewRecorder()
		m.saveWallos(w, httptest.NewRequest("PUT", "/", strings.NewReader(string(data))))
		return w
	}
	if w := save(c); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w := httptest.NewRecorder()
	m.getWallos(w, httptest.NewRequest("GET", "/", nil))
	if strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), `"hasApiKey":true`) {
		t.Fatal(w.Body.String())
	}
	c.APIKey = ""
	c.DaysBefore = 7
	if w := save(c); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	stored, err := m.loadWallos(context.Background())
	if err != nil || stored.APIKey != "secret" {
		t.Fatalf("key not retained: %v", err)
	}
	c.BaseURL = "https://other.example"
	if w := save(c); w.Code != 400 {
		t.Fatal("changing host reused key")
	}
	c.TimeZone = "invalid"
	c.APIKey = "new"
	if w := save(c); w.Code != 400 {
		t.Fatal("invalid timezone accepted")
	}
	fail := false
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(503)
		} else {
			_, _ = w.Write([]byte(`{"success":true,"subscriptions":[]}`))
		}
	}))
	defer s.Close()
	c = wallosTestConfig()
	c.BaseURL = s.URL
	if w := save(c); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, err := m.syncWallos(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err = m.loadWallos(context.Background())
	if err != nil || stored.LastSync == nil || stored.LastError != "" {
		t.Fatalf("success status missing: %v", err)
	}
	fail = true
	if _, err := m.syncWallos(context.Background()); err == nil {
		t.Fatal("expected sync failure")
	}
	stored, err = m.loadWallos(context.Background())
	if err != nil || stored.LastSync == nil || stored.LastError == "" {
		t.Fatalf("failure status missing: %v", err)
	}
	c.Enabled = false
	if w := save(c); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if n, err := m.syncWallos(context.Background()); err != nil || n != 0 {
		t.Fatalf("disabled sync: %d %v", n, err)
	}
}
