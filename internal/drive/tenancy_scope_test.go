package drive_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store/migrations"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/google/uuid"
)

// orgs is a multi-tenant drive store holding a staff administrator and orgs A and B, each
// with two members in one group, a shared workspace called "Team" that group manages, and a file
// in it. A also has a leaver whose My files holds a file.
type orgs struct {
	ctx            context.Context
	d              *drive.Store
	db             *sql.DB
	root           string
	teamA, teamB   drive.Workspace
	fileA, fileB   drive.File
	folderB        drive.Folder
	leaverA        string
	linkB, guestB  string
	personalB      string
	groupA, groupB string
}

func multiTenant(t *testing.T) *orgs {
	t.Helper()
	ctx := context.Background()
	cfg := testdb.Config(t)
	db := testdb.Open(t, cfg)
	if err := migrations.Run(ctx, db, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	d, err := drive.OpenMulti(ctx, db, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	o := &orgs{ctx: ctx, d: d, db: db, root: t.TempDir(), groupA: "group-a", groupB: "group-b"}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(q, err)
		}
	}
	exec(`INSERT INTO drive_orgs(id,status) VALUES('org-a','active'),('org-b','active')`)
	for _, u := range [][4]string{
		{"staff", "admin", "active", drive.TestStaffOrg},
		{"a1", "user", "active", "org-a"}, {"a2", "user", "active", "org-a"}, {"a-gone", "user", "inactive", "org-a"},
		{"b1", "user", "active", "org-b"}, {"b2", "user", "active", "org-b"},
	} {
		exec(`INSERT INTO users(id,username,email,display_name,role,status,sso_provider,created_at,updated_at,org_id) VALUES(?,?,?,?,?,?,'scim',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, u[0], u[0], u[0]+"@local.test", "Person "+u[0], u[1], u[2], u[3])
	}
	for _, g := range [][3]string{{o.groupA, "org-a", "a1 a2"}, {o.groupB, "org-b", "b1 b2"}} {
		exec(`INSERT INTO groups(id,display_name,external_id,created_at,updated_at,org_id) VALUES(?,?,'',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, g[0], g[0], g[1])
		for _, m := range []string{g[2][:2], g[2][3:]} {
			exec(`INSERT INTO group_members(group_id,user_id) VALUES(?,?)`, g[0], m)
		}
	}
	// Suite groups own shared workspaces on a multi-tenant store; these stand in for theirs.
	team := func(org, group string) (drive.Workspace, drive.File) {
		t.Helper()
		w := drive.Workspace{ID: uuid.NewString(), Name: "Team"}
		exec(`INSERT INTO drive_workspaces(id,name,quota,org_id) VALUES(?,?,?,?)`, w.ID, w.Name, 1<<20, org)
		exec(`INSERT INTO drive_grants VALUES(?,?,'manager')`, w.ID, group)
		f, err := d.Publish(ctx, group[len(group)-1:]+"1", drive.File{Workspace: w.ID, Name: "plan.txt"}, blob(t, o.root, "plan"), 0)
		if err != nil {
			t.Fatal(err)
		}
		return w, f
	}
	o.teamA, o.fileA = team("org-a", o.groupA)
	o.teamB, o.fileB = team("org-b", o.groupB)
	if o.folderB, err = d.AddFolder(ctx, "b1", o.teamB.ID, "", "zebracrossing"); err != nil {
		t.Fatal(err)
	}
	if o.personalB, err = d.EnsurePersonalWorkspace(ctx, "b1"); err != nil {
		t.Fatal(err)
	}
	if _, o.linkB, err = d.CreateLink(ctx, "b1", "file", o.fileB.ID, "reader", 0); err != nil {
		t.Fatal(err)
	}
	if _, o.guestB, err = d.CreateGuestLink(ctx, "b1", "file", o.fileB.ID, time.Now().Add(time.Hour).Unix(), "", false, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	// a-gone leaves files behind in their My files.
	exec(`UPDATE users SET status='active' WHERE id='a-gone'`)
	if o.leaverA, err = d.EnsurePersonalWorkspace(ctx, "a-gone"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.Publish(ctx, "a-gone", drive.File{Workspace: o.leaverA, Name: "notes.txt"}, blob(t, o.root, "notes"), 0); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE users SET status='inactive' WHERE id='a-gone'`)
	return o
}

// same requires a call with org B's real ID to fail exactly as with an ID that never existed.
func same(t *testing.T, what string, cross, missing error) {
	t.Helper()
	if cross == nil {
		t.Errorf("%s: another org's ID succeeded", what)
		return
	}
	if fmt.Sprint(cross) != fmt.Sprint(missing) {
		t.Errorf("%s: another org's ID answered %v, a missing one %v", what, cross, missing)
	}
}

// A member of org A reaches nothing of org B's by naming B's IDs, and every refusal is the one
// a random ID gets.
func TestCrossOrgStore(t *testing.T) {
	o := multiTenant(t)
	ctx, d := o.ctx, o.d
	missing := uuid.NewString()

	for _, need := range []int{1, 2, 3} {
		same(t, fmt.Sprintf("Authorize %d", need), d.Authorize(ctx, "a1", o.teamB.ID, need), d.Authorize(ctx, "a1", missing, need))
	}
	got, cross := d.File(ctx, "a1", o.fileB.ID, 1)
	_, miss := d.File(ctx, "a1", missing, 1)
	same(t, "File", cross, miss)
	if got != (drive.File{}) {
		t.Errorf("File returned another org's file with its refusal: %+v", got)
	}
	_, cross = d.Share(ctx, "a1", "file", o.fileB.ID, "user", "a2", "reader")
	_, miss = d.Share(ctx, "a1", "file", missing, "user", "a2", "reader")
	same(t, "Share another org's file", cross, miss)
	_, cross = d.Share(ctx, "a1", "file", o.fileA.ID, "user", "b1", "reader")
	_, miss = d.Share(ctx, "a1", "file", o.fileA.ID, "user", missing, "reader")
	same(t, "Share with another org's user", cross, miss)
	_, cross = d.Share(ctx, "a1", "file", o.fileA.ID, "group", o.groupB, "reader")
	_, miss = d.Share(ctx, "a1", "file", o.fileA.ID, "group", missing, "reader")
	same(t, "Share with another org's group", cross, miss)
	same(t, "Grant another org's group", d.Grant(ctx, "a1", o.teamA.ID, o.groupB, "reader"), d.Grant(ctx, "a1", o.teamA.ID, missing, "reader"))
	_, cross = d.MoveFile(ctx, "a1", o.fileA.ID, 1, drive.Location{Workspace: o.teamB.ID, Name: "plan.txt"})
	_, miss = d.MoveFile(ctx, "a1", o.fileA.ID, 1, drive.Location{Workspace: missing, Name: "plan.txt"})
	same(t, "Move into another org", cross, miss)
	_, cross = d.MoveFile(ctx, "a1", o.fileB.ID, 1, drive.Location{Workspace: o.teamA.ID, Name: "plan.txt"})
	_, miss = d.MoveFile(ctx, "a1", missing, 1, drive.Location{Workspace: o.teamA.ID, Name: "plan.txt"})
	same(t, "Move out of another org", cross, miss)
	moved, cross := d.MoveFolder(ctx, "a1", o.folderB.ID, "", "mine")
	_, miss = d.MoveFolder(ctx, "a1", missing, "", "mine")
	same(t, "Move another org's folder", cross, miss)
	if moved.Workspace != "" {
		t.Errorf("MoveFolder named another org's workspace with its refusal: %s", moved.Workspace)
	}
	_, cross = d.CopyFile(ctx, "a1", o.fileB.ID, drive.Location{Workspace: o.teamA.ID, Name: "copy.txt"})
	_, miss = d.CopyFile(ctx, "a1", missing, drive.Location{Workspace: o.teamA.ID, Name: "copy.txt"})
	same(t, "Copy from another org", cross, miss)
	_, cross = d.CopyFile(ctx, "a1", o.fileA.ID, drive.Location{Workspace: o.teamB.ID, Name: "copy.txt"})
	_, miss = d.CopyFile(ctx, "a1", o.fileA.ID, drive.Location{Workspace: missing, Name: "copy.txt"})
	same(t, "Copy into another org", cross, miss)
	_, cross = d.Handover(drive.WithOrg(ctx, "org-a"), "staff", o.leaverA, drive.Target{Workspace: o.teamB.ID})
	_, miss = d.Handover(drive.WithOrg(ctx, "org-a"), "staff", o.leaverA, drive.Target{Workspace: missing})
	same(t, "Handover into another org's workspace", cross, miss)
	_, cross = d.Handover(drive.WithOrg(ctx, "org-a"), "staff", o.leaverA, drive.Target{User: "b1"})
	_, miss = d.Handover(drive.WithOrg(ctx, "org-a"), "staff", o.leaverA, drive.Target{User: missing})
	same(t, "Handover to another org's member", cross, miss)
	_, _, cross = d.CreateLink(ctx, "a1", "file", o.fileB.ID, "reader", 0)
	_, _, miss = d.CreateLink(ctx, "a1", "file", missing, "reader", 0)
	same(t, "Link another org's file", cross, miss)
	_, cross = d.OpenLink(ctx, "a1", o.linkB)
	_, miss = d.OpenLink(ctx, "a1", "no-such-token")
	same(t, "Open another org's organisation link", cross, miss)
	var opened int
	if err := o.db.QueryRow(`SELECT COUNT(*) FROM drive_link_users WHERE user_id='a1'`).Scan(&opened); err != nil || opened != 0 {
		t.Errorf("opening another org's link recorded %d openers: %v", opened, err)
	}
	if err := d.SetEditorsShare(ctx, "a1", o.teamB.ID, true); err == nil {
		t.Error("SetEditorsShare on another org's workspace succeeded")
	}

	spaces, err := d.Workspaces(ctx, "a1", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range spaces {
		if w.ID == o.teamB.ID || w.ID == o.personalB {
			t.Errorf("a1 lists org B's workspace %s", w.ID)
		}
	}
	people, err := d.People(ctx, "a1", "person")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range people {
		if p.ID[0] != 'a' {
			t.Errorf("people picker offers %s from another org", p.ID)
		}
	}
	if len(people) == 0 {
		t.Error("people picker found nobody in a1's own org")
	}
	hits, _, err := d.Search(ctx, "a1", "zebracrossing", 20)
	if err != nil || len(hits) != 0 {
		t.Errorf("search found another org's folder: %v %v", hits, err)
	}
	shared, err := d.SharedWithMe(ctx, "a1")
	if err != nil || len(shared) != 0 {
		t.Errorf("shared with me: %v %v", shared, err)
	}
}

// Two orgs may each have a shared workspace of the same name; one org may not have two.
func TestWorkspaceNamesArePerOrg(t *testing.T) {
	o := multiTenant(t)
	if err := drive.NameFree(o.ctx, o.d, "org-b", "Team (2)"); err != nil {
		t.Fatalf("a free name refused: %v", err)
	}
	if err := drive.NameFree(o.ctx, o.d, "org-c", "Team"); err != nil {
		t.Fatalf("Team taken in an org that has none: %v", err)
	}
	if err := drive.NameFree(o.ctx, o.d, "org-a", "team"); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("a second Team in one org: %v", err)
	}
}

// Every write path a member uses stamps its rows with the member's org, so none of them
// relies on the single-tenant default.
func TestWritesNameTheirOrg(t *testing.T) {
	o := multiTenant(t)
	ctx, d := o.ctx, o.d
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	_, _, err := d.CreateAppPassword(ctx, "a1", "laptop")
	must("app password", err)
	_, _, err = d.CreateAgentToken(ctx, "a1", "agent", "mcp:read", o.teamA.ID, "")
	must("agent token", err)
	_, err = d.CreateServiceToken(ctx, "a1", o.leaverA, "robot", "reader", "svc-a")
	if err == nil {
		t.Fatal("service token on another member's My files")
	}
	personal, err := d.EnsurePersonalWorkspace(ctx, "a1")
	must("personal", err)
	_, err = d.CreateServiceToken(ctx, "a1", personal, "robot", "reader", "svc-a")
	must("service token", err)
	_, err = d.ApprovePhone(ctx, "a1")
	must("phone code", err)
	_, err = d.EditorSession(ctx, "a1", o.fileA.ID)
	must("editor session", err)
	must("peer setting", d.SetPeerSetting(ctx, "a1", true))
	must("peer guest", d.SetPeerGuestSetting(ctx, "a1", true))
	_, _, err = d.IssueTicket(drive.WithPeer(ctx, livePeer), "a1", "mail.pdf", 3, 1<<20)
	must("peer ticket", err)
	must("picks", d.RecordPicks(ctx, "a1", []string{o.fileA.ID}))
	_, _, err = d.IssueViewTicket(drive.WithPeer(ctx, livePeer), "a1", "view.pdf", 3, 1<<20)
	must("view ticket", err)
	must("trash", d.SetTrash(ctx, "a1", o.fileA.ID, 1, true))

	for _, table := range drive.OrgOwnedTables {
		var wrong int
		var check string
		switch table {
		case "users", "groups", "drive_workspaces", "audit_records", "drive_events", "drive_orgs":
			continue
		case "drive_changes", "drive_change_floor", "drive_service_tokens", "drive_view_copies":
			check = `org_id IS DISTINCT FROM (SELECT org_id FROM drive_workspaces WHERE id=workspace)`
		case "drive_editor_documents":
			check = `org_id IS DISTINCT FROM (SELECT w.org_id FROM drive_files f JOIN drive_workspaces w ON w.id=f.workspace WHERE f.id=file_id)`
		case "drive_editor_saves":
			check = `org_id IS DISTINCT FROM (SELECT org_id FROM drive_editor_documents d WHERE d.key=drive_editor_saves.key)`
		case "drive_peer_tickets", "drive_peer_folders", "drive_peer_picks":
			check = `org_id IS DISTINCT FROM (SELECT org_id FROM users WHERE id=owner)`
		default:
			check = `org_id IS DISTINCT FROM (SELECT org_id FROM users WHERE id=user_id)`
		}
		if err := o.db.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE ` + check).Scan(&wrong); err != nil || wrong != 0 {
			t.Errorf("%s: %d rows outside their owner's org: %v", table, wrong, err)
		}
	}
	var lost int
	if err := o.db.QueryRow(`SELECT COUNT(*) FROM drive_events e WHERE e.org_id IS DISTINCT FROM (SELECT org_id FROM users WHERE id=e.user_id)`).Scan(&lost); err != nil || lost != 0 {
		t.Errorf("%d events outside their actor's org: %v", lost, err)
	}
}

// A member of an org that is suspended, scheduled for deletion or disabled holds nothing and
// no credential works; the member's own org comes back whole on resume.
func TestInactiveOrgStopsItsMembers(t *testing.T) {
	o := multiTenant(t)
	ctx, d := o.ctx, o.d
	_, secret, err := d.CreateAppPassword(ctx, "a1", "laptop")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{`status='suspended'`, `status='deletion_scheduled'`, `enabled=0`} {
		if _, err := o.db.Exec(`UPDATE drive_orgs SET ` + change + ` WHERE id='org-a'`); err != nil {
			t.Fatal(err)
		}
		if ok, err := d.ActivePrincipal(ctx, "a1"); err != nil || ok {
			t.Errorf("%s: a1 still an active principal (%v)", change, err)
		}
		if err := d.Authorize(ctx, "a1", o.teamA.ID, 1); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%s: a1 reads their team: %v", change, err)
		}
		if _, err := d.AppPasswordUser(ctx, "a1", secret); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("%s: app password still works: %v", change, err)
		}
		if _, err := d.People(ctx, "b1", "person a"); err != nil {
			t.Errorf("%s: org B's people picker: %v", change, err)
		}
		if err := d.Authorize(ctx, "b1", o.teamB.ID, 2); err != nil {
			t.Errorf("%s: org B stopped too: %v", change, err)
		}
		if _, err := o.db.Exec(`UPDATE drive_orgs SET status='active',enabled=1 WHERE id='org-a'`); err != nil {
			t.Fatal(err)
		}
		if err := d.Authorize(ctx, "a1", o.teamA.ID, 2); err != nil {
			t.Errorf("%s then resume: %v", change, err)
		}
	}
}

