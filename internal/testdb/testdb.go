// Package testdb hands tests an isolated database configuration.
//
// Default backend is a throwaway SQLite file. Set KY_TEST_POSTGRES_DSN to run
// the same tests against a real Postgres instance; each caller gets its own
// schema, dropped on cleanup.
package testdb

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/qmark"
	"github.com/google/uuid"
)

// Key is the deployment key tests pass to store.Open.
var Key = bytes.Repeat([]byte{7}, 32)

// Config returns a database config pointing at an isolated, empty database.
func Config(t *testing.T) config.DatabaseConfig {
	t.Helper()

	dsn := os.Getenv("KY_TEST_POSTGRES_DSN")
	if dsn == "" {
		return config.DatabaseConfig{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "test.db"),
		}
	}
	return postgresConfig(t, dsn)
}

// Open opens cfg's database directly, for tests that read or write rows themselves; it is
// closed on cleanup.
func Open(t testing.TB, cfg config.DatabaseConfig) *sql.DB {
	t.Helper()
	var db *sql.DB
	var err error
	if cfg.Driver == "postgres" {
		db, err = qmark.Open(cfg.DSN)
	} else {
		db, err = sql.Open("sqlite", cfg.DSN+"?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	}
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Driver != "postgres" {
		db.SetMaxOpenConns(1)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// Postgres reports whether tests run against PostgreSQL.
func Postgres() bool { return os.Getenv("KY_TEST_POSTGRES_DSN") != "" }

// SQLiteOnly skips t on PostgreSQL; why names what only SQLite has.
func SQLiteOnly(t testing.TB, why string) {
	t.Helper()
	if Postgres() {
		t.Skip("SQLite only: " + why)
	}
}

func postgresConfig(t *testing.T, dsn string) config.DatabaseConfig {
	t.Helper()

	// The "pgx" driver is registered by the store package, which every caller imports.
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("testdb: open postgres: %v", err)
	}

	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("testdb: create schema %s: %v", schema, errors.Join(err, admin.Close()))
	}

	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Logf("testdb: drop schema %s: %v", schema, err)
		}
		if err := admin.Close(); err != nil {
			t.Logf("testdb: close: %v", err)
		}
	})

	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}

	return config.DatabaseConfig{
		Driver: "postgres",
		DSN:    dsn + sep + "search_path=" + schema,
	}
}
