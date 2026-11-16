package drive_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// suiteFixture adds directory accounts: alice (KySignOn) and bob (SCIM) are ordinary users,
// boss is a directory application administrator, gone is inactive.
func suiteFixture(t *testing.T) (store.Store, *drive.Store, string) {
	t.Helper()
	st, _, root := fixture(t)
	ctx := context.Background()
	for _, u := range []*store.User{
		{ID: "alice", Username: "alice", Email: "alice@local.test", Role: "user", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-alice"},
		{ID: "bob", Username: "bob", Email: "bob@local.test", Role: "user", Status: "active", SSOProvider: "scim", SSOSubject: "sub-bob"},
		{ID: "boss", Username: "boss", Email: "boss@local.test", Role: "admin", Status: "active", SSOProvider: "kysignon", SSOSubject: "sub-boss"},
		{ID: "gone", Username: "gone", Email: "gone@local.test", Role: "user", Status: "inactive", SSOProvider: "kysignon", SSOSubject: "sub-gone"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	return st, st.Drive(), root
}

func active(id, name string, members ...string) suitedir.Group {
	return suitedir.Group{ID: id, Name: name, Slug: "s", State: suitedir.GroupActive, Members: append([]string{}, members...)}
}

func archived(id, name string) suitedir.Group {
	return suitedir.Group{ID: id, Name: name, Slug: "s", State: suitedir.GroupArchived, Members: []string{}}
}

func reconcile(t *testing.T, d *drive.Store, groups ...suitedir.Group) drive.SuiteReport {
	t.Helper()
	r, err := d.ReconcileSuiteGroups(context.Background(), groups)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func suiteWorkspace(t *testing.T, d *drive.Store, kyid string) (workspace, name, deletedAt string) {
	t.Helper()
	ctx := context.Background()
	for _, q := range []struct {
		sql string
		out *string
	}{
		{`SELECT workspace FROM drive_suite_groups WHERE kyid=?`, &workspace},
		{`SELECT w.name FROM drive_workspaces w JOIN drive_suite_groups s ON s.workspace=w.id WHERE s.kyid=?`, &name},
		{`SELECT w.deleted_at FROM drive_workspaces w JOIN drive_suite_groups s ON s.workspace=w.id WHERE s.kyid=?`, &deletedAt},
	} {
		if err := drive.QueryString(ctx, d, q.sql, q.out, kyid); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func suiteCount(t *testing.T, d *drive.Store, q string, args ...any) int {
	t.Helper()
	n, err := drive.QueryInt(context.Background(), d, q, args...)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func members(t *testing.T, d *drive.Store, kyid string) int {
	return suiteCount(t, d, `SELECT COUNT(*) FROM group_members m JOIN drive_suite_groups s ON s.grp=m.group_id WHERE s.kyid=?`, kyid)
}

func TestSuiteGroupLifecycle(t *testing.T) {
	_, d, root := suiteFixture(t)
	ctx := context.Background()

	// Create: one workspace, one local group granted editor, members resolved by subject;
	// the application administrator, the inactive account and an unknown subject are skipped.
	r := reconcile(t, d, active("g1", "Sales", "sub-alice", "sub-boss", "sub-gone", "sub-unknown"))
	if r.Created != 1 || r.MembersChanged != 1 {
		t.Fatalf("create report %+v", r)
	}
	ws, name, _ := suiteWorkspace(t, d, "g1")
	if name != "Sales" || members(t, d, "g1") != 1 {
		t.Fatalf("created %q with %d members", name, members(t, d, "g1"))
	}
	if n := suiteCount(t, d, `SELECT COUNT(*) FROM drive_grants g JOIN drive_suite_groups s ON s.grp=g.group_id AND s.workspace=g.workspace WHERE s.kyid='g1' AND g.role='editor'`); n != 1 {
		t.Fatalf("editor grants %d", n)
	}
	if err := d.Authorize(ctx, "alice", ws, 2); err != nil {
		t.Fatalf("alice is an editor: %v", err)
	}
	for _, u := range []string{"boss", "bob"} {
		if err := d.Authorize(ctx, u, ws, 1); !errors.Is(err, drive.ErrDenied) {
			t.Fatalf("%s: %v", u, err)
		}
	}
	f, err := d.Publish(ctx, "alice", drive.File{Workspace: ws, Name: "plan.txt"}, blob(t, root, "plan"), 0)
	if err != nil {
		t.Fatal(err)
	}

	// Running twice changes nothing and writes no events.
	events := suiteCount(t, d, `SELECT COUNT(*) FROM drive_events`)
	if r := reconcile(t, d, active("g1", "Sales", "sub-alice", "sub-boss", "sub-gone", "sub-unknown")); r != (drive.SuiteReport{}) {
		t.Fatalf("second pass %+v", r)
	}
	if n := suiteCount(t, d, `SELECT COUNT(*) FROM drive_events`); n != events {
		t.Fatalf("second pass wrote %d events", n-events)
	}

	// Rename, add a member, remove a member.
	r = reconcile(t, d, active("g1", "Sales EU", "sub-bob"))
	if r.Renamed != 1 || r.MembersChanged != 1 {
		t.Fatalf("rename report %+v", r)
	}
	if _, name, _ = suiteWorkspace(t, d, "g1"); name != "Sales EU" {
		t.Fatalf("renamed to %q", name)
	}
	if err := d.Authorize(ctx, "alice", ws, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("removed member keeps access: %v", err)
	}
	if err := d.Authorize(ctx, "bob", ws, 2); err != nil {
		t.Fatalf("added member: %v", err)
	}

	// Archive: members gone, workspace deleted recoverably, items kept, access revoked.
	if r = reconcile(t, d, archived("g1", "Sales EU")); r.Archived != 1 {
		t.Fatalf("archive report %+v", r)
	}
	ws2, _, deletedAt := suiteWorkspace(t, d, "g1")
	if ws2 != ws || deletedAt == "" || members(t, d, "g1") != 0 {
		t.Fatalf("archived: workspace %s deleted %q members %d", ws2, deletedAt, members(t, d, "g1"))
	}
	if err := d.Authorize(ctx, "bob", ws, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("archived workspace still readable: %v", err)
	}
	if n := suiteCount(t, d, `SELECT COUNT(*) FROM drive_files WHERE id=?`, f.ID); n != 1 {
		t.Fatal("archive lost the file")
	}
	events = suiteCount(t, d, `SELECT COUNT(*) FROM drive_events`)
	if r := reconcile(t, d, archived("g1", "Sales EU")); r != (drive.SuiteReport{}) || suiteCount(t, d, `SELECT COUNT(*) FROM drive_events`) != events {
		t.Fatalf("second archive pass %+v", r)
	}

	// Purge never removes it, however old.
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_workspaces SET deleted_at=? WHERE id=?`, time.Now().AddDate(-5, 0, 0).UTC().Format(time.RFC3339Nano), ws); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PurgeDeletedWorkspaces(ctx, time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	if n := suiteCount(t, d, `SELECT COUNT(*) FROM drive_files WHERE workspace=?`, ws); n != 1 {
		t.Fatal("purge removed an archived suite workspace")
	}

	// Re-enable: the same workspace comes back with its file.
	if r = reconcile(t, d, active("g1", "Sales EU", "sub-bob")); r.Restored != 1 {
		t.Fatalf("restore report %+v", r)
	}
	if ws2, _, deletedAt = suiteWorkspace(t, d, "g1"); ws2 != ws || deletedAt != "" {
		t.Fatalf("restored %s deleted %q", ws2, deletedAt)
	}
	if _, err := d.File(ctx, "bob", f.ID, 1); err != nil {
		t.Fatalf("restored file: %v", err)
	}
}

// A recreated namesake has a new ID: a new workspace, numbered, never the archived one.
func TestSuiteGroupNamesakeGetsANewWorkspace(t *testing.T) {
	_, d, _ := suiteFixture(t)
	ctx := context.Background()
	reconcile(t, d, active("old", "Sales", "sub-alice"))
	reconcile(t, d, archived("old", "Sales"))
	reconcile(t, d, archived("old", "Sales"), active("new", "Sales", "sub-alice"))
	oldWS, _, _ := suiteWorkspace(t, d, "old")
	newWS, name, _ := suiteWorkspace(t, d, "new")
	if newWS == oldWS || name != "Sales (2)" {
		t.Fatalf("namesake: %s %q (old %s)", newWS, name, oldWS)
	}
	if err := d.Authorize(ctx, "alice", oldWS, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("namesake inherits the archived workspace: %v", err)
	}
	// A numbered name is left alone on the next pass.
	if r := reconcile(t, d, archived("old", "Sales"), active("new", "Sales", "sub-alice")); r != (drive.SuiteReport{}) {
		t.Fatalf("numbered name renamed again: %+v", r)
	}
	// An empty archived workspace is kept too, and an administrator's delete keeps it.
	reconcile(t, d, archived("new", "Sales"))
	if n := suiteCount(t, d, `SELECT COUNT(*) FROM drive_workspaces WHERE id=? AND deleted_at!=''`, newWS); n != 1 {
		t.Fatal("empty archived workspace removed")
	}
	reconcile(t, d, active("new", "Sales"))
	if _, err := d.DeleteWorkspace(ctx, "admin", newWS, ""); err != nil {
		t.Fatal(err)
	}
	if n := suiteCount(t, d, `SELECT COUNT(*) FROM drive_workspaces WHERE id=?`, newWS); n != 1 {
		t.Fatal("administrator delete removed an empty suite workspace")
	}
}

// Damage between passes (a lost grant, a changed role, an extra member) heals without
// duplicates; an absent group and an unknown state change nothing.
func TestSuiteGroupHeals(t *testing.T) {
	_, d, _ := suiteFixture(t)
	ctx := context.Background()
	reconcile(t, d, active("g1", "Ops", "sub-alice"))
	ws, _, _ := suiteWorkspace(t, d, "g1")
	if err := drive.ExecSQL(ctx, d, `DELETE FROM drive_grants WHERE workspace=?`, ws); err != nil {
		t.Fatal(err)
	}
	if err := drive.ExecSQL(ctx, d, `INSERT INTO group_members(group_id,user_id) SELECT grp,'bob' FROM drive_suite_groups WHERE kyid='g1'`); err != nil {
		t.Fatal(err)
	}
	reconcile(t, d, active("g1", "Ops", "sub-alice"))
	if suiteCount(t, d, `SELECT COUNT(*) FROM drive_grants WHERE workspace=? AND role='editor'`, ws) != 1 || members(t, d, "g1") != 1 {
		t.Fatal("not healed")
	}
	if err := d.Grant(ctx, "admin", ws, mustGroup(t, d, "g1"), "manager"); err != nil {
		t.Fatal(err)
	}
	reconcile(t, d, active("g1", "Ops", "sub-alice"))
	if suiteCount(t, d, `SELECT COUNT(*) FROM drive_grants WHERE workspace=? AND role='editor'`, ws) != 1 {
		t.Fatal("manager grant kept")
	}
	if suiteCount(t, d, `SELECT COUNT(*) FROM drive_suite_groups`) != 1 || suiteCount(t, d, `SELECT COUNT(*) FROM groups WHERE display_name LIKE 'suite-group:%'`) != 1 {
		t.Fatal("duplicates")
	}
	// Absence and an unknown state never archive.
	reconcile(t, d)
	reconcile(t, d, suitedir.Group{ID: "g1", Name: "Ops", Slug: "ops", State: "paused"})
	if _, _, deletedAt := suiteWorkspace(t, d, "g1"); deletedAt != "" || members(t, d, "g1") != 1 {
		t.Fatal("absence archived")
	}
	// A failing group does not stop the others.
	if err := drive.ExecSQL(ctx, d, `INSERT INTO groups(id,display_name,external_id,created_at,updated_at) VALUES('squat','suite-group:g2','',?,?)`, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	r, err := d.ReconcileSuiteGroups(ctx, []suitedir.Group{active("g2", "Taken"), active("g3", "Fine")})
	if err == nil || r.Created != 1 {
		t.Fatalf("partial pass: %+v %v", r, err)
	}
}

func mustGroup(t *testing.T, d *drive.Store, kyid string) string {
	t.Helper()
	var g string
	if err := drive.QueryString(context.Background(), d, `SELECT grp FROM drive_suite_groups WHERE kyid=?`, &g, kyid); err != nil {
		t.Fatal(err)
	}
	return g
}
