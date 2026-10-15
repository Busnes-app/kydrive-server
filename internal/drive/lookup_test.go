package drive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestLookupAndChildren(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	docs, err := d.AddFolder(ctx, "worker", w.ID, "", "Docs")
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := d.AddFolder(ctx, "worker", w.ID, docs.ID, "Sub")
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: docs.ID, Name: "Plan Ü.txt"}, blob(t, root, "x"), 0)
	if err != nil {
		t.Fatal(err)
	}
	gone, _ := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: docs.ID, Name: "old.txt"}, blob(t, root, "x"), 0)
	if err = d.SetTrash(ctx, "worker", gone.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	// The same name in another workspace must never answer for this one.
	if err = st.Groups().ReplaceGroup(ctx, &store.Group{ID: "other", DisplayName: "Other", ExternalID: "external-other", Members: []string{"worker"}}, true); err != nil {
		t.Fatal(err)
	}
	other, _ := d.CreateWorkspace(ctx, "admin", "Other", 100)
	if err = d.Grant(ctx, "admin", other.ID, "other", "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.AddFolder(ctx, "worker", other.ID, "", "Docs"); err != nil {
		t.Fatal(err)
	}

	if it, err := d.Lookup(ctx, "worker", w.ID, "", "Docs"); err != nil || it.Folder == nil || it.Folder.ID != docs.ID || it.File != nil {
		t.Fatalf("folder: %+v %v", it, err)
	}
	if it, err := d.Lookup(ctx, "worker", w.ID, docs.ID, "Plan Ü.txt"); err != nil || it.File == nil || it.File.ID != f.ID || it.File.Size != 1 {
		t.Fatalf("file: %+v %v", it, err)
	}
	for _, name := range []string{"plan ü.txt", "missing", "old.txt", "../Docs", ""} {
		if _, err := d.Lookup(ctx, "worker", w.ID, docs.ID, name); !errors.Is(err, drive.ErrNotFound) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := d.Lookup(ctx, "stranger", w.ID, "", "Docs"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("outsider: %v", err)
	}
	folders, files, err := d.Children(ctx, "worker", w.ID, docs.ID)
	if err != nil || len(folders) != 1 || folders[0].ID != sub.ID || len(files) != 1 || files[0].ID != f.ID {
		t.Fatalf("children: %+v %+v %v", folders, files, err)
	}
	if _, _, err := d.Children(ctx, "stranger", w.ID, docs.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("outsider children: %v", err)
	}
}
