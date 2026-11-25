package backup_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/backup"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// A multi-tenant PostgreSQL deposit carries the database as the usual SQLite member, in its
// tenancy, which migrate-postgres restores into an empty database; nothing is left behind.
func TestCollectSnapshotsMultiTenantPostgres(t *testing.T) {
	if !testdb.Postgres() {
		t.Skip("snapshots PostgreSQL")
	}
	ctx := context.Background()
	cfg := &config.Config{}
	cfg.Database = testdb.Config(t)
	cfg.Database.Tenancy, cfg.Database.StaffOrg = "multi", "00000000-0000-0000-0000-000000000099"
	cfg.Database.DataDir = t.TempDir()
	cfg.Security.EncryptionKey = bytes.Repeat([]byte{1}, 32)
	st, err := store.Open(ctx, cfg.Database, cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Settings().SetSetting(ctx, "canary", "in the capsule"); err != nil {
		t.Fatal(err)
	}

	payload, err := backup.Collect(ctx, cfg, "1.0.0", backup.PurposeDeposit)
	if err != nil {
		t.Fatal(err)
	}
	var db []byte
	for _, f := range payload.Files {
		if f.Path == "data/ky_server.db" {
			db = f.Data
		}
	}
	if db == nil {
		t.Fatal("no database member")
	}
	if left, _ := filepath.Glob(filepath.Join(cfg.Database.DataDir, "snapshot-*")); len(left) != 0 {
		t.Fatalf("snapshot left behind: %v", left)
	}
	restored := filepath.Join(t.TempDir(), "ky_server.db")
	if err := os.WriteFile(restored, db, 0o600); err != nil {
		t.Fatal(err)
	}
	target := testdb.Config(t)
	if _, err := store.MigrateToPostgres(ctx, restored, target.DSN, cfg.Security.EncryptionKey, false); err != nil {
		t.Fatal("restore:", err)
	}
	target.Tenancy, target.StaffOrg = drive.TenancyMulti, cfg.Database.StaffOrg
	back, err := store.Open(ctx, target, cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	if got, err := back.Settings().GetSetting(ctx, "canary"); err != nil || got != "in the capsule" {
		t.Fatalf("restored canary %q %v", got, err)
	}
}
