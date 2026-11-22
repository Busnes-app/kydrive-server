package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/qmark"
	_ "modernc.org/sqlite"
)

// Open initializes and returns the configured database store backend. key is the
// deployment key (Security.EncryptionKey); the SQLite drive store refuses to open without it.
func Open(ctx context.Context, cfg config.DatabaseConfig, key []byte) (Store, error) {
	driver := strings.ToLower(cfg.Driver)
	switch driver {
	case "sqlite", "sqlite3", "":
		return openSQLite(ctx, cfg, key)
	case "postgres", "postgresql", "pgx":
		return openPostgres(ctx, cfg, key)
	default:
		return nil, fmt.Errorf("unsupported database driver: %q (supported: sqlite, postgres)", cfg.Driver)
	}
}

func openSQLite(ctx context.Context, cfg config.DatabaseConfig, key []byte) (Store, error) {
	dsn := cfg.DSN
	filePath := dsn
	if idx := strings.Index(dsn, "?"); idx != -1 {
		filePath = dsn[:idx]
	}

	dir := filepath.Dir(filePath)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create sqlite directory %s: %w", dir, err)
		}
	}

	delim := "?"
	if strings.Contains(dsn, "?") {
		delim = "&"
	}
	dsn += delim + "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(FULL)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("failed to ping sqlite database: %w", err), db.Close())
	}

	if _, err := db.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return newSQLStore(ctx, db, "sqlite", key, drive.Tenancy{Mode: cmp.Or(cfg.Tenancy, drive.TenancySingle), StaffOrg: cfg.StaffOrg})
}

func openPostgres(ctx context.Context, cfg config.DatabaseConfig, key []byte) (Store, error) {
	maxOpen, maxIdle, maxLifetime := cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime
	db, err := qmark.Open(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open postgres database: %w", err)
	}

	if maxOpen <= 0 {
		maxOpen = 25
	}
	if maxIdle <= 0 {
		maxIdle = 5
	}
	if maxLifetime <= 0 {
		maxLifetime = 15 * time.Minute
	}

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(maxLifetime)

	if err := db.PingContext(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("failed to ping postgres database: %w", err), db.Close())
	}

	return newSQLStore(ctx, db, "postgres", key, drive.Tenancy{Mode: cmp.Or(cfg.Tenancy, drive.TenancySingle), StaffOrg: cfg.StaffOrg})
}
