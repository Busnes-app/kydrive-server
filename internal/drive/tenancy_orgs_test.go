package drive_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func TestApplyOrgsMirrorsTheDirectory(t *testing.T) {
	o := multiTenant(t)
	org := func(id, status string, rev int64, quotas string) suitedir.Org {
		return suitedir.Org{ID: id, Status: status, Revision: rev, Enabled: true, Quotas: json.RawMessage(quotas)}
	}
	row := func(id string) (s string) {
		_ = o.db.QueryRow(`SELECT status||' '||revision||' '||enabled||' '||COALESCE(CAST(storage_bytes AS TEXT),'none')||' '||seeded FROM drive_orgs WHERE id=?`, id).Scan(&s)
		return s
	}
	staff, err := o.d.ApplyOrgs(o.ctx, []suitedir.Org{
		org("org-c", "active", 2, `{"storage_bytes":500}`),
		org("org-a", "suspended", 3, `{"storage_bytes":5.5}`),
		org(drive.TestStaffOrg, "suspended", 4, `{}`),
	}, 4)
	if err != nil || !staff {
		t.Fatalf("staff %v, %v", staff, err)
	}
	if row("org-c") != "active 2 1 500 0" || row("org-a") != "suspended 3 1 none 0" || row(drive.TestStaffOrg) != "active 0 1 none 1" {
		t.Fatalf("org-c %q, org-a %q, staff %q", row("org-c"), row("org-a"), row(drive.TestStaffOrg))
	}
	if since, err := o.d.OrgsSince(o.ctx); err != nil || since != 4 {
		t.Fatalf("since %d, %v", since, err)
	}
	// An older revision changes nothing; only a positive integer is a storage pool.
	for _, quotas := range []string{`{"storage_bytes":-1}`, `{"storage_bytes":"500"}`, `{}`} {
		if _, err := o.d.ApplyOrgs(o.ctx, []suitedir.Org{org("org-c", "deleted", 1, `{}`), org("org-b", "active", 5, quotas)}, 5); err != nil {
			t.Fatal(err)
		}
		if row("org-c") != "active 2 1 500 0" || row("org-b") != "active 5 1 none 0" {
			t.Fatalf("%s: org-c %q, org-b %q", quotas, row("org-c"), row("org-b"))
		}
		if _, err := o.db.Exec(`UPDATE drive_orgs SET revision=0 WHERE id='org-b'`); err != nil {
			t.Fatal(err)
		}
	}
	// Enabled false is applied, and a staff org listed active raises nothing.
	off := org("org-c", "active", 6, `{"storage_bytes":500}`)
	off.Enabled = false
	if staff, err := o.d.ApplyOrgs(o.ctx, []suitedir.Org{off, org(drive.TestStaffOrg, "active", 6, `{}`)}, 6); err != nil || staff || row("org-c") != "active 6 0 500 0" {
		t.Fatalf("staff %v, org-c %q, %v", staff, row("org-c"), err)
	}
}

func TestSingleTenantHasNoOrgDirectory(t *testing.T) {
	st, db := tenancyDB(t)
	if _, err := st.Drive().ApplyOrgs(t.Context(), []suitedir.Org{{ID: "org-x", Status: "active", Revision: 1, Quotas: json.RawMessage(`{}`)}}, 1); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("single-tenant ApplyOrgs: %v", err)
	}
	// A single-tenant install keeps its non-unique subject index: duplicates there predate tenancy.
	for _, id := range []string{"u1", "u2"} {
		if _, err := db.Exec(`INSERT INTO users(id,username,email,role,status,sso_provider,sso_subject,created_at,updated_at) VALUES(?,?,'','user','active','kysignon','same',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`, id, id); err != nil {
			t.Fatal(err)
		}
	}
}

// A multi-tenant KyIdentity subject is one account, so each entry point compares one org.
func TestMultiTenantSubjectIsOneAccount(t *testing.T) {
	o := multiTenant(t)
	insert := func(id, provider string) error {
		_, err := o.db.Exec(`INSERT INTO users(id,username,email,role,status,sso_provider,sso_subject,created_at,updated_at,org_id) VALUES(?,?,'','user','active',?,'same',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'org-a')`, id, id, provider)
		return err
	}
	if err := insert("k1", "kysignon"); err != nil {
		t.Fatal(err)
	}
	if err := insert("k2", "kysignon"); err == nil {
		t.Fatal("second kysignon account for one subject")
	}
	if err := insert("s1", "scim"); err != nil {
		t.Fatalf("index reaches past kysignon: %v", err)
	}
}

// Once the last successful poll is older than OrgsMaxAge, every org KyIdentity reports is served
// nothing; the staff org still is, and the next successful poll restores the rest.
func TestStaleOrgMirrorServesNothing(t *testing.T) {
	o := multiTenant(t)
	ctx, d := o.ctx, o.d
	if ok, err := d.ActivePrincipal(ctx, "a1"); err != nil || !ok {
		t.Fatalf("fresh mirror: %v %v", ok, err)
	}
	if _, err := o.db.Exec(`UPDATE server_settings SET value='1' WHERE key='orgs_fresh_until'`); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.ActivePrincipal(ctx, "a1"); err != nil || ok {
		t.Fatalf("stale mirror still serves a member: %v %v", ok, err)
	}
	if _, active, err := d.OrgActive(ctx, "org-a"); err != nil || active {
		t.Fatalf("stale mirror reports org-a active: %v %v", active, err)
	}
	if orgs, err := d.ActiveOrgs(ctx); err != nil || len(orgs) != 1 || orgs[0] != drive.TestStaffOrg {
		t.Fatalf("stale mirror lists %v (%v), want only the staff org", orgs, err)
	}
	if ok, err := d.ActivePrincipal(ctx, "staff"); err != nil || !ok {
		t.Fatalf("staff stopped with the mirror: %v %v", ok, err)
	}
	if _, err := d.ApplyOrgs(ctx, nil, 0); err != nil {
		t.Fatal(err)
	}
	if ok, err := d.ActivePrincipal(ctx, "a1"); err != nil || !ok {
		t.Fatalf("a successful poll did not restore the member: %v %v", ok, err)
	}
}

// A deleted org stays deleted: a later revision saying otherwise changes nothing.
func TestDeletedOrgStaysDeleted(t *testing.T) {
	o := multiTenant(t)
	org := func(status string, rev int64) suitedir.Org {
		return suitedir.Org{ID: "org-x", Status: status, Revision: rev, Enabled: true, Quotas: json.RawMessage(`{}`)}
	}
	for _, batch := range [][]suitedir.Org{{org("deleted", 5)}, {org("active", 6)}} {
		if _, err := o.d.ApplyOrgs(o.ctx, batch, 6); err != nil {
			t.Fatal(err)
		}
	}
	var status string
	if err := o.db.QueryRow(`SELECT status FROM drive_orgs WHERE id='org-x'`).Scan(&status); err != nil || status != "deleted" {
		t.Fatalf("status %q (%v)", status, err)
	}
}
