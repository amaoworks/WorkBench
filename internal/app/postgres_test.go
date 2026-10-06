package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"workbench/internal/capabilities/notifications"
	"workbench/internal/contracts"
	"workbench/internal/foundation/auth"
	workbenchdb "workbench/internal/foundation/database"
	"workbench/internal/foundation/database/sqlc"
	"workbench/internal/foundation/events"
	investmentsqlc "workbench/internal/modules/investment/sqlc"
)

// The supplied test database is isolated with a fresh schema, dropped on cleanup.
func postgresTestConfig(t *testing.T) Config {
	t.Helper()
	dsn := os.Getenv("WORKBENCH_TEST_POSTGRES_URL")
	if dsn == "" {
		t.Skip("set WORKBENCH_TEST_POSTGRES_URL to run PostgreSQL integration tests")
	}
	connection, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL test URL")
	}
	admin := stdlib.OpenDB(*connection)
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("workbench_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec("CREATE SCHEMA " + quoted); err != nil {
		t.Fatalf("create isolated PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + quoted + " CASCADE"); err != nil {
			t.Errorf("drop PostgreSQL test schema: %v", err)
		}
	})
	parsed, err := url.Parse(dsn)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("PostgreSQL integration tests require a postgres:// or postgresql:// URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return Config{
		ListenAddress: "127.0.0.1:8080", DataPath: filepath.Join(t.TempDir(), "data.db"),
		DBDriver: workbenchdb.Postgres, DatabaseURL: parsed.String(),
		AuthMode: auth.ModeLocal, ShutdownGrace: time.Second,
	}
}

