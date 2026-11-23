package drive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/google/uuid"
)

// DefaultOrg is a single-tenant install's one org, the fixed ID KyIdentity uses for it.
const DefaultOrg = "00000000-0000-0000-0000-000000000001"

// The tenancy modes. Configuration offers only single until multi-tenant mode ships; tests
// open multi-tenant stores directly.
const (
	TenancySingle = "single"
	TenancyMulti  = "multi"
)

// Tenancy is how a database is shared. StaffOrg is the org of KyDrive's own staff, the only
// accounts that may administer a multi-tenant install (D9); single-tenant leaves it empty.
type Tenancy struct{ Mode, StaffOrg string }

func (t Tenancy) check() error {
	switch t.Mode {
	case TenancySingle:
		if t.StaffOrg != "" {
			return errors.New("a single-tenant drive has no staff org")
		}
	case TenancyMulti:
		if id, err := uuid.Parse(t.StaffOrg); err != nil || id.String() != t.StaffOrg || t.StaffOrg == DefaultOrg {
			return fmt.Errorf("staff org %q is not a canonical UUID other than the default org", t.StaffOrg)
		}
	default:
		return fmt.Errorf("unknown tenancy mode %q", t.Mode)
	}
	return nil
}

// errOrg prefixes every tenancy trigger's message; transaction reports it as ErrDenied.
const errOrg = "tenancy:"

