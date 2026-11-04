package drive_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// A workspace holding files is deleted only when the admin names it. Deleting takes away every
// access at once and revokes bearer credentials for good; restore brings back grants, shares
// and every file, version and trashed item.
func TestRecoverableDeleteWorkspace(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	folder, err := d.AddFolder(ctx, "worker", w.ID, "", "Plans")
	must(err)
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: folder.ID, Name: "plan.txt"}, blob(t, blobs, "plan one"), 0)
	must(err)
	f, err = d.Publish(ctx, "worker", drive.File{ID: f.ID, Workspace: w.ID, Parent: folder.ID, Name: "plan.txt"}, blob(t, blobs, "plan two"), f.Revision)
	must(err)
	old, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "old.txt"}, blob(t, blobs, "old"), 0)
	must(err)
	must(d.SetTrash(ctx, "worker", old.ID, old.Revision, true))
	_, err = d.Share(ctx, "worker", "file", f.ID, "user", "stranger", "reader")
	must(err)
	_, link, err := d.CreateLink(ctx, "worker", "folder", folder.ID, "reader", 0)
	must(err)
	_, guest, err := d.CreateGuestLink(ctx, "worker", "folder", folder.ID, inDays(1), "", false, week)
	must(err)
	_, err = d.CreateServiceToken(ctx, "worker", w.ID, "Robot", "reader", "svc-recoverable")
	must(err)
	_, scoped, err := d.CreateAgentToken(ctx, "worker", "Scoped agent", drive.ScopeMCPRead, w.ID, "")
	must(err)
	session, err := d.EditorSession(ctx, "worker", f.ID)
	must(err)

	// Only an administrator, and only with the exact name.
	if _, err := d.DeleteWorkspace(ctx, "worker", w.ID, "Organization"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("manager deleted the workspace: %v", err)
	}
	for _, confirm := range []string{"", "organization", "Organization "} {
		if _, err := d.DeleteWorkspace(ctx, "admin", w.ID, confirm); !errors.Is(err, drive.ErrConflict) {
			t.Fatalf("confirm %q: %v", confirm, err)
		}
	}
	recoverable, err := d.DeleteWorkspace(ctx, "admin", w.ID, "Organization")
	must(err)
	if !recoverable {
		t.Fatal("a workspace with files was not deleted recoverably")
	}

	// Every access path is gone.
	for _, user := range []string{"worker", "admin"} {
		list, err := d.Workspaces(ctx, user, user == "admin")
		must(err)
		for _, x := range list {
			if x.ID == w.ID {
				t.Fatalf("%s still lists the deleted workspace", user)
			}
		}
	}
	if _, err := d.File(ctx, "worker", f.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("member reads a file: %v", err)
	}
	if _, err := d.File(ctx, "stranger", f.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("share holder reads a file: %v", err)
	}
	if shared, err := d.SharedWithMe(ctx, "stranger"); err != nil || len(shared) != 0 {
		t.Fatalf("shared with me: %v %v", shared, err)
	}
	if _, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "late.txt"}, blob(t, blobs, "late"), 0); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("publish into a deleted workspace: %v", err)
	}
	if _, _, _, err := d.Changes(ctx, "worker", w.ID, 0, 10); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("changes: %v", err)
	}
	if hits, _, err := d.Search(ctx, "worker", "plan", 10); err != nil || len(hits) != 0 {
		t.Fatalf("search: %v %v", hits, err)
	}
	if _, err := d.OpenLink(ctx, "stranger", link); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("organisation link: %v", err)
	}
	if _, err := d.GuestView(ctx, guest, "", ""); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("guest link: %v", err)
	}
	if _, _, err := d.ServiceToken(ctx, "svc-recoverable"); err == nil {
		t.Fatal("service token survived")
	}
	if _, _, err := d.AgentTokenUser(ctx, scoped, time.Now()); err == nil {
		t.Fatal("scoped agent token survived")
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_editor_sessions WHERE id=? AND revoked=false`, session.ID); n != 0 {
		t.Fatal("editor session still open")
	}
	// Configuration is refused while deleted, and the name stays reserved.
	if err := d.RenameWorkspace(ctx, "admin", w.ID, "Other"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("rename deleted: %v", err)
	}
	if err := d.Grant(ctx, "admin", w.ID, "team", "reader"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("grant on deleted: %v", err)
	}
	if _, err := d.CreateWorkspace(ctx, "admin", "organization", 100); !errors.Is(err, drive.ErrConflict) || !strings.Contains(err.Error(), "Deleted workspaces") {
		t.Fatalf("name reuse while deleted: %v", err)
	}
	// Deleting again is a no-op.
	if again, err := d.DeleteWorkspace(ctx, "admin", w.ID, ""); err != nil || !again {
		t.Fatalf("second delete: %v %v", again, err)
	}

	// Administrators see it in Deleted workspaces: size and count, never file names.
	if _, err := d.DeletedWorkspaces(ctx, "worker"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("non-admin listed deleted workspaces: %v", err)
	}
	gone, err := d.DeletedWorkspaces(ctx, "admin")
	must(err)
	if len(gone) != 1 || gone[0].ID != w.ID || gone[0].Name != "Organization" || gone[0].Items != 3 || gone[0].Used == 0 || gone[0].DeletedBy != "admin" || gone[0].DeletedAt == "" {
		t.Fatalf("deleted workspaces: %+v", gone)
	}

	// Restore: admin only; members, shares and every item come back; credentials do not.
	if err := d.RestoreWorkspace(ctx, "worker", w.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("non-admin restored: %v", err)
	}
	must(d.RestoreWorkspace(ctx, "admin", w.ID))
	if err := d.RestoreWorkspace(ctx, "admin", w.ID); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("restoring a live workspace: %v", err)
	}
	got, err := d.File(ctx, "worker", f.ID, 1)
	must(err)
	if got.Revision != f.Revision {
		t.Fatalf("restored revision %d, want %d", got.Revision, f.Revision)
	}
	if vs, err := d.Versions(ctx, "worker", f.ID); err != nil || len(vs) != 2 {
		t.Fatalf("versions after restore: %v %v", vs, err)
	}
	if trashed, err := d.Files(ctx, "worker", w.ID, true); err != nil || len(trashed) != 1 || trashed[0].ID != old.ID {
		t.Fatalf("trash after restore: %v %v", trashed, err)
	}
	if _, err := d.File(ctx, "stranger", f.ID, 1); err != nil {
		t.Fatalf("share lost on restore: %v", err)
	}
	if _, err := d.OpenLink(ctx, "stranger", link); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("organisation link revived: %v", err)
	}
	if _, _, err := d.ServiceToken(ctx, "svc-recoverable"); err == nil {
		t.Fatal("service token revived")
	}
	if gone, err := d.DeletedWorkspaces(ctx, "admin"); err != nil || len(gone) != 0 {
		t.Fatalf("still listed as deleted: %v %v", gone, err)
	}

	del := events(t, d, "workspace.deleted")
	want := w.ID + ` name="Organization" recoverable=true files=2 folders=1 links=2 service_tokens=1 agent_tokens=1 editor_sessions=1`
	if len(del) != 1 || del[0] != want {
		t.Fatalf("delete events %q, want [%q]", del, want)
	}
	if got := events(t, d, "workspace.restored"); len(got) != 1 || got[0] != w.ID+` name="Organization"` {
		t.Fatalf("restore events %q", got)
	}
}

// After the trash age a deleted workspace is purged: rows go, blobs only when nothing else names
// them, and the name is free again. 0 days keeps it.
func TestPurgeDeletedWorkspaces(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	keep, err := d.CreateWorkspace(ctx, "admin", "Keep", 100)
	must(err)
	must(d.Grant(ctx, "admin", keep.ID, "team", "manager"))
	folder, err := d.AddFolder(ctx, "worker", w.ID, "", "Plans")
	must(err)
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: folder.ID, Name: "plan.txt"}, blob(t, blobs, "plan"), 0)
	must(err)
	solo, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "solo.txt"}, blob(t, blobs, "solo"), 0)
	must(err)
	// A copy in another workspace shares the plan blob.
	copied, err := d.CopyFile(ctx, "worker", f.ID, drive.Location{Workspace: keep.ID, Name: "plan.txt"})
	must(err)
	_, err = d.Share(ctx, "worker", "file", f.ID, "user", "stranger", "reader")
	must(err)
	vs, err := d.Versions(ctx, "worker", solo.ID)
	must(err)
	soloBlob := vs[0].Blob
	_, err = d.DeleteWorkspace(ctx, "admin", w.ID, "Organization")
	must(err)

	at := time.Now()
	for _, days := range []int{0, 90} {
		purged, err := d.PurgeDeletedWorkspaces(ctx, at, days)
		must(err)
		if len(purged) != 0 {
			t.Fatalf("purged early with %d days: %v", days, purged)
		}
	}
	must(drive.ExecSQL(ctx, d, `UPDATE drive_workspaces SET deleted_at=? WHERE id=?`, at.Add(-91*24*time.Hour).UTC().Format(time.RFC3339Nano), w.ID))
	purged, err := d.PurgeDeletedWorkspaces(ctx, at, 90)
	must(err)
	if len(purged) != 1 || purged[0] != soloBlob {
		t.Fatalf("unreferenced blobs %v, want [%s]", purged, soloBlob)
	}
	must(drive.RemoveBlobs(blobs, purged))
	if _, err := os.Stat(filepath.Join(blobs, soloBlob)); !os.IsNotExist(err) {
		t.Fatalf("solo blob kept: %v", err)
	}
	if _, err := d.File(ctx, "worker", copied.ID, 1); err != nil {
		t.Fatalf("copy in another workspace lost: %v", err)
	}
	for _, table := range []string{"drive_files", "drive_folders", "drive_grants", "drive_shares", "drive_links"} {
		if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM `+table+` WHERE workspace=?`, w.ID); err != nil || n != 0 {
			t.Errorf("%s keeps %d rows (%v)", table, n, err)
		}
	}
	if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_versions WHERE file_id IN (?,?)`, f.ID, solo.ID); err != nil || n != 0 {
		t.Errorf("versions kept: %d %v", n, err)
	}
	if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM pragma_foreign_key_check`); err != nil || n != 0 {
		t.Errorf("%d dangling references (%v)", n, err)
	}
	if gone, err := d.DeletedWorkspaces(ctx, "admin"); err != nil || len(gone) != 0 {
		t.Fatalf("still listed: %v %v", gone, err)
	}
	got := events(t, d, "workspace.purged")
	want := w.ID + ` name="Organization" versions=2 files=2 folders=1 links=0 shares=1 grants=1 service_tokens=0 agent_tokens=0`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("purge events %q, want [%q]", got, want)
	}
	if _, err := d.CreateWorkspace(ctx, "admin", "Organization", 100); err != nil {
		t.Fatalf("name not freed: %v", err)
	}
}

