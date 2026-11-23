package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// migratePostgres copies the configured SQLite database into the empty PostgreSQL database
// KY_MIGRATE_POSTGRES_DSN names. It runs with the SQLite deployment's own settings.
func migratePostgres(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("migrate-postgres", flag.ContinueOnError)
	fs.SetOutput(out)
	check := fs.Bool("check", false, "copy and verify, then roll the copy back")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	dsn := os.Getenv("KY_MIGRATE_POSTGRES_DSN")
	if dsn == "" {
		return errors.New("set KY_MIGRATE_POSTGRES_DSN to the empty PostgreSQL database; a password never goes in an argument")
	}
	cfg, err := config.LoadExisting()
	if err != nil {
		return err
	}
	if cfg.Database.Driver != "sqlite" {
		return errors.New("run migrate-postgres with the SQLite deployment's settings (KY_DB_DRIVER=sqlite)")
	}
	report, err := store.MigrateToPostgres(ctx, cfg.Database.SQLitePath(), dsn, cfg.Security.EncryptionKey, *check)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return err
	}
	done := "Copied and verified."
	if *check {
		done = "Verified and rolled back: PostgreSQL keeps KyDrive's schema and the rows a first start writes, which the migration replaces, but no copied rows."
	}
	_, err = fmt.Fprintln(out, done, "The SQLite database and settings are unchanged. Keep the same KY_ENCRYPTION_KEY (or encryption.key) and the data directory: files, the search index and backups stay there.")
	return err
}
