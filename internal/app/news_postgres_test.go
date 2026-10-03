package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
)

func TestNewsPostgresWorkspace(t *testing.T) {
	cfg := postgresTestConfig(t)
	a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	token, cookies := csrf(t, a.Handler())
	capture := func(name, schema, method, path, body string, status int) json.RawMessage {
		t.Helper()
		r := request(t, a.Handler(), method, path, []byte(body), token, cookies)
		if r.Code != status {
			t.Fatalf("%s: %s %s returned %d: %s", name, method, path, r.Code, r.Body.String())
		}
		return append(json.RawMessage(nil), r.Body.Bytes()...)
	}
	captureNewsContracts(t, a, capture)
	assertPostgresQueriesPrepare(t, context.Background(), a.DB())
	var digests, notes, external int
	if err := a.DB().QueryRow("SELECT COUNT(*) FROM news_digests").Scan(&digests); err != nil {
		t.Fatal(err)
	}
	if err := a.DB().QueryRow("SELECT COUNT(*),COALESCE(MAX(external),0) FROM notifications WHERE source_module='news'").Scan(&notes, &external); err != nil || digests != 1 || notes != 1 || external != 1 {
		t.Fatal(digests, notes, external, err)
	}
	r := request(t, a.Handler(), http.MethodPut, "/api/modules/news/enabled", []byte(`{"enabled":false}`), token, cookies)
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	r = request(t, a.Handler(), http.MethodGet, "/api/modules/news/articles", nil, "", cookies)
	if r.Code != 503 {
		t.Fatal("disabled news was not gated", r.Code)
	}
}
