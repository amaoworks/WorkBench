package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"workbench/internal/contracts"
)

const defaultBusyTimeout = 5 * time.Second

var migrationNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type Config struct {
	Path           string
	BusyTimeout    time.Duration
	MaxConnections int
}

type Database struct {
	db   *sql.DB
	lock *flock.Flock
	path string
}

func Open(ctx context.Context, cfg Config) (*Database, error) {
	if cfg.Path == "" {
		return nil, errors.New("database path is required")
	}
	if cfg.BusyTimeout <= 0 {
		cfg.BusyTimeout = defaultBusyTimeout
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 4
	}

	absPath, err := filepath.Abs(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	fileLock := flock.New(absPath + ".lock")
	locked, err := fileLock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return nil, fmt.Errorf("lock database: %w", err)
	}
	if !locked {
		return nil, fmt.Errorf("database %q is already in use", absPath)
	}

	db, err := sql.Open("sqlite", dataSourceName(absPath, cfg.BusyTimeout))
	if err != nil {
		_ = fileLock.Unlock()
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(cfg.MaxConnections)
	db.SetMaxIdleConns(cfg.MaxConnections)
	db.SetConnMaxLifetime(0)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		_ = fileLock.Unlock()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if err := os.Chmod(absPath, 0o600); err != nil {
		_ = db.Close()
		_ = fileLock.Unlock()
		return nil, fmt.Errorf("secure database permissions: %w", err)
	}

	return &Database{db: db, lock: fileLock, path: absPath}, nil
}

func dataSourceName(path string, busyTimeout time.Duration) string {
	values := make(url.Values)
	values.Add("_pragma", "foreign_keys(1)")
	values.Add("_pragma", "journal_mode(WAL)")
	values.Add("_pragma", "synchronous(FULL)")
	values.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeout.Milliseconds()))
	values.Add("_txlock", "immediate")
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: values.Encode()}).String()
}

func (d *Database) SQL() *sql.DB { return d.db }
func (d *Database) Path() string { return d.path }

func (d *Database) Close() error {
	var errs []error
	if d.db != nil {
		errs = append(errs, d.db.Close())
	}
	if d.lock != nil {
		errs = append(errs, d.lock.Unlock())
	}
	return errors.Join(errs...)
}

func (d *Database) MigrateCore(ctx context.Context) error {
	return migrate(ctx, d.db, coreMigrations, "migrations", "goose_core_version")
}

func (d *Database) MigrateModule(ctx context.Context, id contracts.ModuleID, set contracts.MigrationSet) error {
	name := string(id)
	if !migrationNamePattern.MatchString(name) {
		return fmt.Errorf("invalid module id for migration table: %q", id)
	}
	if set.FS == nil {
		return nil
	}
	dir := set.Dir
	if dir == "" {
		dir = "."
	}
	return migrate(ctx, d.db, set.FS, dir, "goose_module_"+name+"_version")
}

func migrate(ctx context.Context, db *sql.DB, source fs.FS, dir, table string) error {
	sub, err := fs.Sub(source, dir)
	if err != nil {
		return fmt.Errorf("open migration directory %q: %w", dir, err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, sub, goose.WithTableName(table))
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

func (d *Database) Backup(ctx context.Context, destination string) error {
	absDestination, err := filepath.Abs(destination)
	if err != nil {
		return fmt.Errorf("resolve backup path: %w", err)
	}
	if _, err := os.Stat(absDestination); err == nil {
		return fmt.Errorf("backup destination already exists: %q", absDestination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect backup destination: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absDestination), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}

	quoted := strings.ReplaceAll(absDestination, "'", "''")
	if _, err := d.db.ExecContext(ctx, "VACUUM INTO '"+quoted+"'"); err != nil {
		return fmt.Errorf("backup database: %w", err)
	}
	if err := IntegrityCheck(ctx, absDestination); err != nil {
		return fmt.Errorf("verify backup: %w", err)
	}
	return os.Chmod(absDestination, 0o600)
}

func IntegrityCheck(ctx context.Context, path string) error {
	values := make(url.Values)
	values.Set("mode", "ro")
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: values.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("integrity_check returned %q", result)
	}
	return nil
}