// tenancySchema is drive schema 27: the org registry, org_id on the tables its caller lists
// (frozen there, as a shipped entry must be) and the triggers that keep each row inside one
// org. The default org row exists only on a database recorded single-tenant (recordTenancy
// runs first). An org-owned table added later needs its own entry.
func tenancySchema(d dialect, owned []string, nullable map[string]bool) string {
	var b strings.Builder
	orgs := `CREATE TABLE drive_orgs(id TEXT PRIMARY KEY,status TEXT NOT NULL CHECK(status IN ('active','suspended','deletion_scheduled','deleted')),revision INTEGER NOT NULL DEFAULT 0,enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),storage_bytes INTEGER CHECK(storage_bytes>0),purged INTEGER NOT NULL DEFAULT 0 CHECK(purged IN (0,1)),seeded INTEGER NOT NULL DEFAULT 0 CHECK(seeded IN (0,1)),retention_after TEXT NOT NULL DEFAULT '');
INSERT INTO drive_orgs(id,status) SELECT '` + DefaultOrg + `','active' WHERE (SELECT value FROM server_settings WHERE key='tenancy_mode')='single';
`
	if d.pg {
		orgs = strings.ReplaceAll(orgs, "INTEGER", "BIGINT") // SQLite's INTEGER is 64-bit
	}
	b.WriteString(orgs)
	for _, t := range owned {
		column, known := "TEXT NOT NULL DEFAULT '"+DefaultOrg+"'", "true"
		if nullable[t] {
			column, known = "TEXT DEFAULT '"+DefaultOrg+"'", "NEW.org_id IS NOT NULL"
		}
		if d.pg {
			column += " REFERENCES drive_orgs(id)"
		}
		fmt.Fprintf(&b, "ALTER TABLE %s ADD COLUMN org_id %s;\n", t, column)
		b.WriteString(d.trigger(t+"_org_exists", "INSERT", t, known+" AND NOT EXISTS(SELECT 1 FROM drive_orgs WHERE id=NEW.org_id AND status!='deleted')", errOrg+" no such org"))
		b.WriteString(d.trigger(t+"_org_fixed", "UPDATE OF org_id", t, "NEW.org_id"+d.isNot()+"OLD.org_id", errOrg+" org_id is immutable"))
	}
	ws := func(id string) string { return `(SELECT org_id FROM drive_workspaces WHERE id=` + id + `)` }
	user := func(id string) string { return `(SELECT org_id FROM users WHERE id=` + id + `)` }
	group := func(id string) string { return `(SELECT org_id FROM groups WHERE id=` + id + `)` }
	// Each pair must name one org; a missing side is NULL and refused too. Updates are checked
	// only when they change a paired column, so a row whose principal is gone stays editable.
	ne := d.isNot()
	for _, r := range []struct{ table, cols, when string }{
		{"group_members", "group_id,user_id", user("NEW.user_id") + ne + group("NEW.group_id")},
		{"drive_personal_workspaces", "workspace,owner", user("NEW.owner") + ne + ws("NEW.workspace")},
		{"drive_grants", "workspace,group_id", group("NEW.group_id") + ne + ws("NEW.workspace")},
		{"drive_suite_groups", "workspace,grp", group("NEW.grp") + ne + ws("NEW.workspace")},
		{"drive_shares", "workspace,principal_kind,principal_id", `NEW.principal_kind IN ('user','group') AND (CASE NEW.principal_kind WHEN 'user' THEN ` + user("NEW.principal_id") + ` ELSE ` + group("NEW.principal_id") + ` END)` + ne + ws("NEW.workspace")},
		{"drive_link_users", "link,user_id", user("NEW.user_id") + ne + `(SELECT w.org_id FROM drive_links l JOIN drive_workspaces w ON w.id=l.workspace WHERE l.id=NEW.link)`},
	} {
		b.WriteString(d.trigger(r.table+"_one_org_insert", "INSERT", r.table, r.when, errOrg+" rows of two orgs"))
		b.WriteString(d.trigger(r.table+"_one_org_update", "UPDATE OF "+r.cols, r.table, r.when, errOrg+" rows of two orgs"))
	}
	// A blob belongs to one org (blobs are named by random IDs, so only a copy reuses one), and
	// files and folders move only between workspaces of one org.
	blobOrgs := `EXISTS(SELECT 1 FROM drive_versions v JOIN drive_files f ON f.id=v.file_id JOIN drive_workspaces w ON w.id=f.workspace WHERE v.blob=NEW.blob AND w.org_id` + ne + `(SELECT w2.org_id FROM drive_files f2 JOIN drive_workspaces w2 ON w2.id=f2.workspace WHERE f2.id=NEW.file_id))`
	b.WriteString(d.trigger("drive_versions_one_org_insert", "INSERT", "drive_versions", blobOrgs, errOrg+" blob of another org"))
	b.WriteString(d.trigger("drive_versions_one_org_update", "UPDATE OF blob,file_id", "drive_versions", blobOrgs, errOrg+" blob of another org"))
	for _, t := range []string{"drive_files", "drive_folders"} {
		b.WriteString(d.trigger(t+"_one_org_move", "UPDATE OF workspace", t, ws("NEW.workspace")+ne+ws("OLD.workspace"), errOrg+" move to another org"))
	}
	return b.String()
}

// multiTenantSQL is true on a database recorded multi-tenant.
const multiTenantSQL = `COALESCE((SELECT value FROM server_settings WHERE key='tenancy_mode'),'')='multi'`

// staffSQL is true unless the database is multi-tenant and account $U is outside the staff org.
func staffSQL(user string) string {
	return `(NOT ` + multiTenantSQL + ` OR (SELECT org_id FROM users WHERE id=` + user + `)=(SELECT value FROM server_settings WHERE key='staff_org'))`
}

// staffAdminSchema is drive schema 28: a multi-tenant database refuses role='admin' outside
// the staff org.
func staffAdminSchema(d dialect) string {
	cond := `NEW.role='admin' AND ` + multiTenantSQL + ` AND NEW.org_id` + d.isNot() + `(SELECT value FROM server_settings WHERE key='staff_org')`
	return strings.TrimSuffix(d.trigger("users_admin_is_staff_insert", "INSERT", "users", cond, errOrg+" administrators are staff")+
		d.trigger("users_admin_is_staff_update", "UPDATE OF role", "users", cond, errOrg+" administrators are staff"), "\n")
}

