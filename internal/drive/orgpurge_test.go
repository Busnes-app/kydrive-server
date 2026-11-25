package drive_test

import (
	"database/sql"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// orgLeft is every row org-b still holds: each org-owned table by org_id, and each derived
// table through its parent.
func (o *orgs) orgLeft(t *testing.T, org string) map[string]int {
	t.Helper()
	left := map[string]int{}
	for _, table := range drive.OrgOwnedTables {
		if n := o.count(t, `SELECT COUNT(*) FROM `+table+` WHERE org_id=?`, org); n > 0 {
			left[table] = n
		}
	}
	ws := `(SELECT id FROM drive_workspaces WHERE org_id='` + org + `')`
	files := `(SELECT f.id FROM drive_files f WHERE f.workspace IN ` + ws + `)`
	for table, q := range map[string]string{
		"group_members":             `SELECT COUNT(*) FROM group_members WHERE user_id IN ('b1','b2') OR group_id='group-b'`,
		"sessions":                  `SELECT COUNT(*) FROM sessions WHERE user_id IN ('b1','b2')`,
		"drive_personal_workspaces": `SELECT COUNT(*) FROM drive_personal_workspaces WHERE owner IN ('b1','b2')`,
		"drive_files":               `SELECT COUNT(*) FROM drive_files WHERE workspace NOT IN (SELECT id FROM drive_workspaces)`,
		"drive_folders":             `SELECT COUNT(*) FROM drive_folders WHERE workspace NOT IN (SELECT id FROM drive_workspaces)`,
		"drive_versions":            `SELECT COUNT(*) FROM drive_versions WHERE file_id NOT IN (SELECT id FROM drive_files) OR file_id IN ` + files,
		"drive_shares":              `SELECT COUNT(*) FROM drive_shares WHERE workspace NOT IN (SELECT id FROM drive_workspaces)`,
		"drive_links":               `SELECT COUNT(*) FROM drive_links WHERE workspace NOT IN (SELECT id FROM drive_workspaces)`,
		"drive_grants":              `SELECT COUNT(*) FROM drive_grants WHERE workspace NOT IN (SELECT id FROM drive_workspaces)`,
		"drive_link_users":          `SELECT COUNT(*) FROM drive_link_users WHERE link NOT IN (SELECT id FROM drive_links)`,
		"drive_editor_sessions":     `SELECT COUNT(*) FROM drive_editor_sessions WHERE file_id NOT IN (SELECT id FROM drive_files)`,
		"drive_upload_sessions":     `SELECT COUNT(*) FROM drive_upload_sessions WHERE workspace NOT IN (SELECT id FROM drive_workspaces)`,
		"drive_suite_groups":        `SELECT COUNT(*) FROM drive_suite_groups WHERE workspace NOT IN (SELECT id FROM drive_workspaces)`,
	} {
		if n := o.count(t, q); n > 0 {
			left[table] = n
		}
	}
	return left
}

// A deleted org's purge removes every row it held and returns its blobs; the tombstone and a
// platform event stay, the other org is untouched, and a rerun changes nothing.
func TestOrgPurgeLeavesNothing(t *testing.T) {
	o := poolOrgs(t)
	o.exec(t, `INSERT INTO sessions(token_hash,user_id,expires_at,created_at) VALUES('sess-b','b1',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	blobB := o.str(t, `SELECT blob FROM drive_versions WHERE file_id=?`, o.fileB.ID)
	o.exec(t, `UPDATE drive_orgs SET status='deleted' WHERE id='org-b'`)
	blobs, _, err := o.d.PurgeOrgs(o.ctx)
	noErr(t, "purge", err)
	if !slices.Contains(blobs, blobB) {
		t.Fatalf("blobs %v miss %s", blobs, blobB)
	}
	if left := o.orgLeft(t, "org-b"); len(left) > 0 {
		t.Fatalf("org-b left %v", left)
	}
	if o.count(t, `SELECT COUNT(*) FROM drive_orgs WHERE id='org-b' AND status='deleted' AND purged=1`) != 1 ||
		o.count(t, `SELECT COUNT(*) FROM drive_events WHERE action='platform.org_purged' AND org_id IS NULL`) != 1 {
		t.Fatal("no tombstone or platform event")
	}
	if _, err := o.d.File(o.ctx, "a1", o.fileA.ID, 0); err != nil {
		t.Fatalf("org-a lost a file: %v", err)
	}
	blobs, views, err := o.d.PurgeOrgs(o.ctx)
	if err != nil || len(blobs)+len(views) > 0 || o.count(t, `SELECT COUNT(*) FROM drive_events WHERE action='platform.org_purged'`) != 1 {
		t.Fatalf("a rerun purged again: %v %v %v", blobs, views, err)
	}
}

// A purge cut short (an editor drop still in flight) sets no purged flag, and the next pass
// finishes it.
func TestOrgPurgeWaitsForEditorDrops(t *testing.T) {
	o := poolOrgs(t)
	o.exec(t, `INSERT INTO drive_editor_sessions(id,file_id,revision,user_id,token_hash,expires,revoked,drop_done) VALUES('ed-b',?,1,'b1','h',0,true,false)`, o.fileB.ID)
	o.exec(t, `UPDATE drive_orgs SET status='deleted' WHERE id='org-b'`)
	_, _, err := o.d.PurgeOrgs(o.ctx)
	noErr(t, "first pass", err)
	if o.count(t, `SELECT purged FROM drive_orgs WHERE id='org-b'`) != 0 || o.count(t, `SELECT COUNT(*) FROM drive_workspaces WHERE id=?`, o.teamB.ID) != 1 {
		t.Fatal("purged before the drop")
	}
	o.exec(t, `UPDATE drive_editor_sessions SET drop_done=true WHERE id='ed-b'`)
	_, _, err = o.d.PurgeOrgs(o.ctx)
	noErr(t, "second pass", err)
	if left := o.orgLeft(t, "org-b"); len(left) > 0 || o.count(t, `SELECT purged FROM drive_orgs WHERE id='org-b'`) != 1 {
		t.Fatalf("org-b left %v", left)
	}
}

// A writer racing the purge (another replica, on PostgreSQL) leaves nothing behind: writes
// before the deletion are purged, writes after it are refused.
func TestOrgPurgeRacesAWriter(t *testing.T) {
	o := poolOrgs(t)
	d2 := o.d
	if testdb.Postgres() {
		var err error
		d2, err = drive.OpenMulti(o.ctx, testdb.Open(t, o.cfg), testdb.Key)
		noErr(t, "second replica", err)
	}
	var stop atomic.Bool
	var wrote atomic.Int64
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; !stop.Load(); i++ {
			if _, err := d2.Publish(o.ctx, "b1", drive.File{Workspace: o.teamB.ID, Name: "race-" + time.Now().Format("150405.000000000") + ".txt"}, blob(t, o.root, "racing"), 0); err == nil {
				wrote.Add(1)
			}
		}
	})
	for wrote.Load() < 3 {
		time.Sleep(time.Millisecond)
	}
	o.exec(t, `UPDATE drive_orgs SET status='deleted' WHERE id='org-b'`)
	_, _, err := o.d.PurgeOrgs(o.ctx)
	stop.Store(true)
	wg.Wait()
	noErr(t, "purge", err)
	_, _, err = o.d.PurgeOrgs(o.ctx)
	noErr(t, "second purge", err)
	if left := o.orgLeft(t, "org-b"); len(left) > 0 || o.count(t, `SELECT purged FROM drive_orgs WHERE id='org-b'`) != 1 {
		t.Fatalf("org-b left %v after %d racing writes", left, wrote.Load())
	}
}

// eraseFixture makes a1 a KyIdentity member with a My files holding a folder shared with
// group-a as editor and with the older a0 as reader (a file inside), a file shared only with an inactive colleague and a private file,
// and records their erasure.
func eraseFixture(t *testing.T) (o *orgs, shared drive.Folder, inside, private, toGone drive.File) {
	o = poolOrgs(t)
	o.exec(t, `UPDATE users SET sso_provider='kysignon',sso_subject='sub-a1' WHERE id='a1'`)
	mine, err := o.d.EnsurePersonalWorkspace(o.ctx, "a1")
	noErr(t, "My files", err)
	shared, err = o.d.AddFolder(o.ctx, "a1", mine, "", "Project")
	noErr(t, "folder", err)
	inside, err = o.d.Publish(o.ctx, "a1", drive.File{Workspace: mine, Parent: shared.ID, Name: "plan.txt"}, blob(t, o.root, "plan"), 0)
	noErr(t, "inside", err)
	private, err = o.d.Publish(o.ctx, "a1", drive.File{Workspace: mine, Name: "diary.txt"}, blob(t, o.root, "diary"), 0)
	noErr(t, "private", err)
	toGone, err = o.d.Publish(o.ctx, "a1", drive.File{Workspace: mine, Name: "old.txt"}, blob(t, o.root, "old"), 0)
	noErr(t, "to gone", err)
	_, err = o.d.Share(o.ctx, "a1", "folder", shared.ID, "group", o.groupA, "editor")
	noErr(t, "group share", err)
	_, err = o.d.Share(o.ctx, "a1", "file", inside.ID, "user", "a2", "reader")
	noErr(t, "nested share", err)
	o.exec(t, `INSERT INTO drive_shares(id,workspace,resource_kind,resource_id,principal_kind,principal_id,role,created_by,created) VALUES('to-gone',?,'file',?,'user','a-gone','manager','a1','now')`, mine, toGone.ID)
	// a0, the longest-standing member, may only read the folder: a2, its editor, keeps it.
	o.exec(t, `INSERT INTO users(id,username,email,display_name,role,status,sso_provider,created_at,updated_at,org_id) VALUES('a0','a0','a0@local.test','Person a0','user','active','scim','2000-01-01 00:00:00',CURRENT_TIMESTAMP,'org-a')`)
	_, err = o.d.Share(o.ctx, "a1", "folder", shared.ID, "user", "a0", "reader")
	noErr(t, "reader share", err)
	_, _, err = o.d.CreateGuestLink(o.ctx, "a1", "file", inside.ID, time.Now().Add(time.Hour).Unix(), "", false, 24*time.Hour)
	noErr(t, "guest link", err)
	o.exec(t, `INSERT INTO drive_erasures(subject,org_id,revision) VALUES('sub-a1','org-a',1)`)
	return o, shared, inside, private, toGone
}

// Delete everything: a topmost shared item goes, with its subtree and shares, to its
// strongest-role active member; the rest goes, and so does the account. Not while the org is
// not active.
func TestEraseHandsSharedItemsOn(t *testing.T) {
	o, shared, inside, private, toGone := eraseFixture(t)
	privateBlob := o.str(t, `SELECT blob FROM drive_versions WHERE file_id=?`, private.ID)
	o.exec(t, `UPDATE drive_orgs SET status='suspended' WHERE id='org-a'`)
	_, _, err := o.d.EraseMembers(o.ctx)
	noErr(t, "suspended", err)
	if o.count(t, `SELECT COUNT(*) FROM users WHERE id='a1'`) != 1 {
		t.Fatal("erased while the org was suspended")
	}
	o.exec(t, `UPDATE drive_orgs SET status='active' WHERE id='org-a'`)
	blobs, _, err := o.d.EraseMembers(o.ctx)
	noErr(t, "erase", err)
	if !slices.Contains(blobs, privateBlob) {
		t.Fatalf("blobs %v miss the private file's", blobs)
	}
	heirs, err := o.d.EnsurePersonalWorkspace(o.ctx, "a2")
	noErr(t, "a2 My files", err)
	if o.count(t, `SELECT COUNT(*) FROM drive_folders f JOIN drive_folders h ON h.id=f.parent WHERE f.id=? AND f.workspace=? AND h.parent=''`, shared.ID, heirs) != 1 ||
		o.count(t, `SELECT COUNT(*) FROM drive_files WHERE id=? AND parent=? AND workspace=?`, inside.ID, shared.ID, heirs) != 1 {
		t.Fatal("the shared folder did not move under a handover folder in a2's My files")
	}
	if o.count(t, `SELECT COUNT(*) FROM drive_changes WHERE workspace=? AND item IN (?,?)`, heirs, shared.ID, inside.ID) != 2 {
		t.Fatal("the successor's change feed misses the moved items")
	}
	if o.count(t, `SELECT COUNT(*) FROM drive_shares WHERE workspace=? AND created_by='a2' AND resource_id IN (?,?)`, heirs, shared.ID, inside.ID) != 3 {
		t.Fatal("shares did not move with the items")
	}
	if o.count(t, `SELECT COUNT(*) FROM drive_links WHERE resource_id=? AND scope='guest' AND revoked=0`, inside.ID) != 0 {
		t.Fatal("a guest link survived the handover")
	}
	if _, err := o.d.File(o.ctx, "a2", inside.ID, 0); err != nil {
		t.Fatalf("a2 cannot read the handed-on file: %v", err)
	}
	for _, f := range []string{private.ID, toGone.ID} {
		if o.count(t, `SELECT COUNT(*) FROM drive_files WHERE id=?`, f) != 0 {
			t.Fatal("an unshared file, or one shared only with an inactive member, survived")
		}
	}
	if o.count(t, `SELECT COUNT(*) FROM users WHERE id='a1'`)+o.count(t, `SELECT COUNT(*) FROM drive_personal_workspaces WHERE owner='a1'`)+
		o.count(t, `SELECT COUNT(*) FROM group_members WHERE user_id='a1'`) != 0 {
		t.Fatal("the account, its My files or its membership survived")
	}
	if o.count(t, `SELECT COUNT(*) FROM drive_events WHERE action='member.erased' AND org_id='org-a'`) != 1 {
		t.Fatal("no erasure event in the org's log")
	}
	if _, err := o.d.File(o.ctx, "a2", o.fileA.ID, 0); err != nil {
		t.Fatalf("a shared workspace's file went: %v", err)
	}
	_, _, err = o.d.EraseMembers(o.ctx)
	noErr(t, "rerun", err)
	if o.count(t, `SELECT COUNT(*) FROM drive_events WHERE action='member.erased'`) != 1 {
		t.Fatal("a rerun erased again")
	}
}

// With no other active member to keep it, a shared item is deleted with the rest.
func TestEraseWithNoSuccessorDeletesAll(t *testing.T) {
	o, shared, inside, _, _ := eraseFixture(t)
	o.exec(t, `UPDATE users SET status='inactive' WHERE id IN ('a0','a2')`)
	_, _, err := o.d.EraseMembers(o.ctx)
	noErr(t, "erase", err)
	if o.count(t, `SELECT COUNT(*) FROM drive_folders WHERE id=?`, shared.ID)+o.count(t, `SELECT COUNT(*) FROM drive_files WHERE id=?`, inside.ID) != 0 {
		t.Fatal("a shared item nobody active can keep survived")
	}
}

func (o *orgs) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := o.db.Exec(q, args...); err != nil {
		t.Fatal(q, err)
	}
}

func (o *orgs) str(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := o.db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatal(q, err)
	}
	return s.String
}

// A purge whose last transaction rolls back returns no viewing-copy blobs: the rows that name
// them are still there.
func TestOrgPurgeKeepsBlobsOfARolledBackStep(t *testing.T) {
	o := poolOrgs(t)
	o.exec(t, `INSERT INTO drive_view_copies(id,user_id,workspace,name,size,pairing,ticket_expires,expires,blob,org_id) VALUES('vc-b','b1',?,'n',1,'p',0,0,'view-blob','org-b')`, o.personalB)
	if testdb.Postgres() {
		o.exec(t, `CREATE FUNCTION fail_purge() RETURNS trigger LANGUAGE plpgsql AS $t$BEGIN RAISE EXCEPTION 'boom'; END$t$`)
		o.exec(t, `CREATE TRIGGER fail_purge BEFORE UPDATE OF purged ON drive_orgs FOR EACH ROW EXECUTE FUNCTION fail_purge()`)
	} else {
		o.exec(t, `CREATE TRIGGER fail_purge BEFORE UPDATE OF purged ON drive_orgs BEGIN SELECT RAISE(ABORT,'boom'); END`)
	}
	o.exec(t, `UPDATE drive_orgs SET status='deleted' WHERE id='org-b'`)
	_, views, err := o.d.PurgeOrgs(o.ctx)
	if err == nil || len(views) != 0 || o.count(t, `SELECT COUNT(*) FROM drive_view_copies WHERE id='vc-b'`) != 1 {
		t.Fatalf("a rolled-back purge returned %v (%v)", views, err)
	}
}
