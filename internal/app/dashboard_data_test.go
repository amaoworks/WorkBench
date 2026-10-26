package app

import (
	"encoding/json"
	"testing"
)

func TestDashboardIncludeDataEmbedsWidgetResponses(t *testing.T) {
	a := newTestApp(t)
	token, cookies := csrf(t, a.Handler())
	if got := request(t, a.Handler(), "POST", "/api/modules/todo/tasks", []byte(`{"title":"Pay rent","dueAt":"2020-01-01T00:00:00Z"}`), token, cookies); got.Code != 201 {
		t.Fatal(got.Body.String())
	}
	plain := request(t, a.Handler(), "GET", "/api/dashboard", nil, "", cookies)
	var withoutData struct {
		Widgets []map[string]json.RawMessage `json:"widgets"`
	}
	if err := json.Unmarshal(plain.Body.Bytes(), &withoutData); err != nil {
		t.Fatal(err)
	}
	for _, widget := range withoutData.Widgets {
		if _, ok := widget["data"]; ok {
			t.Fatalf("data must be opt-in: %s", plain.Body.String())
		}
	}
	got := request(t, a.Handler(), "GET", "/api/dashboard?include=data", nil, "", cookies)
	if got.Code != 200 || got.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("dashboard: %d %v", got.Code, got.Header())
	}
	var body struct {
		Widgets []struct {
			ID   string          `json:"id"`
			Data json.RawMessage `json:"data"`
		} `json:"widgets"`
	}
	if err := json.Unmarshal(got.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	data := map[string]json.RawMessage{}
	for _, widget := range body.Widgets {
		data[widget.ID] = widget.Data
	}
	var today struct {
		Total int `json:"total"`
		Items []struct {
			Title string `json:"title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data["todo.today"], &today); err != nil || today.Total != 1 || today.Items[0].Title != "Pay rent" {
		t.Fatalf("todo.today data: %s %v", data["todo.today"], err)
	}
	var snapshot struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data["investment.snapshot"], &snapshot); err != nil || snapshot.Status != "disconnected" {
		t.Fatalf("snapshot data: %s %v", data["investment.snapshot"], err)
	}
	var unread struct {
		Count *int `json:"count"`
	}
	if err := json.Unmarshal(data["notifications.summary"], &unread); err != nil {
		t.Fatalf("notifications data: %s", data["notifications.summary"])
	}
}
