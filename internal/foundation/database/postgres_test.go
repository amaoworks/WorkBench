package database

import (
	"context"
	"strings"
	"testing"
)

func TestRebindPostgres(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"SELECT ?, ?, ?", "SELECT $1, $2, $3"},
		{"SELECT ?1, ?2, ?1, ?3", "SELECT $1, $2, $1, $3"},
		{"SELECT ?3, ?, ?1, ?", "SELECT $3, $4, $1, $5"},
		{"SELECT '?' AS \"?\", 'it''s ?' WHERE id = ?", "SELECT '?' AS \"?\", 'it''s ?' WHERE id = $1"},
		{"-- ?\nSELECT ? /* ? /* ? */ ? */ , ?", "-- ?\nSELECT $1 /* ? /* ? */ ? */ , $2"},
		{"SELECT $$?$$, $body$?$body$, ?", "SELECT $$?$$, $body$?$body$, $1"},
		{"SELECT E'escaped\\' ?' , ?", "SELECT E'escaped\\' ?' , $1"},
		{"SELECT $1, $2", "SELECT $1, $2"},
	} {
		if got := rebindPostgres(tc.input); got != tc.want {
			t.Errorf("rebindPostgres(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestPostgresConfigurationErrorsDoNotExposePassword(t *testing.T) {
	for _, dsn := range []string{"postgres://user:private-password@localhost:bad/db", "host=localhost password=private-password connect_timeout=bad"} {
		_, err := Open(context.Background(), Config{Driver: Postgres, DSN: dsn})
		if err == nil || strings.Contains(err.Error(), "private-password") {
			t.Fatalf("unsafe configuration error: %v", err)
		}
	}
	for _, cfg := range []Config{
		{Driver: "mysql", Path: "data.db"},
		{Driver: Postgres},
		{Driver: SQLite, Path: "data.db", DSN: "postgres://localhost/db"},
	} {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("accepted invalid configuration: %+v", cfg)
		}
	}
}
