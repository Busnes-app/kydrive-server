package drive_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func events(t *testing.T, d *drive.Store, action string) []string {
	t.Helper()
	all, err := d.Events(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range all {
		if e.Action == action {
			out = append(out, e.Resource)
		}
	}
	return out
}

func TestRenameWorkspace(t *testing.T) {
	st, w, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	studio, err := d.CreateWorkspace(ctx, "admin", "Studio", 100)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := d.EnsurePersonalWorkspace(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		user, workspace, name string
		want                  error
	}{
		{"worker", studio.ID, "Design", drive.ErrDenied},        // not an administrator
		{"admin", studio.ID, "organization", drive.ErrConflict}, // taken, ignoring case
		{"admin", studio.ID, " Design", drive.ErrInvalid},
		{"admin", mine, "Mine", drive.ErrDenied}, // My files keeps its name
		{"admin", "no-such-workspace", "Design", drive.ErrDenied},
	} {
		if err := d.RenameWorkspace(ctx, c.user, c.workspace, c.name); !errors.Is(err, c.want) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	// A workspace may change the case of its own name.
	if err := d.RenameWorkspace(ctx, "admin", studio.ID, "studio"); err != nil {
		t.Fatal(err)
	}
	if err := d.RenameWorkspace(ctx, "admin", studio.ID, "Design"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateWorkspace(ctx, "admin", "Studio", 100); err != nil {
		t.Fatalf("old name still held: %v", err)
	}
	list, err := d.Workspaces(ctx, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, x := range list {
		names[x.ID] = x.Name
	}
	if names[studio.ID] != "Design" || names[w.ID] != "Organization" {
		t.Fatalf("names after rename: %v", names)
	}
	got := events(t, d, "workspace.renamed")
	want := []string{studio.ID + ` from="studio" to="Design"`, studio.ID + ` from="Studio" to="studio"`}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rename events %q, want %q", got, want)
	}
}

func TestDeleteWorkspace(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	refuse := func(want error, what string) {
		t.Helper()
		if err := d.DeleteWorkspace(ctx, "admin", w.ID); !errors.Is(err, want) {
			t.Fatalf("%s: %v", what, err)
		}
	}
	folder, err := d.AddFolder(ctx, "worker", w.ID, "", "a")
	must(err)
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: folder.ID, Name: "plan.txt"}, blob(t, blobs, "plan"), 0)
	must(err)

	// Access that must not outlive the workspace.
	_, err = d.Share(ctx, "worker", "folder", folder.ID, "user", "stranger", "reader")
	must(err)
	_, link, err := d.CreateLink(ctx, "worker", "folder", folder.ID, "reader", 0)
	must(err)
	_, err = d.OpenLink(ctx, "stranger", link)
	must(err)
	_, guest, err := d.CreateGuestLink(ctx, "worker", "folder", folder.ID, inDays(1), "", false, week)
	must(err)
	_, err = d.CreateServiceToken(ctx, "worker", w.ID, "Robot", "reader", "svc-delete-workspace")
	must(err)
	_, scoped, err := d.CreateAgentToken(ctx, "worker", "Scoped agent", drive.ScopeMCPRead, w.ID, "")
	must(err)
	_, anywhere, err := d.CreateAgentToken(ctx, "worker", "Agent", drive.ScopeMCPRead, "", "")
	must(err)

	if err := d.DeleteWorkspace(ctx, "worker", w.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("non-admin deleted a workspace: %v", err)
	}
	mine, err := d.EnsurePersonalWorkspace(ctx, "admin")
	must(err)
	if err := d.DeleteWorkspace(ctx, "admin", mine); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("My files deleted: %v", err)
	}
	refuse(drive.ErrConflict, "live items")
	must(d.SetFolderTrash(ctx, "worker", folder.ID, true))
	refuse(drive.ErrConflict, "trashed items only")
	_, err = d.PurgeFolder(ctx, "worker", folder.ID)
	must(err)
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_files WHERE id=?`, f.ID); n != 0 {
		t.Fatal("purging the folder left its file")
	}

	// An upload in progress blocks the delete; an expired one does not.
	u, err := d.StartUpload(ctx, "worker", drive.UploadSession{Workspace: w.ID, Name: "late.txt", Size: 4}, 100)
	must(err)
	refuse(drive.ErrConflict, "upload in progress")
	must(drive.ExecSQL(ctx, d, `UPDATE drive_upload_sessions SET expires=0 WHERE id=?`, u.ID))

	must(d.DeleteWorkspace(ctx, "admin", w.ID))

	list, err := d.Workspaces(ctx, "admin", true)
	must(err)
	for _, x := range list {
		if x.ID == w.ID {
			t.Fatal("deleted workspace still listed")
		}
	}
	if _, _, err := d.ServiceToken(ctx, "svc-delete-workspace"); err == nil {
		t.Fatal("service token survived")
	}
	if _, _, err := d.AgentTokenUser(ctx, scoped, time.Now()); err == nil {
		t.Fatal("agent token scoped to the workspace survived")
	}
	if _, _, err := d.AgentTokenUser(ctx, anywhere, time.Now()); err != nil {
		t.Fatalf("unscoped agent token revoked: %v", err)
	}
	if _, err := d.OpenLink(ctx, "stranger", link); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("organisation link: %v", err)
	}
	if _, err := d.GuestView(ctx, guest, "", ""); !errors.Is(err, drive.ErrNotFound) {
		t.Fatalf("guest link: %v", err)
	}
	if shared, err := d.SharedWithMe(ctx, "stranger"); err != nil || len(shared) != 0 {
		t.Fatalf("shared with me: %v %v", shared, err)
	}
	// Desktop sync sees the workspace gone (403, which the client evicts on), not a 500.
	if _, _, _, err := d.Changes(ctx, "worker", w.ID, 0, 10); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("changes: %v", err)
	}
	if _, _, _, _, err := d.Snapshot(ctx, "worker", w.ID, "", 10); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("snapshot: %v", err)
	}
	for _, table := range []string{"drive_grants", "drive_shares", "drive_links", "drive_upload_sessions", "drive_changes", "drive_change_floor"} {
		if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM `+table+` WHERE workspace=?`, w.ID); err != nil || n != 0 {
			t.Errorf("%s keeps %d rows (%v)", table, n, err)
		}
	}
	if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_link_users`); err != nil || n != 0 {
		t.Errorf("drive_link_users keeps %d rows (%v)", n, err)
	}
	if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM pragma_foreign_key_check`); err != nil || n != 0 {
		t.Errorf("%d dangling references (%v)", n, err)
	}

	// Deleting again changes nothing and records nothing.
	must(d.DeleteWorkspace(ctx, "admin", w.ID))
	got := events(t, d, "workspace.deleted")
	want := w.ID + ` name="Organization" links=2 shares=1 grants=1 service_tokens=1 agent_tokens=1`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("delete events %q, want [%q]", got, want)
	}
	// The name is free again.
	if _, err := d.CreateWorkspace(ctx, "admin", "Organization", 100); err != nil {
		t.Fatal(err)
	}
}

