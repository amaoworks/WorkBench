package todo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWallosAmountConversion(t *testing.T) {
	currencies := []wallosCurrency{{ID: "1", Code: "USD", Rate: "1.1"}, {ID: "2", Code: "CNY", Rate: "7.92"}, {ID: "3", Code: "JPY", Rate: "150"}, {ID: "4", Code: "EUR", Rate: "0"}}
	for _, tc := range []struct{ price, currency, want string }{
		{"30", "1", "USD 30.00 -> ￥216.00"},
		{"30", "2", "CNY 30.00 -> ￥30.00"},
		{"1000", "3", "JPY 1000.00 -> ￥52.80"},
		{"0", "1", "USD 0.00 -> ￥0.00"},
		{"1", "4", "EUR 1.00 -> 人民币金额无法换算（Wallos 缺少有效汇率）"},
		{"30", "9", "30.00（币种未知） -> 人民币金额无法换算"},
		{"", "1", "金额未提供 -> 人民币金额无法换算"},
		{"-1", "1", "金额未提供 -> 人民币金额无法换算"},
	} {
		if got := wallosAmount(wallosSubscription{Price: json.Number(tc.price), CurrencyID: json.Number(tc.currency)}, currencies); got != tc.want {
			t.Errorf("%s/%s: %s", tc.price, tc.currency, got)
		}
	}
}

func TestWallosDetailsFetchAndExistingTaskRefresh(t *testing.T) {
	m, db := openTodoModule(t)
	ctx := context.Background()
	m.now = func() time.Time { return time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC) }
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" || r.URL.RawQuery != "" {
			t.Error("unsafe request")
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("api_key") != "secret" {
			t.Error("missing credential")
		}
		switch r.URL.Path {
		case "/wallos/api/subscriptions/get_subscriptions.php":
			if r.Form.Get("convert_currency") == "true" {
				t.Error("must retain original amount")
			}
			_, _ = w.Write([]byte(`{"success":true,"subscriptions":[{"id":1,"name":"Grok","next_payment":"2026-09-14","inactive":0,"category_name":"AI","payment_method_name":"Visa","price":"30.00","currency_id":1}]}`))
		case "/wallos/api/currencies/get_currencies.php":
			_, _ = w.Write([]byte(`{"success":true,"main_currency":3,"currencies":[{"id":1,"code":"USD","rate":"1.1"},{"id":2,"code":"CNY","rate":7.92},{"id":3,"code":"EUR","rate":1}]}`))
		default:
			t.Error("unexpected endpoint")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c := wallosTestConfig()
	c.BaseURL = server.URL + "/wallos"
	subs, err := fetchWallos(ctx, c)
	if err != nil || len(subs) != 1 || requests != 2 {
		t.Fatalf("fetch: %v, requests=%d", err, requests)
	}
	if n, err := m.importWallos(ctx, c, subs); err != nil || n != 1 {
		t.Fatalf("import: %d %v", n, err)
	}
	var id string
	if err := db.SQL().QueryRow("SELECT id FROM todo_tasks").Scan(&id); err != nil {
		t.Fatal(err)
	}
	before, err := m.queries.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	want := "分类: AI\n支付方式: Visa\n付款日期: 2026-09-14（Asia/Shanghai）\n金额: USD 30.00 -> ￥216.00\n请及时付款！"
	if before.Title != "Wallos续费提醒 - Grok" || before.Description != want {
		t.Fatalf("unexpected content: %#v", before)
	}
	// Simulate the previously generated format, including a completed task.
	if _, err := db.SQL().Exec("UPDATE todo_tasks SET title='续费提醒：Grok', description='来源：Wallos', completed_at=123 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	if n, err := m.importWallos(ctx, c, subs); err != nil || n != 0 {
		t.Fatalf("refresh: %d %v", n, err)
	}
	after, err := m.queries.GetTask(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Description != want || after.DueAt != before.DueAt || after.CompletedAt.Int64 != 123 {
		t.Fatalf("state not preserved: %#v", after)
	}
	var updates int
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM events_log WHERE topic='todo.task.updated'").Scan(&updates); err != nil || updates != 1 {
		t.Fatalf("update events: %d %v", updates, err)
	}
	if _, err := m.importWallos(ctx, c, subs); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL().QueryRow("SELECT COUNT(*) FROM events_log WHERE topic='todo.task.updated'").Scan(&updates); err != nil || updates != 1 {
		t.Fatalf("unchanged sync events: %d %v", updates, err)
	}
	if _, err := m.queries.DeleteTask(ctx, id); err != nil {
		t.Fatal(err)
	}
	if n, err := m.importWallos(ctx, c, subs); err != nil || n != 1 {
		t.Fatalf("deleted not recreated: %d %v", n, err)
	}
}
