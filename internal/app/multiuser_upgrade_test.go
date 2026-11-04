package app

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"

	"workbench/internal/foundation/auth"
	workbenchdb "workbench/internal/foundation/database"
)

func legacyMigrationFiles(t *testing.T, root, directory string, maxVersion int) fs.FS {
	t.Helper()
	source := os.DirFS(root)
	entries, err := fs.ReadDir(source, directory)
	if err != nil {
		t.Fatal(err)
	}
	result := fstest.MapFS{}
	for _, entry := range entries {
		version, err := strconv.Atoi(strings.SplitN(entry.Name(), "_", 2)[0])
		if err != nil || version > maxVersion {
			continue
		}
		data, err := fs.ReadFile(source, directory+"/"+entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		result[entry.Name()] = &fstest.MapFile{Data: data}
	}
	return result
}

func TestMultiUserUpgradeLegacyWorkspace(t *testing.T) {
	assertMultiUserUpgradeLegacyWorkspace(t, Config{
		ListenAddress: "127.0.0.1:8080", DataPath: filepath.Join(t.TempDir(), "data.db"),
		AuthMode: auth.ModeLocal, ShutdownGrace: time.Second,
	})
}

func TestPostgresMultiUserUpgradeLegacyWorkspace(t *testing.T) {
	assertMultiUserUpgradeLegacyWorkspace(t, postgresTestConfig(t))
}

func assertMultiUserUpgradeLegacyWorkspace(t *testing.T, cfg Config) {
	t.Helper()
	ctx := context.Background()
	db, err := workbenchdb.Open(ctx, workbenchdb.Config{Path: cfg.DataPath, Driver: cfg.DBDriver, DSN: cfg.DatabaseURL})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	directory, dialect := "migrations", goose.DialectSQLite3
	if db.Driver() == workbenchdb.Postgres {
		directory, dialect = "migrations-postgres", goose.DialectPostgres
	}
	for _, migration := range []struct {
		root, table string
		version     int
	}{
		{"../foundation/database", "goose_core_version", 9},
		{"../modules/todo", "goose_module_todo_version", 4},
		{"../modules/investment", "goose_module_investment_version", 13},
		{"../modules/news", "goose_module_news_version", 1},
	} {
		provider, err := goose.NewProvider(dialect, db.SQL(), legacyMigrationFiles(t, migration.root, directory, migration.version), goose.WithTableName(migration.table))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.Up(ctx); err != nil {
			t.Fatalf("legacy %s: %v", migration.table, err)
		}
	}
	for _, statement := range []string{
		`INSERT INTO auth_credentials(id,password_hash,updated_at,must_change_password) VALUES(1,'preserved-legacy-hash',1234,0)`,
		`INSERT INTO sessions(token,data,expiry) VALUES('legacy-token',NULL,2000000000000)`,
		`INSERT INTO todo_tasks(id,title,description,created_at,updated_at) VALUES('legacy-task','Legacy private task','',1,1)`,
		`INSERT INTO investment_schwab(id,app_key,app_secret,callback_url,updated_at) VALUES(1,'legacy-app','legacy-secret','https://example.test/oauth/schwab',1)`,
		`UPDATE investment_watchlists SET content='{"lists":[{"id":"main","title":"Legacy list","symbols":["AAPL"]}],"activeId":"main"}' WHERE id=1`,
		`INSERT INTO news_sources(id,name,kind,url,enabled) VALUES('legacy-source','Legacy source','rss','https://example.test/feed',0)`,
		`INSERT INTO news_articles(id,title,url,source_name,content,published_at,created_at) VALUES('legacy-article','Legacy private article','https://example.test/article','Legacy source','Body',1,1)`,
		`INSERT INTO ai_conversations(id,title,created_at,updated_at) VALUES('legacy-conversation','Legacy private chat',1,1)`,
		`INSERT INTO notifications(id,source_module,severity,title,content,idempotency_key,created_at) VALUES('legacy-notification','todo','info','Legacy reminder','Body','legacy-note-key',1)`,
	} {
		// Session bytes use a bound parameter to work with both database engines.
		if strings.Contains(statement, "INSERT INTO sessions") {
			_, err = db.SQL().Exec(`INSERT INTO sessions(token,data,expiry) VALUES('legacy-token',?,2000000000000)`, []byte("old-session"))
		} else {
			_, err = db.SQL().Exec(statement)
		}
		if err != nil {
			t.Fatalf("legacy fixture: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	a, err := New(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("upgrade legacy workspace: %v", err)
	}
	defer a.Close()
	for _, query := range []string{
		`SELECT COUNT(*) FROM auth_users WHERE id='owner' AND username='admin' AND password_hash='preserved-legacy-hash' AND role='admin'`,
		`SELECT COUNT(*) FROM todo_tasks WHERE id='legacy-task' AND user_id='owner'`,
		`SELECT COUNT(*) FROM news_articles WHERE id='legacy-article' AND user_id='owner'`,
		`SELECT COUNT(*) FROM ai_conversations WHERE id='legacy-conversation' AND user_id='owner'`,
		`SELECT COUNT(*) FROM notifications WHERE id='legacy-notification' AND recipient_id='owner'`,
	} {
		var count int
		if err := a.DB().QueryRow(query).Scan(&count); err != nil || count != 1 {
			t.Fatalf("migrated owner data (%s): count=%d error=%v", query, count, err)
		}
	}
	var sessions int
	if err := a.DB().QueryRow(`SELECT COUNT(*) FROM sessions WHERE token='legacy-token'`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("legacy sessions not revoked: count=%d error=%v", sessions, err)
	}
	for _, tc := range []struct{ route, want string }{
		{"/api/modules/todo/tasks", "Legacy private task"},
		{"/api/modules/investment/watchlists", "Legacy list"},
		{"/api/modules/news/articles", "Legacy private article"},
		{"/api/conversations", "Legacy private chat"},
		{"/api/notifications", "Legacy reminder"},
	} {
		result := request(t, a.Handler(), "GET", tc.route, nil, "", nil)
		if result.Code != 200 || !strings.Contains(result.Body.String(), tc.want) {
			t.Errorf("migrated API %s: %d %s", tc.route, result.Code, result.Body.String())
		}
	}
}
