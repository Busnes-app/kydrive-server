package drive_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func managerFixture(t *testing.T) (*drive.Store, drive.Workspace, string) {
	t.Helper()
	st, w, root := fixture(t)
	if err := st.Drive().Grant(context.Background(), "admin", w.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
	return st.Drive(), w, root
}

func TestPurgeKeepsBlobsStillReferenced(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "shared"), 0)
	if err != nil {
		t.Fatal(err)
	}
	c, err := d.CopyFile(ctx, "worker", f.ID, drive.Location{Workspace: w.ID, Name: "b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.PurgeFile(ctx, "worker", f.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("purged a live file: %v", err)
	}
	if err = d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PurgeFile(ctx, "admin", f.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("admin bypassed content grants: %v", err)
	}
	blobs, err := d.PurgeFile(ctx, "worker", f.ID)
	if err != nil || len(blobs) != 0 {
		t.Fatalf("shared blob released early: %v %v", blobs, err)
	}
	if _, err = d.File(ctx, "worker", f.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("purged file still readable: %v", err)
	}
	if err = d.SetTrash(ctx, "worker", c.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	blobs, err = d.PurgeFile(ctx, "worker", c.ID)
	if err != nil || len(blobs) != 1 || blobs[0] != c.Blob {
		t.Fatalf("last reference: %v %v", blobs, err)
	}
	if err = drive.RemoveBlobs(root, blobs); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, c.Blob)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("blob kept: %v", err)
	}
	if err = drive.RemoveBlobs(root, blobs); err != nil {
		t.Fatalf("removing twice must be harmless: %v", err)
	}
	if err = drive.RemoveBlobs(root, []string{"../escape"}); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("non-UUID blob accepted: %v", err)
	}
}

func TestRestoreAfterPurgeFails(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "one"), 0)
	if _, err := d.Publish(ctx, "worker", f, blob(t, root, "two"), 1); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PurgeVersion(ctx, "worker", f.ID, 2); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("purged current version: %v", err)
	}
	blobs, err := d.PurgeVersion(ctx, "worker", f.ID, 1)
	if err != nil || len(blobs) != 1 {
		t.Fatalf("purge version: %v %v", blobs, err)
	}
	if _, err = d.RestoreVersion(ctx, "worker", f.ID, 1, 2); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("restored a purged version: %v", err)
	}
}

func TestPurgeWaitsForEditorRevocation(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if _, err := d.EditorSession(ctx, "worker", f.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PurgeFile(ctx, "worker", f.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("purged under an open editor: %v", err)
	}
	pending, err := d.PendingRevocations(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	if err = d.MarkDropped(ctx, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PurgeFile(ctx, "worker", f.ID); err != nil {
		t.Fatalf("purge after revocation: %v", err)
	}
}

func TestPurgeFolderRemovesTrashedSubtree(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	top, _ := d.AddFolder(ctx, "worker", w.ID, "", "Top")
	sub, _ := d.AddFolder(ctx, "worker", w.ID, top.ID, "Sub")
	if _, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: sub.ID, Name: "a.txt"}, blob(t, root, "a"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.PurgeFolder(ctx, "worker", top.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("purged a live folder: %v", err)
	}
	if err := d.SetFolderTrash(ctx, "worker", top.ID, true); err != nil {
		t.Fatal(err)
	}
	blobs, err := d.PurgeFolder(ctx, "worker", top.ID)
	if err != nil || len(blobs) != 1 {
		t.Fatalf("purge folder: %v %v", blobs, err)
	}
	if got := names(t, d, "worker", w.ID, true); len(got) != 0 {
		t.Fatalf("trash not empty: %v", got)
	}
	ws, _ := d.Workspaces(ctx, "worker", false)
	if ws[0].Used != 0 {
		t.Fatalf("usage not released: %d", ws[0].Used)
	}
}

func TestPurgeFileRefusesLiveFile(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if _, err := drive.PurgeFileUnauthorized(ctx, d, f.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("purged a live file: %v", err)
	}
	if _, err := drive.PurgeFileUnauthorized(ctx, d, "missing"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("unknown id: %v", err)
	}
	if vs, err := d.Versions(ctx, "worker", f.ID); err != nil || len(vs) != 1 {
		t.Fatalf("versions lost: %v %v", vs, err)
	}
}

func TestPurgeBlockedByUndeliveredDropAfterExpiry(t *testing.T) {
	d, w, root := managerFixture(t)
	ctx := context.Background()
	f, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if _, err := d.EditorSession(ctx, "worker", f.ID, "token"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := drive.ExecSQL(ctx, d, `UPDATE drive_editor_sessions SET expires=1`); err != nil {
		t.Fatal(err)
	}
	pending, err := d.PendingRevocations(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatal(pending, err)
	}
	if _, err = d.PurgeFile(ctx, "worker", f.ID); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("purged before the drop was delivered: %v", err)
	}
	if err = d.MarkDropped(ctx, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PurgeFile(ctx, "worker", f.ID); err != nil {
		t.Fatalf("purge after drop: %v", err)
	}
}