// orgOwned are the tables schema 27 gives org_id; orgNullable may hold NULL there.
var (
	orgOwned = []string{
		"users", "groups", "audit_records",
		"drive_workspaces", "drive_events", "drive_service_tokens", "drive_editor_documents",
		"drive_editor_saves", "drive_app_passwords", "drive_changes", "drive_change_floor",
		"drive_devices", "drive_device_codes", "drive_peer_settings", "drive_peer_folders",
		"drive_peer_tickets", "drive_peer_picks", "drive_peer_guest", "drive_view_copies",
	}
	orgNullable = map[string]bool{"audit_records": true, "drive_events": true}
)

// recordTenancy records the tenancy mode, and a multi-tenant database's staff org, at first
// start and refuses a database recorded otherwise, before the drive schema runs: schema 27
// seeds the default org from the record.
func recordTenancy(ctx context.Context, db *sql.DB, t Tenancy) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	settings := [][2]string{{"tenancy_mode", t.Mode}}
	if t.Mode == TenancyMulti {
		settings = append(settings, [2]string{"staff_org", t.StaffOrg})
	}
	for _, kv := range settings {
		if _, err = tx.ExecContext(ctx, `INSERT INTO server_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, kv[0], kv[1], time.Now().UTC()); err != nil {
			return err
		}
		var recorded string
		if err = tx.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE key=?`, kv[0]).Scan(&recorded); err != nil {
			return err
		}
		if recorded != kv[1] && kv[0] == "tenancy_mode" {
			return fmt.Errorf("this database is %s-tenant and this server is configured %s-tenant", recorded, kv[1])
		}
		if recorded != kv[1] {
			return fmt.Errorf("this database's staff org is %s, not %s", recorded, kv[1])
		}
	}
	// A KyIdentity subject is one account, so the org comparison at every entry point has one
	// row to compare with. Multi-tenant only: a single-tenant install may hold duplicates.
	if t.Mode == TenancyMulti {
		if _, err = tx.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS idx_users_kysignon_subject ON users(sso_subject) WHERE sso_provider='kysignon'`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// seedStaffOrg gives a multi-tenant database its staff org row, active and seeded, which the
// org poller never changes.
func seedStaffOrg(ctx context.Context, db *sql.DB, t Tenancy) error {
	if t.Mode != TenancyMulti {
		return nil
	}
	_, err := db.ExecContext(ctx, `INSERT INTO drive_orgs(id,status,seeded) VALUES(?,'active',1) ON CONFLICT(id) DO NOTHING`, t.StaffOrg)
	return err
}

// asDenied reports a tenancy refusal as ErrDenied, the answer to any other cross-org access.
func asDenied(err error) error {
	if strings.Contains(fmt.Sprint(err), errOrg) && !errors.Is(err, ErrDenied) {
		return fmt.Errorf("%w: %v", ErrDenied, err)
	}
	return err
}

// activePrincipal is true while user $U is active and their org is active and enabled: the
// one test every credential and grant reads. $U is a column reference.
func activePrincipal(user string) string {
	return `EXISTS(SELECT 1 FROM users apu JOIN drive_orgs apo ON apo.id=apu.org_id WHERE apu.id=` + user + ` AND apu.status='active' AND ` + orgServing("apo") + `)`
}

// OrgsFreshUntil is the server_settings key holding the unix time until which the last
// successful org poll is trusted (OrgsMaxAge after it).
const OrgsFreshUntil = "orgs_fresh_until"

// OrgsMaxAge is how long the org mirror is trusted after the last successful poll. Past it
// every org KyIdentity reports is served nothing, until a poll succeeds: an outage shorter
// than this keeps serving (question 4), a longer one fails closed.
const OrgsMaxAge = time.Hour

// orgServing is true while org row $O is active and enabled and, unless it is the seeded
// staff org or the single-tenant default org, the mirror is fresh. $O is a table alias.
func orgServing(o string) string {
	return o + `.status='active' AND ` + o + `.enabled=1 AND (` + o + `.seeded=1 OR ` + o + `.id='` + DefaultOrg + `' OR COALESCE(CAST((SELECT value FROM server_settings WHERE key='` + OrgsFreshUntil + `') AS BIGINT),0)>unixepoch())`
}

// sameOrg is true when workspace $W is in user $U's org. Both are column references.
func sameOrg(user, workspace string) string {
	return `EXISTS(SELECT 1 FROM drive_workspaces sow JOIN users sou ON sou.org_id=sow.org_id WHERE sow.id=` + workspace + ` AND sou.id=` + user + `)`
}

// userOrg is user $U's org. A user with no row is in the default org on a single-tenant
// database and in none (NULL, refused by every org-owned table) on a multi-tenant one.
func userOrg(user string) string {
	return `COALESCE((SELECT org_id FROM users WHERE id=` + user + `),(SELECT id FROM drive_orgs WHERE id='` + DefaultOrg + `'))`
}

// workspaceOrg is workspace $W's org.
func workspaceOrg(workspace string) string {
	return `(SELECT org_id FROM drive_workspaces WHERE id=` + workspace + `)`
}

// ActivePrincipal reports whether user is active and in an active, enabled org.
func (s *Store) ActivePrincipal(ctx context.Context, user string) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT `+activePrincipal("?"), user).Scan(&ok)
	return ok, err
}

