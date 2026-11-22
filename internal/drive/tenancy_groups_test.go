package drive_test

import (
	"slices"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// Each org's suite groups make workspaces, groups and memberships in that org only.
func TestSuiteGroupsStayInTheirOrg(t *testing.T) {
	o := multiTenant(t)
	if _, err := o.db.Exec(`UPDATE users SET sso_subject='sub-'||id`); err != nil {
		t.Fatal(err)
	}
	family := func(id, state string, members ...string) suitedir.Group {
		return suitedir.Group{ID: id, Name: "Family", Slug: "family", State: state, Members: members}
	}
	reconcile := func(org string, g suitedir.Group) error {
		_, err := o.d.ReconcileSuiteGroups(o.ctx, org, []suitedir.Group{g})
		return err
	}
	row := func(kyid string) (s string) {
		_ = o.db.QueryRow(`SELECT w.org_id||' '||w.name||' '||w.deleted_at||'|'||g.org_id||' '||IFNULL((SELECT group_concat(user_id) FROM group_members WHERE group_id=s.grp),'') FROM drive_suite_groups s JOIN drive_workspaces w ON w.id=s.workspace JOIN groups g ON g.id=s.grp WHERE s.kyid=?`, kyid).Scan(&s)
		return s
	}
	mismatches := func(org string) (n int) {
		_ = o.db.QueryRow(`SELECT COUNT(*) FROM drive_events WHERE action='tenancy.org_mismatch' AND org_id=?`, org).Scan(&n)
		return n
	}

	// Both orgs get "Family": a name taken in another org is free here. org-a's list naming
	// b1 adds only a1, and audits b1 in org-a.
	if err := reconcile("org-a", family("ga", suitedir.GroupActive, "sub-a1", "sub-b1")); err != nil {
		t.Fatal(err)
	}
	if err := reconcile("org-b", family("gb", suitedir.GroupActive, "sub-b1")); err != nil {
		t.Fatal(err)
	}
	if err := reconcile("org-a", family("gc", suitedir.GroupActive)); err != nil {
		t.Fatal(err)
	}
	if row("ga") != "org-a Family |org-a a1" || row("gb") != "org-b Family |org-b b1" || row("gc") != "org-a Family (2) |org-a " {
		t.Fatalf("ga %q, gb %q, gc %q", row("ga"), row("gb"), row("gc"))
	}
	if mismatches("org-a") != 1 || mismatches("org-b") != 0 {
		t.Fatalf("mismatches: org-a %d, org-b %d", mismatches("org-a"), mismatches("org-b"))
	}
	var created int
	_ = o.db.QueryRow(`SELECT COUNT(*) FROM drive_events WHERE action='suite_group.created' AND org_id='org-b'`).Scan(&created)
	if created != 1 {
		t.Fatalf("org-b created events %d", created)
	}

	// org-b's answer naming org-a's group ID neither takes nor archives it.
	if err := reconcile("org-b", family("ga", suitedir.GroupActive, "sub-b2")); err == nil {
		t.Fatal("org-b took org-a's group")
	}
	if err := reconcile("org-b", family("ga", suitedir.GroupArchived)); err != nil {
		t.Fatal(err)
	}
	if row("ga") != "org-a Family |org-a a1" {
		t.Fatalf("ga after org-b: %q", row("ga"))
	}
}

func TestActiveOrgsListsOnlyServedOrgs(t *testing.T) {
	o := multiTenant(t)
	if _, err := o.db.Exec(`INSERT INTO drive_orgs(id,status,enabled) VALUES('org-c','suspended',1),('org-d','active',0)`); err != nil {
		t.Fatal(err)
	}
	got, err := o.d.ActiveOrgs(o.ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"org-a", "org-b", drive.TestStaffOrg}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("active orgs %v, want %v", got, want)
	}
}