func TestPostgresWorkspace(t *testing.T) {
	cfg := postgresTestConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	application, err := New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	db := application.DB()

	t.Run("single_process_lock", func(t *testing.T) {
		duplicate, err := workbenchdb.Open(ctx, workbenchdb.Config{Driver: workbenchdb.Postgres, DSN: cfg.DatabaseURL})
		if err == nil {
			_ = duplicate.Close()
			t.Fatal("a second process acquired the same workspace")
		}
		if !strings.Contains(err.Error(), "already in use") {
			t.Fatal(err)
		}
	})
	t.Run("all_generated_queries", func(t *testing.T) {
		assertPostgresQueriesPrepare(t, ctx, db)
	})
	t.Run("prepared_transaction_and_rollback", func(t *testing.T) {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		var isolation string
		if err := tx.QueryRowContext(ctx, "SHOW transaction_isolation").Scan(&isolation); err != nil || isolation != "serializable" {
			t.Fatalf("transaction isolation = %q, error = %v", isolation, err)
		}
		stmt, err := tx.PrepareContext(ctx, "SELECT CAST(?1 AS BIGINT) + CAST(?1 AS BIGINT), '?'::TEXT")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var doubled int64
		var literal string
		if err := stmt.QueryRowContext(ctx, int64(1<<40)).Scan(&doubled, &literal); err != nil || doubled != 1<<41 || literal != "?" {
			t.Fatalf("prepared query: %d, %q, %v", doubled, literal, err)
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO todo_tasks(id,title,description,created_at,updated_at) VALUES(?, ?, '', ?, ?)", "rolled-back", "Rollback", time.Now().UnixMilli(), time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		_, err = events.NewStore(db).PublishTx(ctx, tx, contracts.NewEvent{Topic: "todo.task.created", SourceModule: "todo", SchemaVersion: 1, AggregateID: "rolled-back"})
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM todo_tasks WHERE id = ?) + (SELECT COUNT(*) FROM events_log WHERE aggregate_id = ?)", "rolled-back", "rolled-back").Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback left %d rows: %v", count, err)
		}
	})

	token, cookies := csrf(t, application.Handler())
	call := func(method, route, body string, status int) *bytes.Buffer {
		t.Helper()
		response := request(t, application.Handler(), method, route, []byte(body), token, cookies)
		if response.Code != status {
			t.Fatalf("%s %s: status %d, body %s", method, route, response.Code, response.Body.String())
		}
		return response.Body
	}
	t.Run("business_http_and_pagination", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			call("POST", "/api/modules/todo/tasks", fmt.Sprintf(`{"title":"Task %d","dueAt":"2026-09-01T12:00:00Z"}`, i), 201)
		}
		seen := map[string]bool{}
		cursor := ""
		for i := 0; i < 4; i++ {
			body := call("GET", "/api/modules/todo/tasks?limit=1&cursor="+url.QueryEscape(cursor), "", 200)
			var page struct {
				Items []struct {
					ID string `json:"id"`
				} `json:"items"`
				NextCursor string `json:"nextCursor"`
			}
			if err := json.Unmarshal(body.Bytes(), &page); err != nil || len(page.Items) != 1 || seen[page.Items[0].ID] {
				t.Fatalf("invalid or repeated task page: %s, %v", body, err)
			}
			seen[page.Items[0].ID] = true
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
		if len(seen) != 3 || cursor != "" {
			t.Fatal("pagination did not return each task exactly once")
		}
		call("GET", "/api/modules/todo/widget/summary", "", 200)
		call("GET", "/api/dashboard", "", 200)
		call("POST", "/api/modules/investment/monitor/rules", `{"symbol":"AAPL","direction":"up","thresholdPercent":1.23,"enabled":true}`, 201)
		call("GET", "/api/modules/investment/monitor", "", 200)
		watchlists := `{"revision":0,"writer":"postgres-test","sequence":1,"state":{"lists":[{"id":"main","title":"Main","symbols":["AAPL"]}],"activeId":"main"}}`
		call("PUT", "/api/modules/investment/watchlists", watchlists, 200)
		call("PUT", "/api/modules/investment/watchlists", watchlists, 200)
		call("GET", "/api/modules/investment/watchlists", "", 200)
		preference := `{"name":"account","revision":0,"writer":"postgres-test","sequence":1,"value":"account-B"}`
		call("PUT", "/api/modules/investment/preferences", preference, 200)
		call("PUT", "/api/modules/investment/preferences", preference, 200)
		call("PUT", "/api/modules/investment/preferences", `{"name":"account","revision":1,"writer":"other","sequence":1,"value":"account-A"}`, 200)
		call("PUT", "/api/modules/investment/preferences", preference, 409)
		prefs := call("GET", "/api/modules/investment/preferences", "", 200)
		if !bytes.Contains(prefs.Bytes(), []byte(`"value":"account-A"`)) {
			t.Fatalf("preference did not round trip through PostgreSQL: %s", prefs)
		}
		call("PUT", "/api/settings/logging", `{"level":"warn"}`, 200)
		settings := call("GET", "/api/settings", "", 200)
		if !bytes.Contains(settings.Bytes(), []byte(`"databaseDriver":"postgres"`)) || !bytes.Contains(settings.Bytes(), []byte(`"backupSupported":false`)) || bytes.Contains(settings.Bytes(), []byte(cfg.DatabaseURL)) {
			t.Fatalf("invalid PostgreSQL deployment settings: %s", settings)
		}
		call("POST", "/api/system/backup", "{}", 501)
	})
	t.Run("bytea_and_double_precision", func(t *testing.T) {
		queries := dbsqlc.New(db)
		data := []byte{0, 1, 255, 0}
		if err := queries.UpsertSession(ctx, dbsqlc.UpsertSessionParams{Token: "binary-session", Data: data, Expiry: time.Now().Add(time.Hour).UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		var stored []byte
		if err := db.QueryRowContext(ctx, "SELECT data FROM sessions WHERE token = ?", "binary-session").Scan(&stored); err != nil || !bytes.Equal(stored, data) {
			t.Fatalf("binary session = %v, %v", stored, err)
		}
		investment := investmentsqlc.New(db)
		bar := investmentsqlc.UpsertFutuBarParams{Symbol: "AAPL", Resolution: "1", TimeMs: time.Now().UnixMilli(), Open: 123.123456789, High: 124, Low: 122, Close: 123, Volume: 1e12}
		if err := investment.UpsertFutuBar(ctx, bar); err != nil {
			t.Fatal(err)
		}
		bars, err := investment.ListFutuBars(ctx, investmentsqlc.ListFutuBarsParams{Symbol: bar.Symbol, Resolution: bar.Resolution, TimeMs: bar.TimeMs, TimeMs_2: bar.TimeMs + 1})
		if err != nil || len(bars) != 1 || bars[0].Open != bar.Open || bars[0].Volume != bar.Volume {
			t.Fatalf("bar precision: %+v, %v", bars, err)
		}
	})
	t.Run("notifications_and_events", func(t *testing.T) {
		service, err := notifications.NewService(db, events.NewStore(db))
		if err != nil {
			t.Fatal(err)
		}
		input := contracts.NewNotification{SourceModule: "core", Severity: contracts.NotificationInfo, Title: "PG notification", IdempotencyKey: "pg-notification"}
		first, err := service.Create(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		second, err := service.Create(ctx, input)
		if err != nil || first.ID != second.ID {
			t.Fatalf("notification idempotency failed: %v", err)
		}
		if err := service.MarkRead(ctx, []string{first.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.List(ctx, contracts.NotificationQuery{}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if err := application.dispatcher.DispatchOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if err := application.telegramDispatcher.DispatchOnce(ctx); err != nil {
				t.Fatal(err)
			}
		}
		store := events.NewStore(db)
		consumer := contracts.EventConsumer{ID: "postgres.retry", Module: "core", Topics: []string{"core.notification.created"}, Timeout: time.Second, MaxAttempts: 3, Handler: func(context.Context, contracts.Event) error { return errors.New("retry delivery") }}
		dispatcher, err := events.NewDispatcher(db, store, []contracts.EventConsumer{consumer}, func(contracts.ModuleID) bool { return true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		if err := dispatcher.DispatchOnce(ctx); err != nil {
			t.Fatal(err)
		}
		var nextAttempt int64
		if err := db.QueryRowContext(ctx, "SELECT next_attempt_at FROM event_deliveries WHERE consumer_id = ? AND status = 'retry'", consumer.ID).Scan(&nextAttempt); err != nil || nextAttempt < time.Now().UnixMilli() {
			t.Fatalf("retry timestamp: %d, %v", nextAttempt, err)
		}
		if err := maintenanceJob(db).Handler(ctx, contracts.JobRun{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("conflicting_updates_do_not_overwrite", func(t *testing.T) {
		first, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer first.Rollback()
		second, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer second.Rollback()
		var id, other string
		if err := first.QueryRowContext(ctx, "SELECT id FROM todo_tasks ORDER BY id LIMIT 1").Scan(&id); err != nil {
			t.Fatal(err)
		}
		if err := second.QueryRowContext(ctx, "SELECT id FROM todo_tasks ORDER BY id LIMIT 1").Scan(&other); err != nil || other != id {
			t.Fatal("could not read the same initial snapshot", err)
		}
		if _, err := first.ExecContext(ctx, "UPDATE todo_tasks SET title = ? WHERE id = ?", "Winner", id); err != nil {
			t.Fatal(err)
		}
		if err := first.Commit(); err != nil {
			t.Fatal(err)
		}
		_, err = second.ExecContext(ctx, "UPDATE todo_tasks SET title = ? WHERE id = ?", "Stale", id)
		var pgError *pgconn.PgError
		if !errors.As(err, &pgError) || pgError.Code != "40001" {
			t.Fatalf("conflicting snapshot was not rejected: %v", err)
		}
	})
	t.Run("restart", func(t *testing.T) {
		if err := application.Close(); err != nil {
			t.Fatal(err)
		}
		restarted, err := New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.Close()
		var count int
		if err := restarted.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM todo_tasks").Scan(&count); err != nil || count != 3 {
			t.Fatalf("restart lost tasks: %d, %v", count, err)
		}
	})
}

func TestPostgresPasswordChangeInvalidatesSessionsAndPersists(t *testing.T) {
	cfg := postgresTestConfig(t)
	cfg.AuthMode, cfg.Password = auth.ModePassword, "Oldpass1"
	assertPasswordChangeInvalidatesSessionsAndPersists(t, cfg)
}

func TestPostgresWorkspaceLockLossBlocksExistingConnections(t *testing.T) {
	cfg := postgresTestConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	workspace, err := workbenchdb.Open(ctx, workbenchdb.Config{Driver: workbenchdb.Postgres, DSN: cfg.DatabaseURL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	db := workspace.SQL()
	if _, err := db.ExecContext(ctx, "CREATE TABLE guard_check (id BIGINT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	stmt, err := db.PrepareContext(ctx, "SELECT CAST(? AS BIGINT)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO guard_check(id) VALUES (?)", 1); err != nil {
		t.Fatal(err)
	}
	config, err := pgx.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		t.Fatal("invalid test URL")
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	var terminated bool
	err = admin.QueryRowContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_locks
		WHERE locktype = 'advisory' AND granted AND objsubid = 1
		AND classid::BIGINT = ((hashtextextended(current_database() || ':' || current_schema() || ':workbench', 0) >> 32) & 4294967295)
		AND objid::BIGINT = (hashtextextended(current_database() || ':' || current_schema() || ':workbench', 0) & 4294967295)
		AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&terminated)
	if err != nil || !terminated {
		t.Fatalf("could not terminate test lock session: %v", err)
	}
	assertLost := func(err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "lock connection was lost") {
			t.Fatalf("old instance was not fenced: %v", err)
		}
	}
	assertLost(db.PingContext(ctx))
	var value int
	assertLost(db.QueryRowContext(ctx, "SELECT 1").Scan(&value))
	_, err = db.ExecContext(ctx, "INSERT INTO guard_check(id) VALUES (?)", 2)
	assertLost(err)
	assertLost(stmt.QueryRowContext(ctx, 1).Scan(&value))
	_, err = db.BeginTx(ctx, nil)
	assertLost(err)
	assertLost(tx.Commit())
	replacement, err := workbenchdb.Open(ctx, workbenchdb.Config{Driver: workbenchdb.Postgres, DSN: cfg.DatabaseURL})
	if err != nil {
		t.Fatal("replacement could not acquire released lock", err)
	}
	defer replacement.Close()
	assertLost(db.PingContext(ctx))
	if err := replacement.SQL().QueryRowContext(ctx, "SELECT COUNT(*) FROM guard_check").Scan(&value); err != nil || value != 0 {
		t.Fatalf("old instance committed after losing lock: %d, %v", value, err)
	}
}

func assertPostgresQueriesPrepare(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	count := 0
	for _, dir := range []string{"../foundation/database/sqlc", "../modules/todo/sqlc", "../modules/investment/sqlc", "../modules/news/sqlc"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.sql.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				spec, ok := node.(*ast.ValueSpec)
				if !ok || len(spec.Values) != 1 {
					return true
				}
				literal, ok := spec.Values[0].(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				query, err := strconv.Unquote(literal.Value)
				if err != nil || !strings.HasPrefix(query, "-- name:") {
					return true
				}
				stmt, err := db.PrepareContext(ctx, query)
				if err != nil {
					t.Errorf("PostgreSQL query %s in %s: %v", spec.Names[0], path, err)
				} else {
					_ = stmt.Close()
				}
				count++
				return false
			})
		}
	}
	if count == 0 {
		t.Fatal("no generated SQL queries were checked")
	}
	t.Logf("prepared %d generated queries against PostgreSQL", count)
}