// With the same-org triggers gone and cross-org rows planted directly, the store still gives
// a member nothing in another org: rank, item loads and moves check the org themselves.
func TestScopingHoldsWithoutTriggers(t *testing.T) {
	o := multiTenant(t)
	ctx, d := o.ctx, o.d
	oneOrg := triggers(t, o.db, "", "%one_org%")
	if len(oneOrg) == 0 {
		t.Fatal("no same-org triggers to drop")
	}
	for _, name := range oneOrg {
		dropTrigger(t, o.db, name)
	}
	var link string
	if err := o.db.QueryRow(`SELECT id FROM drive_links WHERE scope='org' AND workspace=?`, o.teamB.ID).Scan(&link); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO group_members(group_id,user_id) VALUES('` + o.groupB + `','a1')`,
		`INSERT INTO drive_shares(id,workspace,resource_kind,resource_id,principal_kind,principal_id,role,created_by,created) VALUES('planted','` + o.teamB.ID + `','file','` + o.fileB.ID + `','user','a1','manager','b1','')`,
		`INSERT INTO drive_link_users(link,user_id,opened) VALUES('` + link + `','a1','')`,
	} {
		if _, err := o.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	missing := uuid.NewString()
	same(t, "Authorize with a planted membership", d.Authorize(ctx, "a1", o.teamB.ID, 1), d.Authorize(ctx, "a1", missing, 1))
	_, cross := d.File(ctx, "a1", o.fileB.ID, 1)
	_, miss := d.File(ctx, "a1", missing, 1)
	same(t, "File with a planted share", cross, miss)
	if err := d.AuthorizeItem(ctx, "a1", o.teamB.ID, "", o.fileB.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("item rank honours a planted share or link: %v", err)
	}
	shared, err := d.SharedWithMe(ctx, "a1")
	if err != nil || len(shared) != 0 {
		t.Errorf("shared with me lists planted grants: %v %v", shared, err)
	}
	same(t, "Grant another org's group", d.Grant(ctx, "a1", o.teamA.ID, o.groupB, "reader"), d.Grant(ctx, "a1", o.teamA.ID, missing, "reader"))
	_, cross = d.MoveFile(ctx, "a1", o.fileA.ID, 1, drive.Location{Workspace: o.teamB.ID, Name: "plan.txt"})
	_, miss = d.MoveFile(ctx, "a1", o.fileA.ID, 1, drive.Location{Workspace: missing, Name: "plan.txt"})
	same(t, "Move into another org", cross, miss)
	_, cross = d.CopyFile(ctx, "a1", o.fileB.ID, drive.Location{Workspace: o.teamA.ID, Name: "copy.txt"})
	_, miss = d.CopyFile(ctx, "a1", missing, drive.Location{Workspace: o.teamA.ID, Name: "copy.txt"})
	same(t, "Copy from another org", cross, miss)
	_, cross = d.Handover(drive.WithOrg(ctx, "org-a"), "staff", o.leaverA, drive.Target{Workspace: o.teamB.ID})
	_, miss = d.Handover(drive.WithOrg(ctx, "org-a"), "staff", o.leaverA, drive.Target{Workspace: missing})
	same(t, "Handover into another org", cross, miss)
	var moved int
	if err := o.db.QueryRow(`SELECT COUNT(*) FROM drive_files f JOIN drive_workspaces w ON w.id=f.workspace JOIN drive_versions v ON v.file_id=f.id WHERE w.org_id='org-b' AND v.blob IN (SELECT v2.blob FROM drive_versions v2 JOIN drive_files f2 ON f2.id=v2.file_id JOIN drive_workspaces w2 ON w2.id=f2.workspace WHERE w2.org_id='org-a')`).Scan(&moved); err != nil || moved != 0 {
		t.Errorf("%d of org A's blobs reached org B: %v", moved, err)
	}
}

// On a multi-tenant store only the staff org holds role='admin': the database refuses it
// elsewhere, and the store refuses an administrator planted outside it.
func TestOnlyStaffAdminister(t *testing.T) {
	o := multiTenant(t)
	ctx, d := o.ctx, o.d
	if _, err := o.db.Exec(`UPDATE users SET role='admin' WHERE id='a1'`); !refused(err) {
		t.Fatalf("a customer made administrator: %v", err)
	}
	if _, err := o.db.Exec(`INSERT INTO users(id,username,role,status,created_at,updated_at,org_id) VALUES('boss','boss','admin','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'org-a')`); !refused(err) {
		t.Fatalf("a customer administrator created: %v", err)
	}
	if _, err := o.db.Exec(`INSERT INTO users(id,username,role,status,created_at,updated_at,org_id) VALUES('staff2','staff2','admin','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?)`, drive.TestStaffOrg); err != nil {
		t.Fatalf("a second staff administrator: %v", err)
	}
	for _, name := range triggers(t, o.db, "users", "users_admin_is_staff%") {
		dropTrigger(t, o.db, name)
	}
	if _, err := o.db.Exec(`UPDATE users SET role='admin' WHERE id='a1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Leavers(ctx, "a1"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("a planted customer administrator lists leavers: %v", err)
	}
	if _, err := d.Leavers(ctx, "staff"); err != nil {
		t.Fatalf("staff: %v", err)
	}
}

// Staff act in the org they select, and another org's objects answer as unknown ones there.
func TestStaffActInTheSelectedOrg(t *testing.T) {
	o := multiTenant(t)
	ctx, d := o.ctx, o.d
	inA, inB := drive.WithOrg(ctx, "org-a"), drive.WithOrg(ctx, "org-b")
	if _, err := o.db.Exec(`UPDATE drive_workspaces SET deleted_at='2026-11-01T00:00:00Z',deleted_by='b1' WHERE id=?`, o.teamB.ID); err != nil {
		t.Fatal(err)
	}
	leavers, err := d.Leavers(inA, "staff")
	if err != nil || len(leavers) != 1 || leavers[0].Workspace != o.leaverA {
		t.Fatalf("A's leavers: %+v %v", leavers, err)
	}
	if leavers, err = d.Leavers(inB, "staff"); err != nil || len(leavers) != 0 {
		t.Fatalf("B's leavers: %+v %v", leavers, err)
	}
	deleted, err := d.DeletedWorkspaces(inA, "staff")
	if err != nil || len(deleted) != 0 {
		t.Fatalf("A lists B's deleted workspace: %+v %v", deleted, err)
	}
	if deleted, err = d.DeletedWorkspaces(inB, "staff"); err != nil || len(deleted) != 1 {
		t.Fatalf("B's deleted workspaces: %+v %v", deleted, err)
	}
	missing := uuid.NewString()
	same(t, "Restore another org's workspace", d.RestoreWorkspace(inA, "staff", o.teamB.ID), d.RestoreWorkspace(inA, "staff", missing))
	_, cross := d.Handover(inB, "staff", o.leaverA, drive.Target{Workspace: o.teamA.ID})
	_, miss := d.Handover(inB, "staff", missing, drive.Target{Workspace: o.teamA.ID})
	same(t, "Handover another org's leaver", cross, miss)
	colleagues, err := d.Colleagues(inA, "staff")
	if err != nil || len(colleagues) == 0 {
		t.Fatalf("A's colleagues: %v %v", colleagues, err)
	}
	for _, c := range colleagues {
		if c.ID[0] != 'a' {
			t.Errorf("A's colleagues include %s", c.ID)
		}
	}
	events, err := d.Events(inB, "staff")
	if err != nil || len(events) == 0 {
		t.Fatalf("B's audit: %v %v", events, err)
	}
	for _, e := range events {
		if e.User[0] != 'b' {
			t.Errorf("B's audit holds %s's %s", e.User, e.Action)
		}
	}
	if err := d.RestoreWorkspace(inB, "staff", o.teamB.ID); err != nil {
		t.Fatalf("restore in its own org: %v", err)
	}
	if _, err := d.Handover(inA, "staff", o.leaverA, drive.Target{Workspace: o.teamA.ID}); err != nil {
		t.Fatalf("handover in its own org: %v", err)
	}
	logged := strs(t, o.db, `SELECT action||' '||org_id FROM drive_events WHERE user_id='staff' ORDER BY created`)
	if want := []string{"workspace.restored org-b", "workspace.handed_over org-a"}; !slices.Equal(logged, want) {
		t.Errorf("staff actions logged as %v, want %v", logged, want)
	}
}

// What customers have no administrator for is refused on a multi-tenant store (D10).
func TestSharedWorkspaceAdministrationIsSingleTenant(t *testing.T) {
	o := multiTenant(t)
	ctx, d := drive.WithOrg(o.ctx, "org-a"), o.d
	_, createErr := d.CreateWorkspace(ctx, "staff", "New", 1<<20)
	_, deleteErr := d.DeleteWorkspace(ctx, "staff", o.teamA.ID, "Team")
	for what, err := range map[string]error{
		"create":    createErr,
		"delete":    deleteErr,
		"rename":    d.RenameWorkspace(ctx, "staff", o.teamA.ID, "Renamed"),
		"quota":     d.Quota(ctx, "staff", o.teamA.ID, 1<<30),
		"retention": d.SetRetention(ctx, "staff", o.teamA.ID, 7, 3),
	} {
		if !errors.Is(err, drive.ErrNotFound) {
			t.Errorf("%s: %v", what, err)
		}
	}
}

// A multi-tenant store records its staff org and seeds its row; it refuses a staff org that
// is not a canonical UUID or is the default org, and a database recorded with another.
func TestStaffOrgIsRecorded(t *testing.T) {
	o := multiTenant(t)
	var status string
	var seeded int
	if err := o.db.QueryRow(`SELECT status,seeded FROM drive_orgs WHERE id=?`, drive.TestStaffOrg).Scan(&status, &seeded); err != nil || status != "active" || seeded != 1 {
		t.Fatalf("staff org row: %s %d %v", status, seeded, err)
	}
	for _, staff := range []string{"", "not-a-uuid", strings.ToUpper(drive.TestStaffOrg), drive.DefaultOrg, uuid.NewString()} {
		if _, err := drive.Open(o.ctx, o.db, testdb.Key, drive.Tenancy{Mode: drive.TenancyMulti, StaffOrg: staff}); err == nil {
			t.Errorf("opened with staff org %q", staff)
		}
	}
	if _, err := drive.Open(o.ctx, o.db, testdb.Key, drive.Tenancy{Mode: drive.TenancyMulti, StaffOrg: drive.TestStaffOrg}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
}

// A single-tenant install's audit still lists what actors without an account record (the
// system, a guest link): they land in its one org.
func TestSingleTenantAuditKeepsSystemEvents(t *testing.T) {
	st, _, _ := fixture(t)
	ctx, d := context.Background(), st.Drive()
	for _, actor := range []string{"system", "guest-link:x", "removed-account"} {
		if err := drive.RecordEvent(ctx, d, actor, "test.recorded", actor); err != nil {
			t.Fatal(err)
		}
	}
	events, err := d.Events(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.User] = true
	}
	for _, actor := range []string{"system", "guest-link:x", "removed-account"} {
		if !seen[actor] {
			t.Errorf("audit lost the event of %s", actor)
		}
	}
}

// On a multi-tenant store, a guest download and a retention purge are audited in the org of
// the workspace they touched, not as platform rows.
func TestWorkerAndGuestEventsLandInTheirOrg(t *testing.T) {
	o := multiTenant(t)
	ctx, d := o.ctx, o.d
	if _, err := d.GuestFile(ctx, o.guestB, "", o.fileB.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTrash(ctx, "a1", o.fileA.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplyRetention(ctx, time.Now().Add(40*24*time.Hour), 30); err != nil {
		t.Fatal(err)
	}
	got := strs(t, o.db, `SELECT action||' '||COALESCE(org_id,'none') FROM drive_events WHERE action IN ('guest_link.downloaded','file.retention_purged') ORDER BY action`)
	if want := []string{"file.retention_purged org-a", "guest_link.downloaded org-b"}; !slices.Equal(got, want) {
		t.Fatalf("events %v, want %v", got, want)
	}
}

// Revoking another org's service token fails exactly as revoking one that never existed, and
// leaves it working.
func TestForeignServiceTokenIsMissing(t *testing.T) {
	o := multiTenant(t)
	ctx, d := o.ctx, o.d
	id, err := d.CreateServiceToken(ctx, "b1", o.teamB.ID, "robot", "reader", "svc-b")
	if err != nil {
		t.Fatal(err)
	}
	same(t, "RevokeServiceToken", d.RevokeServiceToken(ctx, "a1", id), d.RevokeServiceToken(ctx, "a1", uuid.NewString()))
	if _, _, err := d.ServiceToken(ctx, "svc-b"); err != nil {
		t.Fatal("org B's token stopped working:", err)
	}
	if err := d.RevokeServiceToken(ctx, "b1", id); err != nil {
		t.Fatal("its own org's manager:", err)
	}
}
