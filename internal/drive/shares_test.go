package drive_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

type tree struct {
	a, b, c        drive.Folder // a/b and c at the root
	inB, inA, inC  drive.File
	root, rootFile string
}

// layout builds a/b/inB.txt, a/inA.txt, c/inC.txt and root.txt in w as worker.
func layout(t *testing.T, d *drive.Store, w drive.Workspace, blobs string) tree {
	t.Helper()
	ctx := context.Background()
	var tr tree
	var err error
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	tr.a, err = d.AddFolder(ctx, "worker", w.ID, "", "a")
	must(err)
	tr.b, err = d.AddFolder(ctx, "worker", w.ID, tr.a.ID, "b")
	must(err)
	tr.c, err = d.AddFolder(ctx, "worker", w.ID, "", "c")
	must(err)
	for _, x := range []struct {
		parent, name string
		out          *drive.File
	}{{tr.b.ID, "inB.txt", &tr.inB}, {tr.a.ID, "inA.txt", &tr.inA}, {tr.c.ID, "inC.txt", &tr.inC}} {
		*x.out, err = d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: x.parent, Name: x.name}, blob(t, blobs, "x"), 0)
		must(err)
	}
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "root.txt"}, blob(t, blobs, "x"), 0)
	must(err)
	tr.rootFile = f.ID
	return tr
}

func managers(t *testing.T, d *drive.Store, w drive.Workspace) {
	t.Helper()
	if err := d.Grant(context.Background(), "admin", w.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
}

// A folder share reaches exactly its subtree, at exactly its role.
func TestFolderShareScope(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	tr := layout(t, d, w, blobs)
	sh, err := d.Share(ctx, "worker", "folder", tr.a.ID, "user", "stranger", "reader")
	if err != nil {
		t.Fatal(err)
	}
	reads := map[string]bool{tr.inA.ID: true, tr.inB.ID: true, tr.inC.ID: false, tr.rootFile: false}
	for id, want := range reads {
		if _, err := d.File(ctx, "stranger", id, 1); (err == nil) != want {
			t.Errorf("read %s: %v, want access=%v", id, err, want)
		}
	}
	if _, _, err := d.Children(ctx, "stranger", w.ID, tr.a.ID); err != nil {
		t.Errorf("list shared folder: %v", err)
	}
	if _, _, err := d.Children(ctx, "stranger", w.ID, tr.b.ID); err != nil {
		t.Errorf("list subfolder: %v", err)
	}
	for _, folder := range []string{"", tr.c.ID} {
		if _, _, err := d.Children(ctx, "stranger", w.ID, folder); !errors.Is(err, drive.ErrDenied) {
			t.Errorf("list %q: %v", folder, err)
		}
	}
	if _, err := d.Files(ctx, "stranger", w.ID, false); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("whole-workspace listing: %v", err)
	}
	if _, err := d.AddFolder(ctx, "stranger", w.ID, tr.a.ID, "mine"); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("reader created a folder: %v", err)
	}
	if _, err := d.Share(ctx, "worker", "folder", tr.a.ID, "user", "stranger", "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddFolder(ctx, "stranger", w.ID, tr.a.ID, "mine"); err != nil {
		t.Errorf("editor share cannot create inside: %v", err)
	}
	if _, err := d.Publish(ctx, "stranger", drive.File{Workspace: w.ID, Parent: tr.b.ID, Name: "new.txt"}, blob(t, blobs, "y"), 0); err != nil {
		t.Errorf("editor share cannot upload into subfolder: %v", err)
	}
	if _, err := d.Publish(ctx, "stranger", drive.File{Workspace: w.ID, Name: "escape.txt"}, blob(t, blobs, "y"), 0); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("upload outside the share: %v", err)
	}
	if _, err := d.MoveFile(ctx, "stranger", tr.inA.ID, 1, drive.Location{Workspace: w.ID, Parent: tr.c.ID, Name: "inA.txt"}); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("moved a file out of the share: %v", err)
	}
	// A forged parent cannot point a write through the share at a file outside it.
	outside, _ := d.File(ctx, "worker", tr.inC.ID, 1)
	forged := outside
	forged.Parent = tr.a.ID
	if _, err := d.Publish(ctx, "stranger", forged, blob(t, blobs, "z"), outside.Revision); err == nil {
		t.Error("write through a forged parent")
	}
	if err := d.SetTrash(ctx, "stranger", tr.inA.ID, 1, true); err != nil {
		t.Errorf("editor share cannot trash inside: %v", err)
	}
	if err := d.Unshare(ctx, "worker", sh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.File(ctx, "stranger", tr.inB.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Errorf("access after unshare: %v", err)
	}
}