type orgKey struct{}

// WithOrg names the org an administrator acts in, as staff select it (X-Ky-Org). Without it
// an administrator acts in their own org.
func WithOrg(ctx context.Context, org string) context.Context {
	return context.WithValue(ctx, orgKey{}, org)
}

// adminOrg is the org administrator user acts in.
func adminOrg(ctx context.Context, q queryer, user string) (string, error) {
	if org, _ := ctx.Value(orgKey{}).(string); org != "" {
		return org, nil
	}
	var org sql.NullString
	err := q.QueryRowContext(ctx, `SELECT `+userOrg("?"), user).Scan(&org)
	return org.String, err
}

// adminIn is admin, for workspace in the org the administrator acts in; a workspace in any
// other org is ErrDenied, as for a non-administrator.
func adminIn(ctx context.Context, q queryer, user, workspace string) error {
	if err := admin(ctx, q, user); err != nil {
		return err
	}
	org, err := adminOrg(ctx, q, user)
	if err != nil {
		return err
	}
	var in int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_workspaces WHERE id=? AND org_id=?`, workspace, org).Scan(&in); err != nil {
		return err
	}
	if in != 1 {
		return ErrDenied
	}
	return nil
}

// singleTenant refuses, on a multi-tenant store, what customers have no administrator for:
// creating, renaming, deleting, re-quotaing and per-workspace retention of shared workspaces
// (D10). Suite groups own shared workspaces there.
func (s *Store) singleTenant() error {
	if s.tenancy.Mode == TenancyMulti {
		return ErrNotFound
	}
	return nil
}

// Multi reports whether the store is multi-tenant.
func (s *Store) Multi() bool { return s.tenancy.Mode == TenancyMulti }

// Staff reports whether user may hold role='admin': always on a single-tenant store, only
// in the staff org on a multi-tenant one.
func (s *Store) Staff(ctx context.Context, user string) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT `+staffSQL("?"), user).Scan(&ok)
	return ok, err
}

