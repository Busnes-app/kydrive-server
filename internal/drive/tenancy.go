package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultOrg is a single-tenant install's one org, the fixed ID KyIdentity uses for it.
const DefaultOrg = "00000000-0000-0000-0000-000000000001"

// TenancySingle is the only tenancy mode this release opens; multi-tenant mode arrives later.
const TenancySingle = "single"

// errOrg prefixes every tenancy trigger's message; transaction reports it as ErrDenied.
const errOrg = "tenancy:"

// tenancySchema is drive schema 27: the org registry, org_id on the tables its caller lists
// (frozen there, as a shipped entry must be) and the triggers that keep each row inside one
// org. The default org row exists only on a database recorded single-tenant (recordTenancy
// runs first). An org-owned table added later needs its own entry.
func tenancySchema(owned []string, nullable map[string]bool) string {
	var b strings.Builder
	b.WriteString(`CREATE TABLE drive_orgs(id TEXT PRIMARY KEY,status TEXT NOT NULL CHECK(status IN ('active','suspended','deletion_scheduled','deleted')),revision INTEGER NOT NULL DEFAULT 0,enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),storage_bytes INTEGER CHECK(storage_bytes>0),purged INTEGER NOT NULL DEFAULT 0 CHECK(purged IN (0,1)),seeded INTEGER NOT NULL DEFAULT 0 CHECK(seeded IN (0,1)),retention_after TEXT NOT NULL DEFAULT '');
INSERT INTO drive_orgs(id,status) SELECT '` + DefaultOrg + `','active' WHERE (SELECT value FROM server_settings WHERE key='tenancy_mode')='single';
`)
	for _, t := range owned {
		column, known := "TEXT NOT NULL DEFAULT", "true"
		if nullable[t] {
			column, known = "TEXT DEFAULT", "NEW.org_id IS NOT NULL"
		}
		fmt.Fprintf(&b, `ALTER TABLE %[1]s ADD COLUMN org_id %[2]s '%[3]s';
CREATE TRIGGER %[1]s_org_exists BEFORE INSERT ON %[1]s WHEN %[4]s AND NOT EXISTS(SELECT 1 FROM drive_orgs WHERE id=NEW.org_id AND status!='deleted')
BEGIN SELECT RAISE(ABORT,'%[5]s no such org'); END;
CREATE TRIGGER %[1]s_org_fixed BEFORE UPDATE OF org_id ON %[1]s WHEN NEW.org_id IS NOT OLD.org_id
BEGIN SELECT RAISE(ABORT,'%[5]s org_id is immutable'); END;
`, t, column, DefaultOrg, known, errOrg)
	}
	ws := func(id string) string { return `(SELECT org_id FROM drive_workspaces WHERE id=` + id + `)` }
	user := func(id string) string { return `(SELECT org_id FROM users WHERE id=` + id + `)` }
	group := func(id string) string { return `(SELECT org_id FROM groups WHERE id=` + id + `)` }
	// Each pair must name one org; a missing side is NULL and refused too. Updates are checked
	// only when they change a paired column, so a row whose principal is gone stays editable.
	for _, r := range []struct{ table, cols, when string }{
		{"group_members", "group_id,user_id", user("NEW.user_id") + ` IS NOT ` + group("NEW.group_id")},
		{"drive_personal_workspaces", "workspace,owner", user("NEW.owner") + ` IS NOT ` + ws("NEW.workspace")},
		{"drive_grants", "workspace,group_id", group("NEW.group_id") + ` IS NOT ` + ws("NEW.workspace")},
		{"drive_suite_groups", "workspace,grp", group("NEW.grp") + ` IS NOT ` + ws("NEW.workspace")},
		{"drive_shares", "workspace,principal_kind,principal_id", `NEW.principal_kind IN ('user','group') AND (CASE NEW.principal_kind WHEN 'user' THEN ` + user("NEW.principal_id") + ` ELSE ` + group("NEW.principal_id") + ` END) IS NOT ` + ws("NEW.workspace")},
		{"drive_link_users", "link,user_id", user("NEW.user_id") + ` IS NOT (SELECT w.org_id FROM drive_links l JOIN drive_workspaces w ON w.id=l.workspace WHERE l.id=NEW.link)`},
	} {
		fmt.Fprintf(&b, `CREATE TRIGGER %[1]s_one_org_insert BEFORE INSERT ON %[1]s WHEN %[3]s
BEGIN SELECT RAISE(ABORT,'%[4]s rows of two orgs'); END;
CREATE TRIGGER %[1]s_one_org_update BEFORE UPDATE OF %[2]s ON %[1]s WHEN %[3]s
BEGIN SELECT RAISE(ABORT,'%[4]s rows of two orgs'); END;
`, r.table, r.cols, r.when, errOrg)
	}
	// A blob belongs to one org (blobs are named by random IDs, so only a copy reuses one), and
	// files and folders move only between workspaces of one org.
	blobOrgs := `EXISTS(SELECT 1 FROM drive_versions v JOIN drive_files f ON f.id=v.file_id JOIN drive_workspaces w ON w.id=f.workspace WHERE v.blob=NEW.blob AND w.org_id IS NOT (SELECT w2.org_id FROM drive_files f2 JOIN drive_workspaces w2 ON w2.id=f2.workspace WHERE f2.id=NEW.file_id))`
	fmt.Fprintf(&b, `CREATE TRIGGER drive_versions_one_org_insert BEFORE INSERT ON drive_versions WHEN %[1]s
BEGIN SELECT RAISE(ABORT,'%[2]s blob of another org'); END;
CREATE TRIGGER drive_versions_one_org_update BEFORE UPDATE OF blob,file_id ON drive_versions WHEN %[1]s
BEGIN SELECT RAISE(ABORT,'%[2]s blob of another org'); END;
`, blobOrgs, errOrg)
	for _, t := range []string{"drive_files", "drive_folders"} {
		fmt.Fprintf(&b, "CREATE TRIGGER %[1]s_one_org_move BEFORE UPDATE OF workspace ON %[1]s WHEN %[2]s IS NOT %[3]s\nBEGIN SELECT RAISE(ABORT,'%[4]s move to another org'); END;\n", t, ws("NEW.workspace"), ws("OLD.workspace"), errOrg)
	}
	return b.String()
}

// recordTenancy records the tenancy mode at first start and refuses a database recorded with
// another, before the drive schema runs: schema 27 seeds the default org from the record.
func recordTenancy(ctx context.Context, db *sql.DB, mode string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO server_settings(key,value,updated_at) VALUES('tenancy_mode',?,?) ON CONFLICT(key) DO NOTHING`, mode, time.Now().UTC()); err != nil {
		return err
	}
	var recorded string
	if err = tx.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key='tenancy_mode'`).Scan(&recorded); err != nil {
		return err
	}
	if recorded != mode {
		return fmt.Errorf("this database is %s-tenant and this release opens only %s-tenant databases", recorded, mode)
	}
	return tx.Commit()
}

// asDenied reports a tenancy refusal as ErrDenied, the answer to any other cross-org access.
func asDenied(err error) error {
	if strings.Contains(fmt.Sprint(err), errOrg) && !errors.Is(err, ErrDenied) {
		return fmt.Errorf("%w: %v", ErrDenied, err)
	}
	return err
}