func TestShareRolesAndWhoCanShare(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	tr := layout(t, d, w, blobs)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "gone", Username: "gone", Email: "gone@local.test", Role: "user", Status: "inactive", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	// Worker is a workspace editor: no sharing while editors_share is off.
	if _, err := d.Share(ctx, "worker", "file", tr.inA.ID, "user", "stranger", "reader"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("editor shared with editors_share off: %v", err)
	}
	if err := d.SetEditorsShare(ctx, "worker", w.ID, true); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("editor changed the sharing setting: %v", err)
	}
	managers(t, d, w)
	if err := d.SetEditorsShare(ctx, "worker", w.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := d.Grant(ctx, "admin", w.ID, "team", "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Share(ctx, "worker", "file", tr.inA.ID, "user", "stranger", "editor"); err != nil {
		t.Fatalf("editor could not share at their own role: %v", err)
	}
	if _, err := d.Share(ctx, "worker", "file", tr.inA.ID, "user", "stranger", "manager"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("editor shared above their role: %v", err)
	}
	// The stranger now edits the file but can never pass that on: only workspace members share.
	if _, err := d.Share(ctx, "stranger", "file", tr.inA.ID, "group", "team", "reader"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("share holder re-shared: %v", err)
	}
	for _, c := range []struct{ kind, id, pkind, pid, role string }{
		{"file", tr.inA.ID, "user", "gone", "reader"},     // inactive person
		{"file", tr.inA.ID, "user", "missing", "reader"},  // no such person
		{"file", tr.inA.ID, "group", "missing", "reader"}, // no such group
		{"file", tr.inA.ID, "user", "stranger", "owner"},  // no such role
		{"thing", tr.inA.ID, "user", "stranger", "reader"},
		{"file", "missing", "user", "stranger", "reader"},
	} {
		if _, err := d.Share(ctx, "worker", c.kind, c.id, c.pkind, c.pid, c.role); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	scoped := drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: w.ID, Rank: 2})
	if _, err := d.Share(scoped, "worker", "file", tr.inA.ID, "user", "stranger", "reader"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("service token shared: %v", err)
	}
	list, err := d.Shares(ctx, "worker", "file", tr.inA.ID)
	if err != nil || len(list) != 1 || list[0].Name != "stranger" || list[0].Role != "editor" {
		t.Fatalf("shares %+v %v", list, err)
	}
	if _, err := d.Shares(ctx, "stranger", "file", tr.inA.ID); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("share holder listed shares: %v", err)
	}
}