// OrgSelectable reports whether staff may select org: a registered org that is not deleted.
func (s *Store) OrgSelectable(ctx context.Context, org string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_orgs WHERE id=? AND status!='deleted'`, org).Scan(&n)
	return n == 1, err
}

// StaffRead records in the org staff act in (WithOrg) that user read what names.
func (s *Store) StaffRead(ctx context.Context, user, what string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error { return event(ctx, tx, user, "staff.read", what) })
}

// OrgGroups is the set of groups in the org staff act in (WithOrg).
func (s *Store) OrgGroups(ctx context.Context) (map[string]bool, error) {
	org, _ := ctx.Value(orgKey{}).(string)
	ids, err := column(ctx, s.db, `SELECT id FROM groups WHERE org_id=?`, org)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// inOrgOf is ctx acting in the org query (one row, one column) finds, so a worker's event lands
// in the org of the row it is about to change.
func inOrgOf(ctx context.Context, q queryer, query string, args ...any) (context.Context, error) {
	var org sql.NullString
	if err := q.QueryRowContext(ctx, query, args...).Scan(&org); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ctx, err
	}
	return WithOrg(ctx, org.String), nil
}

const fileOrg = `SELECT w.org_id FROM drive_files f JOIN drive_workspaces w ON w.id=f.workspace WHERE f.id=?`

// UserOrgSQL is userOrg for SQL built outside the drive store; user is a column reference or
// placeholder.
func UserOrgSQL(user string) string { return userOrg(user) }

// UserOrg is user's org, "" for an account with no row.
func (s *Store) UserOrg(ctx context.Context, user string) (string, error) {
	var org sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT org_id FROM users WHERE id=?`, user).Scan(&org)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return org.String, err
}

// OrgActive reports whether org is registered, and whether it is active and enabled.
func (s *Store) OrgActive(ctx context.Context, org string) (known, active bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT 1, `+orgServing("o")+` FROM drive_orgs o WHERE o.id=?`, org).Scan(&known, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return known, active, err
}

// ActiveOrgs lists the registered orgs that are active and enabled, the orgs whose suite groups
// are read.
func (s *Store) ActiveOrgs(ctx context.Context) ([]string, error) {
	return column(ctx, s.db, `SELECT o.id FROM drive_orgs o WHERE `+orgServing("o")+` ORDER BY o.id`)
}

// orgsSince is the server_settings key holding the revision the next org poll starts after.
const orgsSince = "orgs_since"

// OrgsSince is the revision the next /api/suite/orgs poll asks for changes after.
func (s *Store) OrgsSince(ctx context.Context) (int64, error) {
	var since int64
	err := s.db.QueryRowContext(ctx, `SELECT CAST(value AS INTEGER) FROM server_settings WHERE key=?`, orgsSince).Scan(&since)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return since, err
}

// ApplyOrgs mirrors a page of /api/suite/orgs into the registry and records next, in one
// transaction. An older revision than the row's changes nothing. The seeded staff org is never
// changed; staffNotActive reports that the directory listed it as anything but active.
func (s *Store) ApplyOrgs(ctx context.Context, orgs []suitedir.Org, next int64) (staffNotActive bool, err error) {
	if !s.Multi() {
		return false, ErrNotFound
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		staffNotActive = false
		for _, o := range orgs {
			if o.ID == s.tenancy.StaffOrg {
				staffNotActive = staffNotActive || o.Status != suitedir.OrgActive || !o.Enabled
				continue
			}
			// A deleted org stays deleted: no later answer brings it back.
			if _, err := tx.ExecContext(ctx, `INSERT INTO drive_orgs(id,status,revision,enabled,storage_bytes) VALUES(?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET status=excluded.status,revision=excluded.revision,enabled=excluded.enabled,storage_bytes=excluded.storage_bytes
WHERE drive_orgs.seeded=0 AND drive_orgs.status<>'deleted' AND drive_orgs.revision<excluded.revision`, o.ID, o.Status, o.Revision, o.Enabled, storageBytes(o.Quotas)); err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		for k, v := range map[string]string{orgsSince: strconv.FormatInt(next, 10), OrgsFreshUntil: strconv.FormatInt(now.Add(OrgsMaxAge).Unix(), 10)} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO server_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, k, v, now); err != nil {
				return err
			}
		}
		return nil
	})
	return staffNotActive, err
}

// storageBytes is quotas' storage_bytes, or nil (no pool, so writes are refused) unless it is
// a positive integer.
func storageBytes(quotas json.RawMessage) any {
	var q struct {
		StorageBytes *int64 `json:"storage_bytes"`
	}
	if json.Unmarshal(quotas, &q) != nil || q.StorageBytes == nil || *q.StorageBytes <= 0 {
		return nil
	}
	return *q.StorageBytes
}
