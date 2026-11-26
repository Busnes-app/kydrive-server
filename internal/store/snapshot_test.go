package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

const snapshotStaffOrg = "00000000-0000-0000-0000-000000000099"

// multiPostgres is a multi-tenant PostgreSQL store with orgs A (active) and B (deleted, its
// rows not yet purged): members, a group, a workspace with a file, a session and audit rows.
func multiPostgres(t *testing.T) (config.DatabaseConfig, store.Store, *sql.DB, string) {
	t.Helper()
	needPostgres(t)
	ctx := context.Background()
	cfg := testdb.Config(t)
	cfg.Tenancy, cfg.StaffOrg = "multi", snapshotStaffOrg
	st, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	db := testdb.Open(t, cfg)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	d := st.Drive()
	// Recorded as the poll records them (with their keys), granted as an answered report leaves them.
	if _, err := d.ApplyOrgs(ctx, []suitedir.Org{{ID: "org-a", Status: suitedir.OrgActive, Revision: 1, Enabled: true}, {ID: "org-b", Status: suitedir.OrgActive, Revision: 1, Enabled: true}}, 1); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE drive_orgs SET grant_ceiling=1099511627776,grant_expires=99999999999 WHERE id IN ('org-a','org-b')`)
	root := t.TempDir()
	var fileA string
	for _, o := range []string{"a", "b"} {
		org, member, group := "org-"+o, o+"1", "group-"+o
		exec(`INSERT INTO users(id,username,display_name,role,status,sso_provider,created_at,updated_at,org_id) VALUES(?,?,?,'user','active','scim',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, member, member, "Person "+member, org)
		exec(`INSERT INTO groups(id,display_name,external_id,created_at,updated_at,org_id) VALUES(?,?,'',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, group, group, org)
		exec(`INSERT INTO group_members(group_id,user_id) VALUES(?,?)`, group, member)
		ws := "ws-" + o
		exec(`INSERT INTO drive_workspaces(id,name,quota,org_id) VALUES(?,'Team',1048576,?)`, ws, org)
		exec(`INSERT INTO drive_grants VALUES(?,?,'manager')`, ws, group)
		k, err := d.KeysFor(ctx, ws)
		if err != nil {
			t.Fatal(err)
		}
		v, err := drive.WriteBlob(objects.Dir(filepath.Join(root, "blobs")), k, strings.NewReader("plan of "+org), 1024)
		if err != nil {
			t.Fatal(err)
		}
		f, err := d.Publish(ctx, member, drive.File{Workspace: ws, Name: "plan.txt"}, v, 0)
		if err != nil {
			t.Fatal(err)
		}
		if o == "a" {
			fileA = f.ID
		}
		if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "session-" + o, UserID: member, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "", false); err != nil {
			t.Fatal(err)
		}
		if err := st.Audit().LogAudit(ctx, &store.AuditRecord{UserID: member, Action: "auth.login"}); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE drive_orgs SET status='deleted' WHERE id='org-b'`)
	return cfg, st, db, fileA
}

// dump is every row of every migrated table, as PostgreSQL renders it, in a stable order.
func dump(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range store.CopiedTables(true) {
		var rows string
		if err := db.QueryRow(`SELECT COALESCE(string_agg(r,E'\n' ORDER BY r),'') FROM (SELECT row_to_json(t)::text r FROM "` + table + `" t) s`).Scan(&rows); err != nil {
			t.Fatal(table, err)
		}
		out[table] = rows
	}
	return out
}

// A PostgreSQL snapshot holds one point in time of every row, in the database's tenancy, and
// migrate-postgres puts exactly those rows into an empty database, which then works.
func TestPostgresSnapshotRestoresEveryRow(t *testing.T) {
	ctx := context.Background()
	srcCfg, _, src, fileA := multiPostgres(t)
	want := dump(t, src)
	if want["drive_files"] == "" || !strings.Contains(want["drive_orgs"], `"deleted"`) || strings.Count(want["drive_org_keys"], "org_id") != 3 {
		t.Fatal("fixture lacks a file or the deleted org")
	}

	// Writes committed while the snapshot runs are not in it.
	*store.SnapshotHook = func() {
		if _, err := src.Exec(`UPDATE users SET display_name='changed during the snapshot' WHERE id='a1'`); err != nil {
			t.Error(err)
		}
		if _, err := src.Exec(`INSERT INTO audit_records(user_id,action,created_at,org_id) VALUES('a1','late',CURRENT_TIMESTAMP,'org-a')`); err != nil {
			t.Error(err)
		}
	}
	defer func() { *store.SnapshotHook = nil }()
	path := filepath.Join(t.TempDir(), "ky_server.db")
	if err := store.SnapshotPostgres(ctx, srcCfg.DSN, path, testdb.Key); err != nil {
		t.Fatal("snapshot:", err)
	}
	lite := testdb.Open(t, config.DatabaseConfig{Driver: "sqlite", DSN: path})
	if got, err := store.RecordedTenancy(ctx, lite); err != nil || got.Mode != drive.TenancyMulti || got.StaffOrg != snapshotStaffOrg {
		t.Fatalf("snapshot tenancy %+v %v", got, err)
	}
	// The triggers are back on the snapshot: org B is still refused new rows.
	if _, err := lite.Exec(`INSERT INTO groups(id,display_name,external_id,created_at,updated_at,org_id) VALUES('late','late','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'org-b')`); err == nil {
		t.Fatal("the snapshot's triggers were not recreated")
	}

	target := testdb.Config(t)
	if _, err := store.MigrateToPostgres(ctx, path, target.DSN, testdb.Key, false); err != nil {
		t.Fatal("restore:", err)
	}
	restored := testdb.Open(t, target)
	got := dump(t, restored)
	for _, table := range store.CopiedTables(true) {
		if got[table] != want[table] {
			t.Errorf("%s differs after the round trip:\n got %s\nwant %s", table, got[table], want[table])
		}
	}

	target.Tenancy, target.StaffOrg = "multi", snapshotStaffOrg
	st, err := store.Open(ctx, target, testdb.Key)
	if err != nil {
		t.Fatal("open the restored database:", err)
	}
	defer st.Close()
	if f, err := st.Drive().File(ctx, "a1", fileA, 1); err != nil || f.Name != "plan.txt" {
		t.Fatalf("file after restore: %+v %v", f, err)
	}
	if err := st.Audit().LogAudit(ctx, &store.AuditRecord{UserID: "a1", Action: "after"}); err != nil {
		t.Fatal("audit after restore:", err)
	}
	if _, err := restored.Exec(`INSERT INTO groups(id,display_name,external_id,created_at,updated_at,org_id) VALUES('late','late','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'org-b')`); err == nil {
		t.Fatal("the restored database's org triggers are off")
	}
	single := target
	single.Tenancy, single.StaffOrg = "", ""
	if st, err := store.Open(ctx, single, testdb.Key); err == nil {
		st.Close()
		t.Fatal("a single-tenant server opened the restored multi-tenant database")
	}
	// The capsule brought org A's key back; a poll reporting A deleted destroys it again.
	if _, err := st.Drive().ApplyOrgs(ctx, []suitedir.Org{{ID: "org-a", Status: suitedir.OrgDeleted, Revision: 2, Enabled: true}}, 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Drive().PurgeOrgs(ctx); err != nil {
		t.Fatal(err)
	}
	var keys int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM drive_org_keys WHERE org_id IN ('org-a','org-b')`).Scan(&keys); err != nil || keys != 0 {
		t.Fatalf("%d purged org keys left after the restore's purge, %v", keys, err)
	}
}
