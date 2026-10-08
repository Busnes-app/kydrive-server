package drive_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func TestRetentionIsAdminSetAndBounded(t *testing.T) {
	d, w, _ := managerFixture(t)
	ctx := context.Background()
	if err := d.SetRetention(ctx, "worker", w.ID, 30, 5); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("manager set retention: %v", err)
	}
	for _, c := range [][2]int{{-1, 0}, {3651, 0}, {0, 1001}} {
		if err := d.SetRetention(ctx, "admin", w.ID, c[0], c[1]); !errors.Is(err, drive.ErrInvalid) {
			t.Fatalf("%v accepted: %v", c, err)
		}
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 30, 5); err != nil {
		t.Fatal(err)
	}
	ws, _ := d.Workspaces(ctx, "worker", false)
	if ws[0].TrashDays != 30 || ws[0].KeepVersions != 5 {
		t.Fatalf("not saved: %+v", ws[0])
	}
}

func TestRetentionEmptiesOldTrash(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if err := d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if blobs, err := d.ApplyRetention(ctx, time.Now().Add(365*24*time.Hour)); err != nil || len(blobs) != 0 {
		t.Fatalf("retention off by default: %v %v", blobs, err)
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 30, 0); err != nil {
		t.Fatal(err)
	}
	if blobs, err := d.ApplyRetention(ctx, time.Now().Add(29*24*time.Hour)); err != nil || len(blobs) != 0 {
		t.Fatalf("purged early: %v", blobs)
	}
	blobs, err := d.ApplyRetention(ctx, time.Now().Add(31*24*time.Hour))
	if err != nil || len(blobs) != 1 {
		t.Fatalf("not purged: %v %v", blobs, err)
	}
}

func TestRetentionSkipsLiveEditorVersions(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "v1"), 0)
	f, _ = d.Publish(ctx, "worker", f, blob(t, root, "v2"), 1)
	if _, err := d.EditorSession(ctx, "worker", f.ID, "token"); err != nil { // based on revision 2
		t.Fatal(err)
	}
	f, _ = d.Publish(ctx, "worker", f, blob(t, root, "v3"), 2)
	if _, err := d.Publish(ctx, "worker", f, blob(t, root, "v4"), 3); err != nil {
		t.Fatal(err)
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 0, 2); err != nil {
		t.Fatal(err)
	}
	blobs, err := d.ApplyRetention(ctx, time.Now())
	if err != nil || len(blobs) != 1 {
		t.Fatalf("expected only revision 1 purged: %v %v", blobs, err)
	}
	vs, _ := d.Versions(ctx, "worker", f.ID)
	if len(vs) != 3 || vs[2].Revision != 2 {
		t.Fatalf("versions left: %+v", vs)
	}
}

func TestRetentionRemovesExpiredFolderTreesWithEvents(t *testing.T) {
	d, w, _ := managerFixture(t)
	ctx := context.Background()
	top, _ := d.AddFolder(ctx, "worker", w.ID, "", "Top")
	sub, _ := d.AddFolder(ctx, "worker", w.ID, top.ID, "Sub")
	if err := d.SetFolderTrash(ctx, "worker", top.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 30, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ApplyRetention(ctx, time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	fs, _ := d.Folders(ctx, "worker", w.ID, true)
	if len(fs) != 0 {
		t.Fatalf("folders left: %+v", fs)
	}
	evs, _ := d.Events(ctx, "admin")
	seen := map[string]bool{}
	for _, e := range evs {
		if e.Action == "folder.retention_purged" && e.User == "system" {
			seen[e.Resource] = true
		}
	}
	if !seen[top.ID] || !seen[sub.ID] {
		t.Fatalf("missing events: %v", seen)
	}
}

func trashedWithPolicy(t *testing.T) (*drive.Store, drive.Workspace, drive.File, time.Time) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if err := d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 30, 0); err != nil {
		t.Fatal(err)
	}
	return d, w, f, time.Now().Add(31 * 24 * time.Hour)
}