// A purge whose commit fails returns none of its blobs: the rolled-back rows still name them.
func TestFailedWorkspacePurgeKeepsBlobs(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "solo.txt"}, blob(t, blobs, "solo"), 0)
	must(err)
	_, err = d.DeleteWorkspace(ctx, "admin", w.ID, "Organization")
	must(err)
	at := time.Now()
	must(drive.ExecSQL(ctx, d, `UPDATE drive_workspaces SET deleted_at=? WHERE id=?`, at.Add(-91*24*time.Hour).UTC().Format(time.RFC3339Nano), w.ID))
	// A deferred foreign key fails only at COMMIT, after the purge statements all succeeded.
	must(drive.ExecSQL(ctx, d, `CREATE TABLE fail_commit(ref TEXT REFERENCES drive_workspaces(id) DEFERRABLE INITIALLY DEFERRED)`))
	must(drive.ExecSQL(ctx, d, `CREATE TRIGGER fail_purge AFTER DELETE ON drive_workspaces BEGIN INSERT INTO fail_commit VALUES(OLD.id); END`))
	purged, err := d.PurgeDeletedWorkspaces(ctx, at, 90)
	if err == nil || len(purged) != 0 {
		t.Fatalf("failed purge returned blobs %v, err %v", purged, err)
	}
}

// A file still open in an editor (drop not delivered) holds the purge back.
func TestPurgeDeletedWorkspaceWaitsForEditorDrop(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "doc.txt"}, blob(t, blobs, "doc"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.EditorSession(ctx, "worker", f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.DeleteWorkspace(ctx, "admin", w.ID, "Organization"); err != nil {
		t.Fatal(err)
	}
	if err = drive.ExecSQL(ctx, d, `UPDATE drive_workspaces SET deleted_at='2000-01-01T00:00:00Z' WHERE id=?`, w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PurgeDeletedWorkspaces(ctx, time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_files WHERE id=?`, f.ID); n != 1 {
		t.Fatal("purged under an open editor session")
	}
	if err = drive.ExecSQL(ctx, d, `UPDATE drive_editor_sessions SET drop_done=true WHERE file_id=?`, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PurgeDeletedWorkspaces(ctx, time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_files WHERE id=?`, f.ID); n != 0 {
		t.Fatal("not purged after the drop")
	}
}
