package drive_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// leaverFixture gives "leaver" a personal tree: Docs/a.txt (2 versions, open in the editor),
// a trashed root file and a trashed folder holding a file, plus a service token.
func leaverFixture(t *testing.T) (store.Store, string, drive.File, drive.EditorSession, string) {
	t.Helper()
	st, _, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "leaver", Username: "leaver", DisplayName: "Lee Ver", Email: "leaver@local.test", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	src, err := d.EnsurePersonalWorkspace(ctx, "leaver")
	if err != nil {
		t.Fatal(err)
	}
	docs, err := d.AddFolder(ctx, "leaver", src, "", "Docs")
	if err != nil {
		t.Fatal(err)
	}
	a, err := d.Publish(ctx, "leaver", drive.File{Workspace: src, Parent: docs.ID, Name: "a.txt"}, blob(t, root, "one"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if a, err = d.Publish(ctx, "leaver", a, blob(t, root, "two"), 1); err != nil {
		t.Fatal(err)
	}
	b, err := d.Publish(ctx, "leaver", drive.File{Workspace: src, Name: "b.txt"}, blob(t, root, "bee"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetTrash(ctx, "leaver", b.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	old, _ := d.AddFolder(ctx, "leaver", src, "", "Old")
	if _, err = d.Publish(ctx, "leaver", drive.File{Workspace: src, Parent: old.ID, Name: "c.txt"}, blob(t, root, "sea"), 0); err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "leaver", old.ID, true); err != nil {
		t.Fatal(err)
	}
	session, err := d.EditorSession(ctx, "leaver", a.ID, "leaver-editor")
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("l", 64)
	if _, err = d.CreateServiceToken(ctx, "leaver", src, "Sync", "editor", token); err != nil {
		t.Fatal(err)
	}
	return st, src, a, session, token
}

func TestHandoverMovesTheWholeTreeToAColleague(t *testing.T) {
	st, src, a, session, token := leaverFixture(t)
	ctx := context.Background()
	d := st.Drive()
	if _, err := d.Handover(ctx, "admin", src, drive.Target{User: "worker"}); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("handed over an active owner: %v", err)
	}
	if err := drive.ExecSQL(ctx, d, `UPDATE users SET status='inactive' WHERE id='leaver'`); err != nil {
		t.Fatal(err)
	}
	leavers, err := d.Leavers(ctx, "admin")
	if err != nil || len(leavers) != 1 || leavers[0].Workspace != src || leavers[0].Name != "Lee Ver" || leavers[0].Status != "inactive" || leavers[0].Items != 5 || leavers[0].Size != 12 {
		t.Fatalf("leavers: %+v %v", leavers, err)
	}
	tiny, err := d.CreateWorkspace(ctx, "admin", "Tiny", 11)
	if err != nil {
		t.Fatal(err)
	}
	service := drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: src, Rank: 2})
	for name, c := range map[string]struct {
		ctx  context.Context
		user string
		to   drive.Target
		want error
	}{
		"non-admin":     {ctx, "worker", drive.Target{User: "worker"}, drive.ErrDenied},
		"service token": {service, "admin", drive.Target{User: "worker"}, drive.ErrDenied},
		"over quota":    {ctx, "admin", drive.Target{Workspace: tiny.ID}, drive.ErrQuota},
		"personal id":   {ctx, "admin", drive.Target{Workspace: src}, drive.ErrDenied},
		"both targets":  {ctx, "admin", drive.Target{Workspace: tiny.ID, User: "worker"}, drive.ErrInvalid},
		"leaver target": {ctx, "admin", drive.Target{User: "leaver"}, drive.ErrDenied},
	} {
		if _, err = d.Handover(c.ctx, c.user, src, c.to); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if after, _ := d.Leavers(ctx, "admin"); len(after) != 1 || after[0].Items != 5 {
		t.Fatalf("a refused handover moved items: %+v", after)
	}
	if _, err = d.Leavers(ctx, "worker"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("non-admin listed leavers: %v", err)
	}

	if err = drive.ExecSQL(ctx, d, `UPDATE drive_files SET trashed_at='2000-01-01T00:00:00Z' WHERE trashed`); err != nil {
		t.Fatal(err)
	}
	folder, err := d.Handover(ctx, "admin", src, drive.Target{User: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	mine, err := d.EnsurePersonalWorkspace(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	live, _ := d.Folders(ctx, "worker", mine, false)
	if len(live) != 2 || live[0].Name != "Docs" || live[0].Parent != folder || live[1].ID != folder || live[1].Name != "Handover from Lee Ver "+time.Now().UTC().Format(time.DateOnly) {
		t.Fatalf("live folders: %+v", live)
	}
	files, _ := d.Files(ctx, "worker", mine, false)
	if len(files) != 1 || files[0].ID != a.ID || files[0].Parent != live[0].ID {
		t.Fatalf("live files: %+v", files)
	}
	if vs, err := d.Versions(ctx, "worker", a.ID); err != nil || len(vs) != 2 {
		t.Fatalf("versions: %d %v", len(vs), err)
	}
	trashed, _ := d.Files(ctx, "worker", mine, true)
	trashedFolders, _ := d.Folders(ctx, "worker", mine, true)
	if len(trashed) != 2 || len(trashedFolders) != 1 || trashedFolders[0].Parent != folder {
		t.Fatalf("trash: %+v %+v", trashed, trashedFolders)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_files WHERE trashed AND julianday('now')-julianday(trashed_at)<0.01 AND workspace=?`, mine); n != 2 {
		t.Fatalf("trash clock not restarted: %d", n)
	}
	if _, err = d.File(ctx, "admin", a.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("admin reads handed-over content: %v", err)
	}

	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_editor_sessions WHERE id=? AND revoked=false`, session.ID); n != 0 {
		t.Fatal("editor session not revoked")
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM drive_editor_documents WHERE key=? AND closed=false`, session.Key); n != 0 {
		t.Fatal("editor document still open")
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT editor_epoch FROM drive_files WHERE id=?`, a.ID); n != 1 {
		t.Fatalf("epoch: %d", n)
	}

	events, _ := d.Events(ctx, "admin")
	found := false
	for _, e := range events {
		if e.Action == "workspace.handed_over" {
			found = true
			for _, name := range []string{"Lee Ver", "Docs", "a.txt", "b.txt", "Old", "Handover"} {
				if strings.Contains(e.Resource, name) {
					t.Fatalf("event names content: %q", e.Resource)
				}
			}
		}
	}
	if !found {
		t.Fatal("no handover event")
	}

	if _, err = d.Handover(ctx, "admin", src, drive.Target{User: "worker"}); !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("second handover: %v", err)
	}
	if after, _ := d.Leavers(ctx, "admin"); len(after) != 0 {
		t.Fatalf("empty workspace still listed: %+v", after)
	}

	// A reactivated owner gets the same, now empty, My files; the old token stays dead.
	if err = drive.ExecSQL(ctx, d, `UPDATE users SET status='active' WHERE id='leaver'`); err != nil {
		t.Fatal(err)
	}
	if again, err := d.EnsurePersonalWorkspace(ctx, "leaver"); err != nil || again != src {
		t.Fatalf("reactivated workspace: %s %v", again, err)
	}
	if fs, _ := d.Files(ctx, "leaver", src, true); len(fs) != 0 {
		t.Fatalf("source not empty: %+v", fs)
	}
	if _, _, err = d.ServiceToken(ctx, token); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("source token survived: %v", err)
	}
}

func TestHandoverOfADeletedAccountToASharedWorkspace(t *testing.T) {
	st, src, _, _, _ := leaverFixture(t)
	ctx := context.Background()
	d := st.Drive()
	if err := st.Users().DeleteUser(ctx, "leaver"); err != nil {
		t.Fatal(err)
	}
	leavers, err := d.Leavers(ctx, "admin")
	if err != nil || len(leavers) != 1 || leavers[0].Name != "Lee Ver" || leavers[0].Status != "deleted" {
		t.Fatalf("leavers: %+v %v", leavers, err)
	}
	archive, err := d.CreateWorkspace(ctx, "admin", "Archive", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Grant(ctx, "admin", archive.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
	folder, err := d.OperatorHandover(ctx, src, drive.Target{Workspace: archive.ID})
	if err != nil {
		t.Fatal(err)
	}
	folders, _ := d.Folders(ctx, "worker", archive.ID, false)
	if len(folders) != 2 || folders[1].ID != folder || !strings.HasPrefix(folders[1].Name, "Handover from Lee Ver ") {
		t.Fatalf("folders: %+v", folders)
	}
	if n, _ := drive.QueryInt(ctx, d, `SELECT COUNT(*) FROM audit_records WHERE action='drive.handover' AND user_id=? AND ip_address='operator-cli' AND resource=?`, drive.OperatorActor, src); n != 1 {
		t.Fatalf("operator audit records: %d", n)
	}
}

func TestHandoverFolderNameIsNumberedWhenTaken(t *testing.T) {
	st, src, _, _, _ := leaverFixture(t)
	ctx := context.Background()
	d := st.Drive()
	if err := drive.ExecSQL(ctx, d, `UPDATE users SET status='suspended' WHERE id='leaver'`); err != nil {
		t.Fatal(err)
	}
	archive, _ := d.CreateWorkspace(ctx, "admin", "Archive", 1<<20)
	if err := d.Grant(ctx, "admin", archive.ID, "team", "editor"); err != nil {
		t.Fatal(err)
	}
	taken := "Handover from Lee Ver " + time.Now().UTC().Format(time.DateOnly)
	if _, err := d.AddFolder(ctx, "worker", archive.ID, "", taken); err != nil {
		t.Fatal(err)
	}
	folder, err := d.Handover(ctx, "admin", src, drive.Target{Workspace: archive.ID})
	if err != nil {
		t.Fatal(err)
	}
	folders, _ := d.Folders(ctx, "worker", archive.ID, false)
	for _, f := range folders {
		if f.ID == folder && f.Name != taken+" (2)" {
			t.Fatalf("handover folder %q", f.Name)
		}
	}
}

// Workspaces created before owner names were kept get them from the account on upgrade.
func TestSchemaBackfillsOwnerNames(t *testing.T) {
	ctx := context.Background()
	cfg := testdb.Config(t)
	if cfg.Driver != "sqlite" {
		t.Skip("drive is SQLite-only")
	}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Users().CreateUser(ctx, &store.User{ID: "old", Username: "old", DisplayName: "Old Timer", Email: "old@local.test", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Drive().EnsurePersonalWorkspace(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", cfg.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, `ALTER TABLE drive_personal_workspaces DROP COLUMN owner_name; DELETE FROM drive_schema WHERE version=4`); err != nil {
		t.Fatal(err)
	}
	if st, err = store.Open(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	st.Close()
	var name string
	if err = db.QueryRowContext(ctx, `SELECT owner_name FROM drive_personal_workspaces WHERE owner='old'`).Scan(&name); err != nil || name != "Old Timer" {
		t.Fatalf("backfill: %q %v", name, err)
	}
}