// A publish racing the delete either lands first (the delete is refused) or is refused itself;
// never a deleted workspace with files.
func TestDeleteWorkspaceRacesUploads(t *testing.T) {
	st, _, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	for round := range 10 {
		target, err := d.CreateWorkspace(ctx, "admin", fmt.Sprintf("Race %d", round), 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		if err = d.Grant(ctx, "admin", target.ID, "team", "editor"); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		published := make(chan error, 4)
		for i := range 4 {
			v := blob(t, blobs, fmt.Sprintf("body %d", i))
			wg.Go(func() {
				_, err := d.Publish(ctx, "worker", drive.File{Workspace: target.ID, Name: fmt.Sprintf("f%d.txt", i)}, v, 0)
				published <- err
			})
		}
		deleted := d.DeleteWorkspace(ctx, "admin", target.ID)
		wg.Wait()
		close(published)
		landed := 0
		for err := range published {
			switch {
			case err == nil:
				landed++
			case !errors.Is(err, drive.ErrDenied):
				t.Fatalf("publish: %v", err)
			}
		}
		files, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_files WHERE workspace=?`, target.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case deleted == nil && files != 0:
			t.Fatalf("deleted workspace holds %d files", files)
		case deleted != nil && !errors.Is(deleted, drive.ErrConflict):
			t.Fatalf("delete: %v", deleted)
		case files != landed:
			t.Fatalf("%d files for %d publishes", files, landed)
		}
	}
	if n, err := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM pragma_foreign_key_check`); err != nil || n != 0 {
		t.Fatalf("%d dangling references (%v)", n, err)
	}
}
