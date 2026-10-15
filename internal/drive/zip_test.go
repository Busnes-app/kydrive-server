package drive_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func TestFolderTree(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	top, _ := d.AddFolder(ctx, "worker", w.ID, "", "Projekt Ü")
	sub, _ := d.AddFolder(ctx, "worker", w.ID, top.ID, "sub")
	gone, _ := d.AddFolder(ctx, "worker", w.ID, top.ID, "gone")
	for _, f := range []drive.Folder{{Parent: top.ID, Name: "empty"}, {Name: "sibling"}} {
		if _, err := d.AddFolder(ctx, "worker", w.ID, f.Parent, f.Name); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []drive.File{{Workspace: w.ID, Parent: top.ID, Name: "a.txt"}, {Workspace: w.ID, Parent: sub.ID, Name: "b.txt"}, {Workspace: w.ID, Parent: gone.ID, Name: "c.txt"}, {Workspace: w.ID, Name: "outside.txt"}} {
		if _, err := d.Publish(ctx, "worker", f, blob(t, root, f.Name), 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetFolderTrash(ctx, "worker", gone.ID, true); err != nil {
		t.Fatal(err)
	}
	name, entries, err := d.FolderTree(ctx, "worker", top.ID)
	if err != nil || name != "Projekt Ü" {
		t.Fatal(name, err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, fmt.Sprintf("%s:%d:%v", e.Path, e.Size, e.Blob != ""))
	}
	want := []string{"Projekt Ü/:0:false", "Projekt Ü/a.txt:5:true", "Projekt Ü/empty/:0:false", "Projekt Ü/sub/:0:false", "Projekt Ü/sub/b.txt:5:true"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if _, _, err = d.FolderTree(ctx, "stranger", top.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("outsider: %v", err)
	}
	if _, _, err = d.FolderTree(ctx, "worker", gone.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("trashed folder: %v", err)
	}
}
