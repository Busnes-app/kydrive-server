package migrations

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/testdb"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

func TestCaseDuplicateUsernamesRefuseTheUniqueIndex(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	name := cfg.Driver
	if name == "postgres" {
		name = "pgx"
	}
	db, err := sql.Open(name, cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := run(ctx, db, cfg.Driver, registry[:7]); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, u := range []string{"Alice", "alice", "bob"} {
		q := "INSERT INTO users (id, username, created_at, updated_at) VALUES (?, ?, ?, ?)"
		if cfg.Driver == "postgres" {
			q = "INSERT INTO users (id, username, created_at, updated_at) VALUES ($1, $2, $3, $4)"
		}
		if _, err := db.ExecContext(ctx, q, "u"+string(rune('0'+i)), u, now, now); err != nil {
			t.Fatal(err)
		}
	}
	err = Run(ctx, db, cfg.Driver)
	if err == nil || !strings.Contains(err.Error(), `"Alice", "alice"`) || strings.Contains(err.Error(), "bob") {
		t.Fatalf("duplicate usernames not refused by name: %v", err)
	}
	var applied int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM schema_migrations WHERE version = 8").Scan(&applied); err != nil || applied != 0 {
		t.Fatalf("refused migration recorded: %d %v", applied, err)
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM users WHERE username = 'alice'"); err != nil {
		t.Fatal(err)
	}
	if err := Run(ctx, db, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	q := "INSERT INTO users (id, username, created_at, updated_at) VALUES ('u9', 'BOB', ?, ?)"
	if cfg.Driver == "postgres" {
		q = "INSERT INTO users (id, username, created_at, updated_at) VALUES ('u9', 'BOB', $1, $2)"
	}
	if _, err := db.ExecContext(ctx, q, now, now); err == nil {
		t.Fatal("case-insensitive duplicate accepted after migration")
	}
}

// Two processes starting on one SQLite file both succeed and apply each migration once.
func TestConcurrentRunsApplyEachMigrationOnce(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "race.db") + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"
	// The file is already WAL, as after the first server start; switching it is not under test.
	setup, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err = setup.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	setup.Close()
	errs := make(chan error, 2)
	for range 2 {
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		go func() { errs <- Run(ctx, db, "sqlite") }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows, versions int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*), COUNT(DISTINCT version) FROM schema_migrations").Scan(&rows, &versions); err != nil || rows != len(registry) || versions != len(registry) {
		t.Fatalf("applied %d rows, %d versions, want %d: %v", rows, versions, len(registry), err)
	}
}
