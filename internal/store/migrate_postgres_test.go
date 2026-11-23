package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

func needPostgres(t *testing.T) {
	t.Helper()
	if !testdb.Postgres() {
		t.Skip("migrates into PostgreSQL")
	}
}

func strs(t *testing.T, db *sql.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func count(t *testing.T, db *sql.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(table, err)
	}
	return n
}

// source is a SQLite deployment with a row in every table, the way the server writes them
// where that is easy and by hand elsewhere. It returns the database path and a published
// file's ID.
func source(t *testing.T) (string, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	cfg := config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(dir, "ky_server.db")}
	st, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, u := range []*store.User{
		{ID: "owner", Username: "owner", Email: "owner@local.test", PasswordHash: "hash", Role: "admin", Status: "active", SSOProvider: "local", MustChangePassword: true},
		{ID: "member", Username: "member", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "subject"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", ExternalID: "ext", Members: []string{"member"}}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.Sessions().CreateSession(ctx, &store.Session{TokenHash: "session", UserID: "owner", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}, "hash", false); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := st.Audit().LogAudit(ctx, &store.AuditRecord{UserID: "owner", Action: "auth.login"}); err != nil {
			t.Fatal(err)
		}
	}
	d := st.Drive()
	w, err := d.CreateWorkspace(ctx, "owner", "Team files", 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Grant(ctx, "owner", w.ID, "team", "editor"); err != nil {
		t.Fatal(err)
	}
	folder, err := d.AddFolder(ctx, "member", w.ID, "", "Reports")
	if err != nil {
		t.Fatal(err)
	}
	v, err := drive.WriteBlob(objects.Dir(filepath.Join(dir, "blobs")), testdb.Key, strings.NewReader("kept"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.Publish(ctx, "member", drive.File{Workspace: w.ID, Parent: folder.ID, Name: "notes.txt"}, v, 0)
	if err != nil {
		t.Fatal(err)
	}
	db := testdb.Open(t, cfg)
	for _, q := range []string{
		// A row an older release wrote, its time in SQLite's own format.
		`INSERT INTO users(id,username,created_at,updated_at) VALUES('old','old',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO mfa_challenges(token_hash,user_id,expires_at) VALUES('mfa','owner',CURRENT_TIMESTAMP)`,
		`INSERT INTO sso_logouts(sid,expires_at) VALUES('sid',CURRENT_TIMESTAMP)`,
		`INSERT INTO sso_logout_tokens(jti,expires_at) VALUES('jti',CURRENT_TIMESTAMP)`,
		`INSERT INTO sso_subject_logouts(provider,subject,cutoff,expires_at) VALUES('kysignon','s',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO drive_suite_groups(kyid,workspace,grp) VALUES('kyid','` + w.ID + `','team')`,
		// Change seqs with gaps, as purged history leaves them.
		`INSERT INTO drive_changes(seq,workspace,kind,item,created) VALUES(90,'` + w.ID + `','file','` + f.ID + `',1),(95,'` + w.ID + `','folder','` + folder.ID + `',1)`,
		`INSERT INTO drive_change_floor(workspace,seq) VALUES('` + w.ID + `',0)`,
		`INSERT INTO drive_shares(id,workspace,resource_kind,resource_id,principal_kind,principal_id,role,created_by,created) VALUES('share','` + w.ID + `','file','` + f.ID + `','user','owner','reader','member','now')`,
		`INSERT INTO drive_links(id,workspace,resource_kind,resource_id,role,scope,token_hash,created_by,created) VALUES('link','` + w.ID + `','file','` + f.ID + `','reader','org','link-hash','member','now')`,
		`INSERT INTO drive_link_users VALUES('link','owner','now')`,
		`INSERT INTO drive_service_tokens(id,user_id,workspace,name,role,token_hash,expires,revoked) VALUES('service','owner','` + w.ID + `','Service','reader','service-hash',1,true)`,
		`INSERT INTO drive_editor_sessions(id,file_id,revision,user_id,token_hash,expires,editable) VALUES('editor','` + f.ID + `',1,'member','editor-hash',1,false)`,
		`INSERT INTO drive_editor_documents(key,file_id,base_revision,current_revision) VALUES('document','` + f.ID + `',1,1)`,
		`INSERT INTO drive_editor_saves(key,digest,revision) VALUES('document','digest',1)`,
		`INSERT INTO drive_upload_sessions(id,owner,workspace,parent,name,name_hmac,file,expected,size,expires) VALUES('upload','member','` + w.ID + `','','n','mac','',4,4,1)`,
		`INSERT INTO drive_upload_segments(session,start,path,size) VALUES('upload',0,'segment',4)`,
		`INSERT INTO drive_blob_kyb1(blob) VALUES('sealed')`,
		`INSERT INTO drive_blob_retire(blob) VALUES('retired')`,
		`INSERT INTO drive_app_passwords(id,user_id,name,hash,created,scope) VALUES('dav','owner','DAV','dav-hash','now','dav')`,
		`INSERT INTO drive_devices(id,user_id,name,platform,hash,created,last_used) VALUES('device','owner','Desktop','linux','device-hash','now',1)`,
		`INSERT INTO drive_device_codes(code_hash,user_id,name,platform,challenge,expires) VALUES('code','owner','Desktop','linux','challenge',1)`,
		`INSERT INTO drive_view_copies(id,user_id,workspace,name,size,pairing,ticket_expires,expires) VALUES('view','owner','` + w.ID + `','name',1,'pairing',1,1)`,
		`INSERT INTO drive_peer_settings(user_id,enabled) VALUES('owner',1)`,
		`INSERT INTO drive_peer_folders(owner,folder) VALUES('owner','` + folder.ID + `')`,
		`INSERT INTO drive_peer_tickets(id,token_hash,owner,name,size,expires,pairing) VALUES('ticket','ticket-hash','owner','name',1,1,'pairing')`,
		`INSERT INTO drive_peer_picks(owner,file,expires) VALUES('owner','` + f.ID + `',1)`,
		`INSERT INTO drive_peer_guest(user_id) VALUES('owner')`,
		`INSERT INTO drive_erasures(subject,org_id,revision) VALUES('erased','org',1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if _, err := d.EnsurePersonalWorkspace(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	for _, table := range store.MigrationTables {
		if count(t, db, table) == 0 {
			t.Fatalf("the source has no %s row: add one, so its copy is tested", table)
		}
	}
	return cfg.DSN, f.ID
}

// A migration copies every row, keeps change seqs and IDs, and leaves a store that works:
// the same key opens it, the copied file reads back, and new rows get fresh IDs.
func TestMigrateToPostgresKeepsEveryRow(t *testing.T) {
	needPostgres(t)
	ctx := context.Background()
	path, file := source(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lite := testdb.Open(t, config.DatabaseConfig{Driver: "sqlite", DSN: path})
	want := map[string]int64{}
	for _, table := range store.MigrationTables {
		want[table] = count(t, lite, table)
	}
	target := testdb.Config(t)
	pg := testdb.Open(t, target)

	for range 2 { // a check leaves nothing a second check or the copy trips on
		report, err := store.MigrateToPostgres(ctx, path, target.DSN, testdb.Key, true)
		if err != nil {
			t.Fatal("check:", err)
		}
		if !maps(report.Rows, want) {
			t.Fatalf("check copied %v, want %v", report.Rows, want)
		}
		if n := count(t, pg, "users"); n != 0 {
			t.Fatalf("a check kept %d users", n)
		}
	}
	report, err := store.MigrateToPostgres(ctx, path, target.DSN, testdb.Key, false)
	if err != nil {
		t.Fatal(err)
	}
	if !maps(report.Rows, want) {
		t.Fatalf("copied %v, want %v", report.Rows, want)
	}
	for _, table := range store.MigrationTables {
		if n := count(t, pg, table); n != want[table] {
			t.Errorf("%s: %d rows, want %d", table, n, want[table])
		}
	}
	if got := strs(t, pg, `SELECT seq::text FROM drive_changes WHERE seq>=90 ORDER BY seq`); !slices.Equal(got, []string{"90", "95"}) {
		t.Errorf("change seqs %v, want [90 95]", got)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Error("the SQLite database changed")
	}

	st, err := store.Open(ctx, target, testdb.Key)
	if err != nil {
		t.Fatal("open the copy:", err)
	}
	defer st.Close()
	u, err := st.Users().GetUserByUsername(ctx, "owner")
	if err != nil || u.PasswordHash != "hash" || !u.MustChangePassword || u.CreatedAt.IsZero() {
		t.Fatalf("owner: %+v %v", u, err)
	}
	if _, err := st.Sessions().GetSession(ctx, "session"); err != nil {
		t.Fatal("session:", err)
	}
	d := st.Drive()
	f, err := d.File(ctx, "member", file, 1)
	if err != nil || f.Name != "notes.txt" {
		t.Fatalf("file: %+v %v", f, err)
	}
	// New rows: an audit record and a change row each take an ID past the copied ones.
	if err := st.Audit().LogAudit(ctx, &store.AuditRecord{UserID: "owner", Action: "after"}); err != nil {
		t.Fatal("audit after migration:", err)
	}
	if _, err := d.MoveFile(ctx, "member", f.ID, f.Revision, drive.Location{Workspace: f.Workspace, Parent: f.Parent, Name: "renamed.txt"}); err != nil {
		t.Fatal("change after migration:", err)
	}
	if got := strs(t, pg, `SELECT COUNT(*)::text FROM drive_changes WHERE seq>95`); got[0] != "1" {
		t.Errorf("%v change rows after the copied ones, want 1", got)
	}
	// Every sequence is past its column's largest value.
	for _, c := range strs(t, pg, `SELECT table_name||'.'||column_name FROM information_schema.columns WHERE table_schema=current_schema() AND (is_identity='YES' OR column_default LIKE 'nextval(%')`) {
		table, col, _ := strings.Cut(c, ".")
		var ok bool
		if err := pg.QueryRow(`SELECT (SELECT last_value FROM pg_sequences WHERE schemaname=current_schema() AND sequencename=split_part(pg_get_serial_sequence(?,?),'.',2))>=COALESCE(MAX(`+col+`),0) FROM `+table, table, col).Scan(&ok); err != nil || !ok {
			t.Errorf("%s: sequence behind its rows (%v)", c, err)
		}
	}

	if _, err := store.MigrateToPostgres(ctx, path, target.DSN, testdb.Key, false); err == nil || !strings.Contains(err.Error(), "already has data") {
		t.Fatalf("a second migration into the same database: %v", err)
	}
}

func maps(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// A migration is refused before it copies anything while a server runs on the source, with
// another deployment's key, or into a database holding tables KyDrive does not know.
func TestMigrateToPostgresRefuses(t *testing.T) {
	needPostgres(t)
	ctx := context.Background()
	path, _ := source(t)
	target := testdb.Config(t)
	pg := testdb.Open(t, target)

	lock, err := store.LockSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.MigrateToPostgres(ctx, path, target.DSN, testdb.Key, false); err == nil || !strings.Contains(err.Error(), "stop the KyDrive server") {
		t.Errorf("while a server runs: %v", err)
	}
	lock.Close()

	if _, err := store.MigrateToPostgres(ctx, path, target.DSN, bytes.Repeat([]byte{8}, 32), false); err == nil {
		t.Error("migrated with another key")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".postgres-migration-*")); len(left) > 0 {
		t.Errorf("a refused migration left %v", left)
	}
	if _, err := store.MigrateToPostgres(ctx, filepath.Join(t.TempDir(), "typo.db"), target.DSN, testdb.Key, false); err == nil {
		t.Error("migrated a missing file")
	}

	if _, err := pg.Exec(`CREATE TABLE stranger(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MigrateToPostgres(ctx, path, target.DSN, testdb.Key, false); err == nil || !strings.Contains(err.Error(), "does not know") {
		t.Errorf("into a database with an unknown table: %v", err)
	}
	if n := len(strs(t, pg, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema()`)); n != 1 {
		t.Errorf("a refused migration created %d tables", n-1)
	}
}

// A source table this release does not list is refused, not left behind.
func TestMigrateToPostgresRefusesAnUnknownSourceTable(t *testing.T) {
	needPostgres(t)
	path, _ := source(t)
	if _, err := testdb.Open(t, config.DatabaseConfig{Driver: "sqlite", DSN: path}).Exec(`CREATE TABLE extra(id TEXT)`); err != nil {
		t.Fatal(err)
	}
	target := testdb.Config(t)
	if _, err := store.MigrateToPostgres(context.Background(), path, target.DSN, testdb.Key, false); err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("migrated around an unknown table: %v", err)
	}
	if n := count(t, testdb.Open(t, target), "users"); n != 0 {
		t.Fatalf("a refused migration copied %d users", n)
	}
}

// A source row PostgreSQL would read differently, or one whose foreign key points nowhere, is
// refused and nothing is kept.
func TestMigrateToPostgresRefusesBadSourceRows(t *testing.T) {
	needPostgres(t)
	for name, q := range map[string]string{
		"a boolean that is neither 0 nor 1": `UPDATE users SET totp_enabled=2 WHERE id='owner'`,
		"a dangling foreign key":            `INSERT INTO sessions(token_hash,user_id,created_at,expires_at) VALUES('orphan','gone',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
	} {
		t.Run(name, func(t *testing.T) {
			path, _ := source(t)
			db := testdb.Open(t, config.DatabaseConfig{Driver: "sqlite", DSN: path})
			if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
			target := testdb.Config(t)
			if _, err := store.MigrateToPostgres(context.Background(), path, target.DSN, testdb.Key, false); err == nil {
				t.Fatal("migrated")
			}
			if n := count(t, testdb.Open(t, target), "users"); n != 0 {
				t.Fatalf("kept %d users", n)
			}
		})
	}
}

// migrationTables names every table either backend has, bar schema bookkeeping, each after
// the tables its foreign keys point to.
func TestMigrationTablesAreCompleteAndInOrder(t *testing.T) {
	needPostgres(t)
	ctx := context.Background()
	lite := config.DatabaseConfig{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "x.db")}
	pg := testdb.Config(t)
	for _, cfg := range []config.DatabaseConfig{lite, pg} {
		st, err := store.Open(ctx, cfg, testdb.Key)
		if err != nil {
			t.Fatal(err)
		}
		st.Close()
	}
	tables := strs(t, testdb.Open(t, lite), `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	db := testdb.Open(t, pg)
	for _, side := range [][]string{tables, strs(t, db, `SELECT tablename FROM pg_tables WHERE schemaname=current_schema()`)} {
		side = slices.DeleteFunc(side, func(s string) bool { return slices.Contains(store.SchemaTables, s) })
		slices.Sort(side)
		if want := slices.Sorted(slices.Values(store.MigrationTables)); !slices.Equal(side, want) {
			t.Errorf("tables %v, migrationTables %v", side, want)
		}
	}
	for _, fk := range strs(t, db, `SELECT conrelid::regclass::text||' '||confrelid::regclass::text FROM pg_constraint WHERE contype='f' AND connamespace=current_schema()::regnamespace`) {
		child, parent, _ := strings.Cut(fk, " ")
		if slices.Index(store.MigrationTables, parent) > slices.Index(store.MigrationTables, child) {
			t.Errorf("%s is copied before %s, which its foreign key needs", child, parent)
		}
	}
	// A fresh open writes only the rows the copy replaces.
	for _, table := range store.MigrationTables {
		if count(t, db, table) > 0 && !slices.Contains(store.OpenedTables, table) {
			t.Errorf("opening an empty database writes %s, which migrate-postgres would refuse", table)
		}
	}
}

// A second server, or migrate-postgres, cannot take a SQLite database a server holds.
func TestLockSQLiteExcludesASecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ky_server.db")
	first, err := store.LockSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LockSQLite(path); err == nil {
		t.Fatal("two holders")
	}
	first.Close()
	again, err := store.LockSQLite(path)
	if err != nil {
		t.Fatal("not released on close:", err)
	}
	again.Close()
}
