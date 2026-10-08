package drive_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// A pilot database predates trash bookkeeping. Upgrading must keep its folders, stamp
// already-trashed files with the upgrade time (so retention does not purge them at once),
// free trashed folder names, and be idempotent.
func TestSchemaUpgradesLegacyDatabase(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	if cfg.Driver != "sqlite" {
		t.Skip("drive is SQLite-only")
	}
	legacy, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.ExecContext(ctx, `
CREATE TABLE drive_workspaces(id TEXT PRIMARY KEY,name TEXT NOT NULL,quota BIGINT NOT NULL CHECK(quota>0));
CREATE TABLE drive_folders(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,UNIQUE(workspace,parent,name));
CREATE TABLE drive_files(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,revision BIGINT NOT NULL,trashed BOOLEAN NOT NULL DEFAULT false);
INSERT INTO drive_workspaces VALUES('w','Legacy',100);
INSERT INTO drive_folders VALUES('d','w','','Reports');
INSERT INTO drive_files VALUES('f','w','d','old.txt',1,true);`)
	legacy.Close()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		st, err := store.Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
	}
	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var folders, stamped, version, days int
	err = db.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM drive_folders WHERE id='d' AND trashed=false),
 (SELECT COUNT(*) FROM drive_files WHERE id='f' AND trashed_at!='' AND julianday('now')-julianday(trashed_at)<1),
 (SELECT MAX(version) FROM drive_schema),
 (SELECT trash_days FROM drive_workspaces WHERE id='w')`).Scan(&folders, &stamped, &version, &days)
	if err != nil || folders != 1 || stamped != 1 || version != 2 || days != 0 {
		t.Fatalf("upgrade: err=%v folders=%d stamped=%d version=%d days=%d", err, folders, stamped, version, days)
	}
	if _, err = db.ExecContext(ctx, `UPDATE drive_folders SET trashed=true WHERE id='d'; INSERT INTO drive_folders(id,workspace,parent,name) VALUES('d2','w','','Reports')`); err != nil {
		t.Fatalf("trashed folder name not reusable: %v", err)
	}
}
