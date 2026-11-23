package drive_test

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store/migrations"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
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
	// An expired stamp, and none at all (as a restore leaves it).
	for _, stale := range []string{`UPDATE server_settings SET value='1' WHERE key='orgs_fresh_until'`, `DELETE FROM server_settings WHERE key='orgs_fresh_until'`} {
		if _, err := o.db.Exec(stale); err != nil {
			t.Fatal(err)
		}
		if ok, err := d.ActivePrincipal(ctx, "a1"); err != nil || ok {
			t.Fatalf("%s: stale mirror still serves a member: %v %v", stale, ok, err)
		}
		if known, active, err := d.OrgActive(ctx, "org-a"); err != nil || !known || active {
			t.Fatalf("%s: stale mirror reports org-a known=%v active=%v: %v", stale, known, active, err)
		}
		if orgs, err := d.ActiveOrgs(ctx); err != nil || len(orgs) != 1 || orgs[0] != drive.TestStaffOrg {
			t.Fatalf("%s: stale mirror lists %v (%v), want only the staff org", stale, orgs, err)
		}
		if ok, err := d.ActivePrincipal(ctx, "staff"); err != nil || !ok {
			t.Fatalf("%s: staff stopped with the mirror: %v %v", stale, ok, err)
		}
		if _, err := d.ApplyOrgs(ctx, nil, 0); err != nil {
			t.Fatal(err)
		}
		if ok, err := d.ActivePrincipal(ctx, "a1"); err != nil || !ok {
			t.Fatalf("%s: a successful poll did not restore the member: %v %v", stale, ok, err)
		}
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

// Independent database handles model replicas; no process-local poll lock protects this row.
func TestOrgCursorNeverMovesBack(t *testing.T) {
	cfg := testdb.Config(t)
	first := testdb.Open(t, cfg)
	if err := migrations.Run(t.Context(), first, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	a, err := drive.OpenMulti(t.Context(), first, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	second := testdb.Open(t, cfg)
	b, err := drive.OpenMulti(t.Context(), second, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	check := func(want int64) {
		t.Helper()
		for _, d := range []*drive.Store{a, b} {
			if got, err := d.OrgsSince(t.Context()); err != nil || got != want {
				t.Fatalf("cursor %d, want %d: %v", got, want, err)
			}
		}
	}
	check(0)
	// Decimal strings must compare numerically, not lexically. PostgreSQL INTEGER is
	// only 32 bits; the suite revision cursor is int64 on both backends.
	for _, next := range []int64{9, 10, 2, 10, 0} {
		if _, err := a.ApplyOrgs(t.Context(), nil, next); err != nil {
			t.Fatal(err)
		}
		if next == 9 {
			check(9)
		} else {
			check(10)
		}
	}
	const high int64 = 1 << 40
	if _, err := a.ApplyOrgs(t.Context(), nil, high); err != nil {
		t.Fatal(err)
	}
	check(high)
	start := make(chan struct{})
	failures := make(chan error, 2)
	var wg sync.WaitGroup
	for _, d := range []*drive.Store{a, b} {
		wg.Go(func() {
			<-start
			for next := int64(1); next <= 12; next++ {
				if _, err := d.ApplyOrgs(t.Context(), nil, next); err != nil {
					failures <- err
					return
				}
			}
		})
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	check(high)
	// A failed page must publish neither its rows nor a later cursor.
	if _, err := a.ApplyOrgs(t.Context(), []suitedir.Org{
		{ID: "org-valid", Status: "active", Enabled: true, Revision: high + 1},
		{ID: "org-invalid", Status: "invalid", Enabled: true, Revision: high + 2},
	}, high+2); err == nil {
		t.Fatal("invalid page accepted")
	}
	check(high)
	if known, _, err := b.OrgActive(t.Context(), "org-valid"); err != nil || known {
		t.Fatal("failed page left an org", err)
	}
}
