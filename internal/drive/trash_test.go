package drive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func names(t *testing.T, d *drive.Store, user, workspace string, trashed bool) map[string]bool {
	t.Helper()
	ctx := context.Background()
	out := map[string]bool{}
	files, err := d.Files(ctx, user, workspace, trashed)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		out[f.Name] = true
	}
	folders, err := d.Folders(ctx, user, workspace, trashed)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		out[f.Name+"/"] = true
	}
	return out
}

func TestFolderTrashCascadesAndRestoresItsBatch(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	top, err := d.AddFolder(ctx, "worker", w.ID, "", "Top")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := d.AddFolder(ctx, "worker", w.ID, top.ID, "Sub")
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: sub.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	early, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: top.ID, Name: "early.txt"}, blob(t, root, "e"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetTrash(ctx, "worker", early.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "worker", top.ID, true); err != nil {
		t.Fatal(err)
	}
	if live := names(t, d, "worker", w.ID, false); len(live) != 0 {
		t.Fatalf("live after trash: %v", live)
	}
	if got := names(t, d, "worker", w.ID, true); !got["Top/"] || !got["Sub/"] || !got["a.txt"] || !got["early.txt"] {
		t.Fatalf("trash: %v", got)
	}
	if _, err = d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: sub.ID, Name: "late.txt"}, blob(t, root, "l"), 0); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("upload into trashed folder: %v", err)
	}
	if _, err = d.AddFolder(ctx, "worker", w.ID, "", "Top"); err != nil {
		t.Fatalf("trashed folder name not reusable: %v", err)
	}
	if err = d.SetFolderTrash(ctx, "worker", top.ID, false); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("restore over a live name: %v", err)
	}
	if err = d.SetFolderTrash(ctx, "worker", mustFolder(t, d, w.ID, "Top", false), true); err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "worker", top.ID, false); err != nil {
		t.Fatal(err)
	}
	live := names(t, d, "worker", w.ID, false)
	if !live["Top/"] || !live["Sub/"] || !live["a.txt"] || live["early.txt"] {
		t.Fatalf("restore must bring back its own batch only: %v", live)
	}
	if f, _ := d.File(ctx, "worker", a.ID, 1); f.Parent != sub.ID || f.Trashed {
		t.Fatalf("restored file moved: %+v", f)
	}
}

func TestFolderTrashRestoreEdgeCases(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	top, _ := d.AddFolder(ctx, "worker", w.ID, "", "Top")
	sub, _ := d.AddFolder(ctx, "worker", w.ID, top.ID, "Sub")
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: top.ID, Name: "f.txt"}, blob(t, root, "f"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "worker", top.ID, true); err != nil {
		t.Fatal(err)
	}
	// Restoring one item out of a trashed folder puts it at the root.
	if err = d.SetFolderTrash(ctx, "worker", sub.ID, false); err != nil {
		t.Fatal(err)
	}
	if err = d.SetTrash(ctx, "worker", f.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	folders, _ := d.Folders(ctx, "worker", w.ID, false)
	if len(folders) != 1 || folders[0].ID != sub.ID || folders[0].Parent != "" {
		t.Fatalf("sub folder: %+v", folders)
	}
	if got, _ := d.File(ctx, "worker", f.ID, 1); got.Parent != "" {
		t.Fatalf("file parent: %q", got.Parent)
	}
	// Toggling to the state an item is already in is a conflict, not a silent success.
	if err = d.SetTrash(ctx, "worker", f.ID, 1, false); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("double restore: %v", err)
	}
	if err = d.SetFolderTrash(ctx, "stranger", sub.ID, true); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("stranger trashed folder: %v", err)
	}
}

func mustFolder(t *testing.T, d *drive.Store, workspace, name string, trashed bool) string {
	t.Helper()
	folders, err := d.Folders(context.Background(), "worker", workspace, trashed)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		if f.Name == name {
			return f.ID
		}
	}
	t.Fatalf("no folder %q", name)
	return ""
}

func TestFolderTrashHidesStateFromStrangers(t *testing.T) {
	st, w, _ := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	live, err := d.AddFolder(ctx, "worker", w.ID, "", "Live")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := d.AddFolder(ctx, "worker", w.ID, "", "Gone")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "worker", gone.ID, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{live.ID, gone.ID, "missing"} {
		for _, trash := range []bool{true, false} {
			if err = d.SetFolderTrash(ctx, "stranger", id, trash); !errors.Is(err, drive.ErrDenied) {
				t.Fatalf("stranger %s trash=%v: %v", id, trash, err)
			}
		}
	}
	if err = d.SetFolderTrash(ctx, "worker", gone.ID, true); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("re-trash: %v", err)
	}
}

// A move between a caller's lookup and its trash transaction must not let the caller trash the
// file in a workspace it cannot reach.
func TestTrashIsBoundToTheAuthorizedWorkspace(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "colleague", Username: "colleague", Email: "colleague@local.test", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "editors", DisplayName: "Editors", ExternalID: "external-editors", Members: []string{"colleague"}}, true); err != nil {
		t.Fatal(err)
	}
	if err := d.Grant(ctx, "admin", w.ID, "editors", "editor"); err != nil {
		t.Fatal(err)
	}
	if err := d.Grant(ctx, "admin", w.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
	mine, err := d.EnsurePersonalWorkspace(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "plan.txt"}, blob(t, root, "p"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.MoveFile(ctx, "worker", f.ID, 1, drive.Location{Workspace: mine, Name: "plan.txt"}); err != nil {
		t.Fatal(err)
	}
	if err = drive.SetTrashAs(ctx, d, "colleague", f.ID, w.ID, 1, true); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("stale workspace trash: %v", err)
	}
	if got, err := d.File(ctx, "worker", f.ID, 1); err != nil || got.Trashed || got.Workspace != mine {
		t.Fatalf("file changed: %+v %v", got, err)
	}
	if err = d.SetTrash(ctx, "colleague", f.ID, 1, true); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("colleague trashed a file outside its workspaces: %v", err)
	}
}