func TestPersonalShareStopsWhenOwnerDeactivated(t *testing.T) {
	st, _, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	mine, err := d.EnsurePersonalWorkspace(ctx, "worker")
	if err != nil {
		t.Fatal(err)
	}
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: mine, Name: "draft.docx"}, blob(t, blobs, "x"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.Share(ctx, "worker", "file", f.ID, "user", "stranger", "editor"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.File(ctx, "stranger", f.ID, 2); err != nil {
		t.Fatalf("shared draft unreadable: %v", err)
	}
	for _, status := range []string{"inactive", "active"} {
		if err = drive.ExecSQL(ctx, d, `UPDATE users SET status=? WHERE id='worker'`, status); err != nil {
			t.Fatal(err)
		}
		_, err = d.File(ctx, "stranger", f.ID, 1)
		if (err == nil) != (status == "active") {
			t.Fatalf("owner %s: %v", status, err)
		}
	}
}

func TestShareRevocationRevokesEditor(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	tr := layout(t, d, w, blobs)
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "partners", DisplayName: "Partners", ExternalID: "x-partners", Members: []string{"stranger"}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Share(ctx, "worker", "folder", tr.a.ID, "group", "partners", "editor"); err != nil {
		t.Fatal(err)
	}
	e, err := d.EditorSession(ctx, "stranger", tr.inB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if editable, err := drive.QueryInt(ctx, d, `SELECT editable FROM drive_editor_sessions WHERE id=?`, e.ID); err != nil || editable != 1 {
		t.Fatalf("editor through a folder share not editable: %d %v", editable, err)
	}
	if _, err = d.PendingRevocations(ctx); err != nil || revoked(t, d, e.ID) {
		t.Fatalf("live shared editor revoked: %v", err)
	}
	if err = st.Groups().ReplaceGroup(ctx, &store.Group{ID: "partners", DisplayName: "Partners", ExternalID: "x-partners"}, false); err != nil {
		t.Fatal(err)
	}
	if _, err = d.PendingRevocations(ctx); err != nil || !revoked(t, d, e.ID) {
		t.Fatalf("editor kept after leaving the group: %v", err)
	}
}

func TestSharedWithMe(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	tr := layout(t, d, w, blobs)
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "partners", DisplayName: "Partners", ExternalID: "x-partners", Members: []string{"stranger"}}, true); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct{ kind, id, pkind, pid, role string }{
		{"folder", tr.a.ID, "user", "stranger", "reader"},
		{"folder", tr.a.ID, "group", "partners", "editor"}, // the higher role wins
		{"file", tr.inC.ID, "user", "stranger", "reader"},
		{"file", tr.rootFile, "user", "stranger", "reader"},
		{"file", tr.inA.ID, "user", "worker", "reader"}, // worker is a member: not "shared with me"
	} {
		if _, err := d.Share(ctx, "worker", s.kind, s.id, s.pkind, s.pid, s.role); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.SetTrash(ctx, "worker", tr.rootFile, 1, true); err != nil {
		t.Fatal(err)
	}
	items, err := d.SharedWithMe(ctx, "stranger")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, i := range items {
		got = append(got, fmt.Sprintf("%s:%s:%s:%s", i.Kind, i.Name, i.Role, i.WorkspaceName))
	}
	want := fmt.Sprint([]string{"folder:a:editor:Organization", "file:inC.txt:reader:Organization"})
	if fmt.Sprint(got) != want {
		t.Fatalf("got %v want %v", got, want)
	}
	if items, _ := d.SharedWithMe(ctx, "worker"); len(items) != 0 {
		t.Fatalf("member sees own workspace as shared: %+v", items)
	}
}

func TestPeopleSearch(t *testing.T) {
	st, _, _ := fixture(t)
	d, ctx := st.Drive(), context.Background()
	for _, u := range []*store.User{
		{ID: "ann", Username: "ann.lee", DisplayName: "Ann Lee", Email: "ann@local.test", Role: "user", Status: "active", SSOProvider: "scim"},
		{ID: "anna", Username: "anna", DisplayName: "", Email: "anna@local.test", Role: "user", Status: "active", SSOProvider: "scim"},
		{ID: "andy", Username: "andy", DisplayName: "Andy Gone", Email: "andy@local.test", Role: "user", Status: "inactive", SSOProvider: "scim"},
	} {
		if err := st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	people, err := d.People(ctx, "worker", "ann") // "an" would also match "stranger"
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(people) != fmt.Sprint([]drive.Person{{ID: "ann", Name: "Ann Lee", Username: "ann.lee"}, {ID: "anna", Name: "anna", Username: "anna"}}) {
		t.Fatalf("people %+v", people)
	}
	for _, q := range []string{"a", "%%", "__", "andy"} { // andy is inactive
		if got, err := d.People(ctx, "worker", q); q == "a" && !errors.Is(err, drive.ErrInvalid) || q != "a" && (err != nil || len(got) != 0) {
			t.Errorf("%q: %+v %v", q, got, err)
		}
	}
	if _, err := d.People(drive.WithServiceAccess(ctx, drive.ServiceAccess{Rank: 1}), "worker", "ann"); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("service token searched people: %v", err)
	}
}
