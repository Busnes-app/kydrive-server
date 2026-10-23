package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Busnes-app/ky-primitives/keyfile"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

var errResetFailed = errors.New("restored, but access-grant reset failed")

// SQLite interprets '?' and '#' as URI delimiters rather than filename characters.
func checkRestorePath(dir string) error {
	if strings.ContainsAny(dir, "?#") {
		return errors.New("restore target contains '?' or '#'; choose a path without them")
	}
	return nil
}

// resetRestored never creates a deployment key or a missing database. Rerunning it safely
// finishes an extracted restore whose reset failed before committing.
func resetRestored(ctx context.Context, dataDir string) error {
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	if err := checkRestorePath(dataDir); err != nil {
		return err
	}
	path := filepath.Join(dataDir, "ky_server.db")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("no restored database at %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("restored database must be a nonempty regular file")
	}
	// Inspect read-only before Open can run migrations: an empty or unrelated SQLite file
	// must never turn into a newly initialized deployment presented as a restored drive.
	readOnly := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String()
	check, err := sql.Open("sqlite", readOnly)
	if err != nil {
		return err
	}
	var tables int
	err = check.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('users','schema_migrations','drive_schema','drive_files','drive_versions')`).Scan(&tables)
	if err := errors.Join(err, check.Close()); err != nil {
		return fmt.Errorf("inspect restored database: %w", err)
	}
	if tables != 5 {
		return errors.New("restored database does not contain the KyDrive schema")
	}
	key, err := keyfile.Load(filepath.Join(dataDir, "encryption.key"), 32)
	if err != nil {
		return fmt.Errorf("load restored deployment key: %w", err)
	}
	st, err := store.Open(ctx, config.DatabaseConfig{Driver: "sqlite", DSN: path, DataDir: dataDir}, key)
	if err != nil {
		return err
	}
	return errors.Join(st.ResetAfterRestore(ctx), st.Close())
}

const resetDone = "Access grants reset. Sign in again; recreate app passwords, device links, service tokens and sharing links."

func runRestoreReset(args []string) {
	fs := flag.NewFlagSet("restore-reset", flag.ExitOnError)
	target := fs.String("to", "", "directory a restore extracted into")
	_ = fs.Parse(args)
	if *target == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: kydrive-server restore-reset -to <dir>")
		os.Exit(2)
	}
	if err := resetRestored(context.Background(), filepath.Join(*target, "data")); err != nil {
		log.Fatalf("Reset failed: %v\nDo not start the server on %s.", err, *target)
	}
	fmt.Println(resetDone)
}
