package drive_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestMoveFileRenamesAndRelocates(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	folder, _ := d.AddFolder(ctx, "worker", w.ID, "", "Docs")
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "taken.txt"}, blob(t, root, "t"), 0); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		expected int64
		to       drive.Location
		want     error
	}{
		"taken name":     {1, drive.Location{Workspace: w.ID, Name: "taken.txt"}, drive.ErrConflict},
		"stale":          {7, drive.Location{Workspace: w.ID, Name: "b.txt"}, drive.ErrConflict},
		"bad name":       {1, drive.Location{Workspace: w.ID, Name: "a/b"}, drive.ErrInvalid},
		"foreign folder": {1, drive.Location{Workspace: w.ID, Parent: "missing", Name: "b.txt"}, drive.ErrInvalid},
	} {
		if _, err = d.MoveFile(ctx, "worker", f.ID, c.expected, c.to); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	moved, err := d.MoveFile(ctx, "worker", f.ID, 1, drive.Location{Workspace: w.ID, Parent: folder.ID, Name: "b.txt"})
	if err != nil || moved.Name != "b.txt" || moved.Parent != folder.ID || moved.Revision != 1 {
		t.Fatalf("move: %+v %v", moved, err)
	}
	if vs, _ := d.Versions(ctx, "worker", f.ID); len(vs) != 1 {
		t.Fatalf("move created a version: %d", len(vs))
	}
	reader := drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: w.ID, Rank: 1})
	if _, err = d.MoveFile(reader, "worker", f.ID, 1, drive.Location{Workspace: w.ID, Name: "c.txt"}); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("reader token moved: %v", err)
	}
}

func TestMoveAcrossWorkspacesRevokesLostAccess(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "colleague", Username: "colleague", Email: "colleague@local.test", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", ExternalID: "external-team", Members: []string{"worker", "colleague"}}, false); err != nil {
		t.Fatal(err)
	}
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "private", DisplayName: "Private", ExternalID: "external-private", Members: []string{"worker"}}, true); err != nil {
		t.Fatal(err)
	}
	target, err := d.CreateWorkspace(ctx, "admin", "Private", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Grant(ctx, "admin", target.ID, "private", "editor"); err != nil {
		t.Fatal(err)
	}
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "plan.txt"}, blob(t, root, "plan"), 0)
	if err != nil {
		t.Fatal(err)
	}
	session, err := d.EditorSession(ctx, "colleague", f.ID, "colleague-token")
	if err != nil {
		t.Fatal(err)
	}
	small, _ := d.CreateWorkspace(ctx, "admin", "Small", 1)
	if err = d.Grant(ctx, "admin", small.ID, "private", "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.MoveFile(ctx, "worker", f.ID, 1, drive.Location{Workspace: small.ID, Name: "plan.txt"}); !errors.Is(err, drive.ErrQuota) {
		t.Fatalf("quota ignored: %v", err)
	}
	if _, err = d.MoveFile(ctx, "colleague", f.ID, 1, drive.Location{Workspace: target.ID, Name: "plan.txt"}); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("moved into a workspace without access: %v", err)
	}
	if _, err = d.MoveFile(ctx, "worker", f.ID, 1, drive.Location{Workspace: target.ID, Name: "plan.txt"}); err != nil {
		t.Fatal(err)
	}
	pending, err := d.PendingRevocations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != session.ID {
		t.Fatalf("colleague's editor not revoked: %+v", pending)
	}
	if _, err = d.File(ctx, "colleague", f.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("colleague still reads moved file: %v", err)
	}
}

func TestMoveFileCancelledContextIsNotConflict(t *testing.T) {
	st, w, root := fixture(t)
	d := st.Drive()
	f, err := d.Publish(context.Background(), "worker", drive.File{Workspace: w.ID, Name: "a.txt"}, blob(t, root, "a"), 0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = d.MoveFile(ctx, "worker", f.ID, 1, drive.Location{Workspace: w.ID, Name: "b.txt"}); err == nil || errors.Is(err, drive.ErrConflict) {
		t.Fatalf("want non-conflict error, got %v", err)
	}
}
