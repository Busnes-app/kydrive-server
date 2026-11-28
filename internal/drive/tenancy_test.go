package drive_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/store/migrations"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// tenancyDB opens a fully migrated store and a second handle on the same database.
func tenancyDB(t *testing.T) (store.Store, *sql.DB) {
	t.Helper()
	cfg := testdb.Config(t)
	st, err := store.Open(context.Background(), cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, testdb.Open(t, cfg)
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
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func refused(err error) bool { return err != nil && strings.Contains(err.Error(), "tenancy:") }

func TestEveryTableIsClassified(t *testing.T) {
	_, db := tenancyDB(t)
	class := map[string]string{}
	add := func(name, c string) {
		if prev, ok := class[name]; ok {
			t.Errorf("%s is both %s and %s", name, prev, c)
		}
		class[name] = c
	}
	for _, n := range drive.OrgOwnedTables {
		add(n, "org-owned")
	}
	for n := range drive.DerivedTables {
		add(n, "derived")
	}
	for _, n := range drive.PlatformTables {
		add(n, "platform")
	}
	for _, n := range drive.SharedStateTables {
		add(n, "platform")
	}
	for n := range drive.MultiTenantTables {
		add(n, "multi-tenant")
	}
	tables := tables(t, db)
	present := map[string]bool{}
	for _, n := range tables {
		present[n] = true
		if class[n] == "" {
			t.Errorf("table %s has no tenancy class: add it to a list in tenancy_classes_test.go", n)
		}
	}
	for n := range class {
		// drive_name_scrub is gone once names are scrubbed (names.go); the shared tables exist
		// on PostgreSQL only.
		if !present[n] && n != "drive_name_scrub" && drive.MultiTenantTables[n] == "" && (testdb.Postgres() || !slices.Contains(drive.SharedStateTables, n)) {
			t.Errorf("classified table %s does not exist", n)
		}
	}
	for child, parent := range drive.DerivedTables {
		if !references(t, db, child, parent) {
			t.Errorf("derived table %s has no foreign key to %s", child, parent)
		}
	}
}

func TestOrgOwnedTablesHaveOrgID(t *testing.T) {
	_, db := tenancyDB(t)
	for _, table := range drive.OrgOwnedTables {
		notNull, def, err := orgIDColumn(db, table)
		if err != nil {
			t.Errorf("%s: no org_id: %v", table, err)
			continue
		}
		if want := !drive.NullableOrgID[table]; notNull != want {
			t.Errorf("%s: org_id NOT NULL is %v, want %v", table, notNull, want)
		}
		if def != "'"+drive.DefaultOrg+"'" {
			t.Errorf("%s: org_id default %q", table, def)
		}
		for _, trigger := range []string{table + "_org_exists", table + "_org_fixed"} {
			if len(triggers(t, db, table, trigger)) != 1 {
				t.Errorf("%s: no trigger %s", table, trigger)
			}
		}
		// Triggers run before constraints, so an otherwise empty row reaches the org check.
		if _, err := db.Exec(`INSERT INTO ` + table + `(org_id) VALUES('no-such-org')`); !refused(err) {
			t.Errorf("%s: insert into an unknown org: %v", table, err)
		}
	}
}

// A multi-tenant database has no default org row, so a row that forgets org_id is refused
// instead of landing in the default org.
func TestMultiTenantRefusesDefaultOrg(t *testing.T) {
	_, db := tenancyDB(t)
	if _, err := db.Exec(`DELETE FROM drive_orgs`); err != nil {
		t.Fatal(err)
	}
	for _, table := range drive.OrgOwnedTables {
		if _, err := db.Exec(`INSERT INTO ` + table + ` DEFAULT VALUES`); !refused(err) {
			t.Errorf("%s: insert without org_id: %v", table, err)
		}
	}
}

func TestTenancyModeIsRecorded(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	st, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	db := testdb.Open(t, cfg)
	var mode, org string
	if err := db.QueryRow(`SELECT (SELECT value FROM server_settings WHERE key='tenancy_mode'),(SELECT id||' '||status||' '||enabled||' '||COALESCE(CAST(storage_bytes AS TEXT),'none') FROM drive_orgs)`).Scan(&mode, &org); err != nil || mode != "single" || org != drive.DefaultOrg+" active 1 none" {
		t.Fatalf("mode %q, orgs %q, %v", mode, org, err)
	}
	if _, err := db.Exec(`UPDATE server_settings SET value='multi' WHERE key='tenancy_mode'`); err != nil {
		t.Fatal(err)
	}
	if st, err := store.Open(ctx, cfg, testdb.Key); err == nil || !strings.Contains(err.Error(), "multi-tenant") {
		if err == nil {
			st.Close()
		}
		t.Fatalf("opened a database recorded multi-tenant: %v", err)
	}
}

// Each same-org trigger refuses a row joining org B to the default org's rows and accepts the
// same row inside one org.
func TestTenancyTriggersKeepRowsInOneOrg(t *testing.T) {
	ctx := context.Background()
	st, db := tenancyDB(t)
	d := st.Drive()
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	const b = "org-b"
	mustExec(`INSERT INTO drive_orgs(id,status) VALUES(?,'active'),('org-gone','deleted')`, b)
	mustExec(`INSERT INTO server_settings(key,value,updated_at) VALUES('orgs_fresh_until','99999999999',CURRENT_TIMESTAMP)`)
	for _, u := range []struct{ id, org string }{{"ua", drive.DefaultOrg}, {"ua2", drive.DefaultOrg}, {"ub", b}} {
		mustExec(`INSERT INTO users(id,username,created_at,updated_at,org_id) VALUES(?,?,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, u.id, u.id, u.org)
	}
	mustExec(`INSERT INTO groups(id,display_name,created_at,updated_at,org_id) VALUES('ga','A',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?),('gb','B',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, drive.DefaultOrg, b)
	wsA, err := d.EnsurePersonalWorkspace(ctx, "ua")
	if err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO drive_workspaces(id,name,quota,org_id) VALUES('wb','B',100,?),('wa2','A2',100,?),('wb2','B2',100,?)`, b, drive.DefaultOrg, b)
	digest := strings.Repeat("0", 64)
	fa, err := d.Publish(ctx, "ua", drive.File{Workspace: wsA, Name: "a.txt"}, drive.Version{Blob: "blob-a", Size: 1, Digest: digest}, 0)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO drive_files(id,workspace,parent,name,name_hmac,revision) VALUES('fb','wb','','b','mac-b',1)`)
	mustExec(`INSERT INTO drive_folders(id,workspace,parent,name,name_hmac) VALUES('da','` + wsA + `','','d','mac-d')`)
	mustExec(`INSERT INTO drive_links(id,workspace,resource_kind,resource_id,role,scope,token_hash,created_by,created) VALUES('la','` + wsA + `','file','` + fa.ID + `','reader','org','hash-la','ua','now')`)

	share := func(kind, principal string) string {
		return fmt.Sprintf(`INSERT INTO drive_shares(id,workspace,resource_kind,resource_id,principal_kind,principal_id,role,created_by,created) VALUES('s-%s','%s','file','%s','%s','%s','reader','ua','now')`, principal, wsA, fa.ID, kind, principal)
	}
	for _, c := range []struct{ name, cross, same string }{
		{"group member", `INSERT INTO group_members VALUES('ga','ub')`, `INSERT INTO group_members VALUES('ga','ua')`},
		{"personal workspace owner", `INSERT INTO drive_personal_workspaces(workspace,owner) VALUES('wa2','ub')`, `INSERT INTO drive_personal_workspaces(workspace,owner) VALUES('wb2','ub')`},
		{"grant", `INSERT INTO drive_grants VALUES('wa2','gb','reader')`, `INSERT INTO drive_grants VALUES('wa2','ga','reader')`},
		{"suite group", `INSERT INTO drive_suite_groups VALUES('k1','wa2','gb')`, `INSERT INTO drive_suite_groups VALUES('k2','wb','gb')`},
		{"user share", share("user", "ub"), share("user", "ua2")},
		{"group share", share("group", "gb"), share("group", "ga")},
		{"link opener", `INSERT INTO drive_link_users VALUES('la','ub','now')`, `INSERT INTO drive_link_users VALUES('la','ua2','now')`},
		{"blob of another org", `INSERT INTO drive_versions(file_id,revision,blob,digest,size,created) VALUES('fb',1,'blob-a','` + digest + `',1,'now')`, `INSERT INTO drive_versions(file_id,revision,blob,digest,size,created) VALUES('fb',1,'blob-b','` + digest + `',1,'now')`},
		{"file move", `UPDATE drive_files SET workspace='wb' WHERE id='` + fa.ID + `'`, `UPDATE drive_files SET workspace='wa2' WHERE id='` + fa.ID + `'`},
		{"folder move", `UPDATE drive_folders SET workspace='wb' WHERE id='da'`, `UPDATE drive_folders SET workspace='wa2' WHERE id='da'`},
		{"member moved to another org's group", `UPDATE group_members SET group_id='gb' WHERE user_id='ua'`, `UPDATE group_members SET group_id='ga' WHERE user_id='ua'`},
		{"unknown org", `INSERT INTO users(id,username,created_at,updated_at,org_id) VALUES('ux','ux',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'nope')`, `INSERT INTO users(id,username,created_at,updated_at,org_id) VALUES('ux','ux',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'org-b')`},
		{"deleted org", `INSERT INTO drive_events(id,user_id,action,resource,created,org_id) VALUES('e1','ub','x','y','now','org-gone')`, `INSERT INTO drive_events(id,user_id,action,resource,created,org_id) VALUES('e1','ub','x','y','now',NULL)`},
		{"share moved to another org's user", `UPDATE drive_shares SET principal_id='ub' WHERE id='s-ua2'`, `UPDATE drive_shares SET principal_id='ua2' WHERE id='s-ua2'`},
		{"org_id changed", `UPDATE users SET org_id='org-b' WHERE id='ua'`, `UPDATE users SET org_id=org_id WHERE id='ua'`},
	} {
		if err := drive.ExecTx(ctx, d, c.cross); !refused(err) || !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%s across orgs: %v", c.name, err)
		}
		if _, err := db.Exec(c.same); err != nil {
			t.Errorf("%s inside one org: %v", c.name, err)
		}
	}
	// A new share naming no account has no org to compare: refused, not let through as NULL.
	if err := drive.ExecTx(ctx, d, share("user", "nobody")); !refused(err) {
		t.Errorf("share to a missing account: %v", err)
	}
	// A share whose user is gone has no org to compare, yet its role can still change.
	mustExec(`DELETE FROM users WHERE id='ua2'`)
	if err := drive.ExecTx(ctx, d, `UPDATE drive_shares SET role='editor' WHERE id='s-ua2'`); err != nil {
		t.Fatalf("role change on a share whose user is gone: %v", err)
	}
}

// fillOldSchema writes a row to every org-owned table through a database at the schema
// before tenancy, two directory accounts sharing a KySignOn subject among them.
func fillOldSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	d, err := drive.OpenAt(ctx, db, testdb.Key, drive.TenancySchemaVersion-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users(id,username,sso_provider,sso_subject,created_at,updated_at) VALUES('owner','owner','kysignon','dup',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),('twin','twin','kysignon','dup',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO groups(id,display_name,created_at,updated_at) VALUES('g','Group',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		`INSERT INTO group_members VALUES('g','owner')`,
		`INSERT INTO audit_records(user_id,action,created_at) VALUES('owner','auth.login',CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	// The older release's own statements: this one's write org_id, which schema 26 lacks.
	ws, f := "ws", drive.File{ID: "kept"}
	stored, mac, err := drive.SealName(d, "drive_files", f.ID, "kept.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO drive_workspaces(id,name,quota) VALUES('ws','My files',1073741824)`,
		`INSERT INTO drive_personal_workspaces(workspace,owner,owner_name) VALUES('ws','owner','owner')`,
		`INSERT INTO drive_files(id,workspace,parent,name,name_hmac,revision) VALUES('kept','ws','','` + stored + `','` + mac + `',1)`,
		`INSERT INTO drive_versions(file_id,revision,blob,digest,size,created) VALUES('kept',1,'blob','` + strings.Repeat("a", 64) + `',4,'now')`,
		`INSERT INTO drive_changes(workspace,kind,item,created) VALUES('ws','file','kept',1)`,
		`INSERT INTO drive_events(id,user_id,action,resource,created) VALUES('event','owner','file.published','kept','now')`,
		`INSERT INTO drive_grants VALUES('` + ws + `','g','reader')`,
		`INSERT INTO drive_shares(id,workspace,resource_kind,resource_id,principal_kind,principal_id,role,created_by,created) VALUES('share','` + ws + `','file','` + f.ID + `','user','twin','reader','owner','now')`,
		`INSERT INTO drive_links(id,workspace,resource_kind,resource_id,role,scope,token_hash,created_by,created) VALUES('org','` + ws + `','file','` + f.ID + `','reader','org','org-hash','owner','now')`,
		`INSERT INTO drive_link_users VALUES('org','twin','now')`,
		`INSERT INTO drive_service_tokens(id,user_id,workspace,name,role,token_hash,expires) VALUES('service','owner','` + ws + `','Service','reader','service-hash',1)`,
		`INSERT INTO drive_editor_documents(key,file_id,base_revision,current_revision) VALUES('document','` + f.ID + `',1,1)`,
		`INSERT INTO drive_editor_saves(key,digest,revision) VALUES('document','digest',1)`,
		`INSERT INTO drive_app_passwords(id,user_id,name,hash,created,scope) VALUES('dav','owner','DAV','dav-hash','now','dav')`,
		`INSERT INTO drive_change_floor(workspace,seq) VALUES('` + ws + `',0)`,
		`INSERT INTO drive_devices(id,user_id,name,platform,hash,created,last_used) VALUES('device','owner','Desktop','linux','device-hash','now',1)`,
		`INSERT INTO drive_device_codes(code_hash,user_id,name,platform,challenge,expires) VALUES('code','owner','Desktop','linux','challenge',1)`,
		`INSERT INTO drive_peer_settings VALUES('owner',1)`,
		`INSERT INTO drive_peer_folders VALUES('owner','folder')`,
		`INSERT INTO drive_peer_tickets(id,token_hash,owner,name,size,expires,pairing) VALUES('ticket','ticket-hash','owner','name',1,1,'pairing')`,
		`INSERT INTO drive_peer_picks VALUES('owner','` + f.ID + `',1)`,
		`INSERT INTO drive_peer_guest VALUES('owner')`,
		`INSERT INTO drive_view_copies(id,user_id,workspace,name,size,pairing,ticket_expires,expires) VALUES('view','owner','` + ws + `','name',1,'pairing',1,1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
}

// snapshot renders every row of every table, in the columns the older schema had.
func snapshot(t *testing.T, db *sql.DB, cols map[string][]string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for table, names := range cols {
		rows, err := db.Query(`SELECT ` + strings.Join(names, ",") + ` FROM ` + table + ` ORDER BY rowid`)
		if err != nil {
			t.Fatal(table, err)
		}
		var b strings.Builder
		for rows.Next() {
			vals := make([]any, len(names))
			ptrs := make([]any, len(names))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%q\n", vals)
		}
		rows.Close()
		out[table] = b.String()
	}
	return out
}

// An upgrade from the previous release keeps every row as it was, puts each in the default
// org, and leaves the store working: a new upload, share and group member land in that org.
func TestTenancyUpgradeKeepsEveryRow(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	testdb.SQLiteOnly(t, "upgrades an older SQLite schema")
	db := testdb.Open(t, cfg)
	if err := migrations.Run(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	fillOldSchema(t, ctx, db)
	if n := len(strs(t, db, `SELECT name FROM sqlite_master WHERE name='drive_orgs'`)); n != 0 {
		t.Fatal("the previous schema already has drive_orgs")
	}
	cols := map[string][]string{}
	for _, table := range strs(t, db, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name!='drive_schema'`) {
		cols[table] = columns(t, db, table)
	}
	before := snapshot(t, db, cols)

	st, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal("upgrade:", err)
	}
	defer st.Close()
	if after := snapshot(t, db, cols); !reflect.DeepEqual(before, after) {
		for table := range before {
			if before[table] != after[table] {
				t.Errorf("%s changed:\n%s\nbecame\n%s", table, before[table], after[table])
			}
		}
	}
	for _, table := range drive.OrgOwnedTables {
		var rows, inDefault int
		err := db.QueryRow(`SELECT COUNT(*),COUNT(*) FILTER (WHERE org_id=?) FROM `+table, drive.DefaultOrg).Scan(&rows, &inDefault)
		_, existed := cols[table]
		// Tables introduced after this fixture's schema start empty.
		if err != nil || rows != inDefault || (rows > 0) != existed {
			t.Errorf("%s: %d rows, %d in the default org, existed before upgrade: %t, %v", table, rows, inDefault, existed, err)
		}
	}
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM drive_schema`).Scan(&version); err != nil || version != drive.SchemaVersion {
		t.Fatalf("schema version %d, %v", version, err)
	}
	// Per-org keys are multi-tenant only: a single-tenant install keeps the deployment key.
	if slices.Contains(tables(t, db), "drive_org_keys") {
		t.Fatal("an upgraded single-tenant install has a drive_org_keys table")
	}

	d := st.Drive()
	ws, err := d.EnsurePersonalWorkspace(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.Publish(ctx, "owner", drive.File{Workspace: ws, Name: "new.txt"}, drive.Version{Blob: "blob-new", Size: 4, Digest: strings.Repeat("b", 64)}, 0)
	if err != nil {
		t.Fatal("upload after upgrade:", err)
	}
	if _, err := d.File(ctx, "owner", f.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.File(ctx, "twin", strs(t, db, `SELECT resource_id FROM drive_shares WHERE id='share'`)[0], 1); err != nil {
		t.Fatal("share lost its reader:", err)
	}
	if _, err := db.Exec(`INSERT INTO group_members VALUES('g','twin')`); err != nil {
		t.Fatal(err)
	}
	if n := len(strs(t, db, `SELECT id FROM drive_events WHERE org_id IS NOT ?`, drive.DefaultOrg)); n != 0 {
		t.Fatalf("%d events outside the default org", n)
	}
}

// An upgrade from before schema 20 renames a file that clashes with a folder, recording its
// change and event in statements of that schema, which has no org_id yet; both rows end up in
// the default org.
func TestUpgradeRenamesAClashBeforeTenancy(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	testdb.SQLiteOnly(t, "upgrades an older SQLite schema")
	db := testdb.Open(t, cfg)
	if err := migrations.Run(ctx, db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	d, err := drive.OpenAt(ctx, db, testdb.Key, drive.ClashSchemaVersion-1)
	if err != nil {
		t.Fatal(err)
	}
	folderName, folderMAC, err := drive.SealName(d, "drive_folders", "folder", "Plan")
	if err != nil {
		t.Fatal(err)
	}
	fileName, fileMAC, err := drive.SealName(d, "drive_files", "file", "Plan")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO drive_workspaces(id,name,quota) VALUES('ws','Team',1048576)`,
		`INSERT INTO drive_folders(id,workspace,parent,name,name_hmac) VALUES('folder','ws','','` + folderName + `','` + folderMAC + `')`,
		`INSERT INTO drive_files(id,workspace,parent,name,name_hmac,revision) VALUES('file','ws','','` + fileName + `','` + fileMAC + `',1)`,
		`INSERT INTO drive_versions(file_id,revision,blob,digest,size,created) VALUES('file',1,'blob','` + strings.Repeat("a", 64) + `',1,'now')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	st, err := store.Open(ctx, cfg, testdb.Key)
	if err != nil {
		t.Fatal("upgrade:", err)
	}
	defer st.Close()
	if got := strs(t, db, `SELECT action||' '||org_id FROM drive_events WHERE resource LIKE 'file %'`); len(got) != 1 || got[0] != "file.renamed "+drive.DefaultOrg {
		t.Fatalf("rename events %v", got)
	}
	if got := strs(t, db, `SELECT item||' '||org_id FROM drive_changes WHERE workspace='ws'`); len(got) != 1 || got[0] != "file "+drive.DefaultOrg {
		t.Fatalf("changes %v", got)
	}
}