func TestRetentionRechecksPolicyBeforePurgingFile(t *testing.T) {
	ctx := context.Background()
	d, w, f, at := trashedWithPolicy(t)
	if err := d.SetRetention(ctx, "admin", w.ID, 0, 0); err != nil {
		t.Fatal(err)
	}
	if blobs, err := drive.RetainFile(ctx, d, f.ID, at); !errors.Is(err, drive.ErrInvalid) || len(blobs) != 0 {
		t.Fatalf("policy off: %v %v", blobs, err)
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 30, 0); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTrash(ctx, "worker", f.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	if blobs, err := drive.RetainFile(ctx, d, f.ID, at); !errors.Is(err, drive.ErrInvalid) || len(blobs) != 0 {
		t.Fatalf("restored: %v %v", blobs, err)
	}
	if vs, _ := d.Versions(ctx, "worker", f.ID); len(vs) != 1 {
		t.Fatalf("history lost: %+v", vs)
	}
}

func TestRetentionRechecksKeepVersionsBeforePurgingVersion(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "v1"), 0)
	f, _ = d.Publish(ctx, "worker", f, blob(t, root, "v2"), 1)
	f, _ = d.Publish(ctx, "worker", f, blob(t, root, "v3"), 2)
	if err := d.SetRetention(ctx, "admin", w.ID, 0, 2); err != nil {
		t.Fatal(err)
	}
	if err := d.SetRetention(ctx, "admin", w.ID, 0, 5); err != nil {
		t.Fatal(err)
	}
	if blobs, err := drive.RetainVersion(ctx, d, f.ID, 1); !errors.Is(err, drive.ErrInvalid) || len(blobs) != 0 {
		t.Fatalf("raised keep: %v %v", blobs, err)
	}
	if vs, _ := d.Versions(ctx, "worker", f.ID); len(vs) != 3 {
		t.Fatalf("version lost: %+v", vs)
	}
}

func TestRetentionIsNotStarvedByStuckItems(t *testing.T) {
	d, w, _ := managerFixture(t)
	ctx := context.Background()
	if err := d.SetRetention(ctx, "admin", w.ID, 1, 0); err != nil {
		t.Fatal(err)
	}
	must := func(q string, args ...any) {
		t.Helper()
		if err := drive.ExecSQL(ctx, d, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// 501 files trashed long ago, each held by a live editor session, then one purgeable file trashed later.
	must(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<501)
INSERT INTO drive_files(id,workspace,parent,name,revision,trashed,trashed_at) SELECT 'stuck'||i,?,'','s'||i,1,true,'2000-01-01T00:00:00Z' FROM n`, w.ID)
	must(`INSERT INTO drive_editor_sessions(id,file_id,revision,user_id,token_hash,expires) SELECT 'sess'||id,id,1,'worker','h',99999999999 FROM drive_files WHERE id LIKE 'stuck%'`)
	must(`INSERT INTO drive_files(id,workspace,parent,name,revision,trashed,trashed_at) VALUES('free',?,'','free',1,true,'2001-01-01T00:00:00Z')`, w.ID)
	must(`INSERT INTO drive_versions VALUES('free',1,'b','d',0,'2001-01-01T00:00:00Z')`)
	if _, err := d.ApplyRetention(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	vs, _ := d.Versions(ctx, "worker", "free")
	if len(vs) != 0 {
		t.Fatalf("starved: %+v", vs)
	}
}

func TestRetentionRefusesPersonalWorkspace(t *testing.T) {
	d, _, root := managerFixture(t)
	ctx := context.Background()
	mine, err := d.EnsurePersonalWorkspace(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: mine, Name: "a.txt"}, blob(t, root, "a"), 0)
	if err = d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if err = d.SetRetention(ctx, "admin", mine, 1, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("admin set retention on My files: %v", err)
	}
	if _, err = d.ApplyRetention(ctx, time.Now().Add(3650*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err = d.File(ctx, "worker", f.ID, 1); err != nil {
		t.Fatalf("personal file purged: %v", err)
	}
}
