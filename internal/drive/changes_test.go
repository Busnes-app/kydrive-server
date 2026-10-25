package drive_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

type changeEnv struct {
	st       store.Store
	d        *drive.Store
	ctx      context.Context
	w, other string
	blobs    string
}

func newChangeEnv(t *testing.T) *changeEnv {
	t.Helper()
	st, w, blobs := fixture(t)
	e := &changeEnv{st: st, d: st.Drive(), ctx: context.Background(), w: w.ID, blobs: blobs}
	managers(t, e.d, w)
	other, err := e.d.CreateWorkspace(e.ctx, "admin", "Other", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.d.Grant(e.ctx, "admin", other.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
	e.other = other.ID
	return e
}

func (e *changeEnv) file(t *testing.T, name string) drive.File {
	t.Helper()
	f, err := e.d.Publish(e.ctx, "worker", drive.File{Workspace: e.w, Name: name}, blob(t, e.blobs, name), 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (e *changeEnv) folder(t *testing.T, workspace, user, name string) drive.Folder {
	t.Helper()
	f, err := e.d.AddFolder(e.ctx, user, workspace, "", name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func lastChange(t *testing.T, d *drive.Store) int64 {
	t.Helper()
	n, err := drive.QueryInt(context.Background(), d, `SELECT COALESCE(MAX(seq),0) FROM drive_changes`)
	if err != nil {
		t.Fatal(err)
	}
	return int64(n)
}

func row(workspace, kind, item string) string { return workspace + " " + kind + " " + item }

// A mutator case sets up, then returns the mutation, which returns the rows it must write.
type mutatorCase struct {
	name string // Store method, optionally followed by "/variant"
	act  func(t *testing.T, e *changeEnv) func() ([]string, error)
}

var mutators = []mutatorCase{
	{"Publish", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		return func() ([]string, error) {
			f, err := e.d.Publish(e.ctx, "worker", drive.File{Workspace: e.w, Name: "a.txt"}, blob(t, e.blobs, "a"), 0)
			return []string{row(e.w, "file", f.ID)}, err
		}
	}},
	{"Publish/version", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.file(t, "a.txt")
		return func() ([]string, error) {
			_, err := e.d.Publish(e.ctx, "worker", f, blob(t, e.blobs, "b"), 1)
			return []string{row(e.w, "file", f.ID)}, err
		}
	}},
	{"RestoreVersion", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.file(t, "a.txt")
		if _, err := e.d.Publish(e.ctx, "worker", f, blob(t, e.blobs, "b"), 1); err != nil {
			t.Fatal(err)
		}
		return func() ([]string, error) {
			_, err := e.d.RestoreVersion(e.ctx, "worker", f.ID, 1, 2)
			return []string{row(e.w, "file", f.ID)}, err
		}
	}},
	{"CopyFile", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.file(t, "a.txt")
		return func() ([]string, error) {
			c, err := e.d.CopyFile(e.ctx, "worker", f.ID, drive.Location{Workspace: e.other, Name: "a.txt"})
			return []string{row(e.other, "file", c.ID)}, err
		}
	}},
	{"PublishEditor", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.file(t, "a.docx")
		s, err := e.d.EditorSession(e.ctx, "worker", f.ID)
		if err != nil {
			t.Fatal(err)
		}
		return func() ([]string, error) {
			_, err := e.d.PublishEditor(e.ctx, "worker", f, blob(t, e.blobs, "saved"), s.Key, false)
			return []string{row(e.w, "file", f.ID)}, err
		}
	}},
	{"GuestUpload", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		in := e.folder(t, e.w, "worker", "Inbox")
		_, token, err := e.d.CreateGuestLink(e.ctx, "worker", "folder", in.ID, inDays(1), "", true, week)
		if err != nil {
			t.Fatal(err)
		}
		return func() ([]string, error) {
			f, err := e.d.GuestUpload(e.ctx, token, "", "x.txt", blob(t, e.blobs, "x"))
			return []string{row(e.w, "file", f.ID)}, err
		}
	}},
	{"CompleteUpload", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		staging := t.TempDir()
		u := startUpload(t, e.d, "worker", drive.UploadSession{Workspace: e.w, Name: "big.bin", Size: 3})
		if _, err := appendChunk(e.d, staging, "worker", u.ID, 0, "abc"); err != nil {
			t.Fatal(err)
		}
		return func() ([]string, error) {
			f, err := e.d.CompleteUpload(e.ctx, staging, e.blobs, testKey, "worker", u.ID)
			return []string{row(e.w, "file", f.ID)}, err
		}
	}},
	{"AddFolder", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		return func() ([]string, error) {
			f, err := e.d.AddFolder(e.ctx, "worker", e.w, "", "Docs")
			return []string{row(e.w, "folder", f.ID)}, err
		}
	}},
	{"MoveFile", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.file(t, "a.txt")
		return func() ([]string, error) {
			_, err := e.d.MoveFile(e.ctx, "worker", f.ID, 1, drive.Location{Workspace: e.w, Name: "b.txt"})
			return []string{row(e.w, "file", f.ID)}, err
		}
	}},
	{"MoveFile/cross-workspace", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.file(t, "a.txt")
		return func() ([]string, error) {
			_, err := e.d.MoveFile(e.ctx, "worker", f.ID, 1, drive.Location{Workspace: e.other, Name: "a.txt"})
			return []string{row(e.w, "file", f.ID), row(e.other, "file", f.ID)}, err
		}
	}},
	{"MoveFolder", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.folder(t, e.w, "worker", "Docs")
		return func() ([]string, error) {
			_, err := e.d.MoveFolder(e.ctx, "worker", f.ID, "", "Papers")
			return []string{row(e.w, "folder", f.ID)}, err
		}
	}},
	{"SetTrash", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.file(t, "a.txt")
		return func() ([]string, error) {
			return []string{row(e.w, "file", f.ID)}, e.d.SetTrash(e.ctx, "worker", f.ID, 1, true)
		}
	}},
	{"SetTrash/restore", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.file(t, "a.txt")
		if err := e.d.SetTrash(e.ctx, "worker", f.ID, 1, true); err != nil {
			t.Fatal(err)
		}
		return func() ([]string, error) {
			return []string{row(e.w, "file", f.ID)}, e.d.SetTrash(e.ctx, "worker", f.ID, 1, false)
		}
	}},
	{"SetFolderTrash", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.folder(t, e.w, "worker", "Docs")
		if _, err := e.d.Publish(e.ctx, "worker", drive.File{Workspace: e.w, Parent: f.ID, Name: "in.txt"}, blob(t, e.blobs, "in"), 0); err != nil {
			t.Fatal(err)
		}
		return func() ([]string, error) {
			return []string{row(e.w, "folder", f.ID)}, e.d.SetFolderTrash(e.ctx, "worker", f.ID, true)
		}
	}},
	{"SetFolderTrash/restore", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		f := e.folder(t, e.w, "worker", "Docs")
		if err := e.d.SetFolderTrash(e.ctx, "worker", f.ID, true); err != nil {
			t.Fatal(err)
		}
		return func() ([]string, error) {
			return []string{row(e.w, "folder", f.ID)}, e.d.SetFolderTrash(e.ctx, "worker", f.ID, false)
		}
	}},
	{"Handover", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		src, top := leaver(t, e)
		return func() ([]string, error) {
			folder, err := e.d.Handover(e.ctx, "admin", src, drive.Target{Workspace: e.w})
			return append(top, row(e.w, "folder", folder)), err
		}
	}},
	{"OperatorHandover", func(t *testing.T, e *changeEnv) func() ([]string, error) {
		src, top := leaver(t, e)
		return func() ([]string, error) {
			folder, err := e.d.OperatorHandover(e.ctx, src, drive.Target{User: "worker"})
			if err != nil {
				return nil, err
			}
			mine, err := e.d.EnsurePersonalWorkspace(e.ctx, "worker")
			return append(top, row(mine, "folder", folder)), err
		}
	}},
}

// leaver makes an inactive account's My files with a folder, a nested file and a trashed root
// file, and returns it with the source rows a handover must write: one per top-level item.
func leaver(t *testing.T, e *changeEnv) (string, []string) {
	t.Helper()
	if err := e.st.Users().CreateUser(e.ctx, &store.User{ID: "leaver", Username: "leaver", Email: "leaver@local.test", Role: "user", Status: "active", SSOProvider: "scim"}); err != nil {
		t.Fatal(err)
	}
	src, err := e.d.EnsurePersonalWorkspace(e.ctx, "leaver")
	if err != nil {
		t.Fatal(err)
	}
	docs := e.folder(t, src, "leaver", "Docs")
	if _, err = e.d.Publish(e.ctx, "leaver", drive.File{Workspace: src, Parent: docs.ID, Name: "in.txt"}, blob(t, e.blobs, "in"), 0); err != nil {
		t.Fatal(err)
	}
	old, err := e.d.Publish(e.ctx, "leaver", drive.File{Workspace: src, Name: "old.txt"}, blob(t, e.blobs, "old"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.d.SetTrash(e.ctx, "leaver", old.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	if err = drive.ExecSQL(e.ctx, e.d, `UPDATE users SET status='inactive' WHERE id='leaver'`); err != nil {
		t.Fatal(err)
	}
	return src, []string{row(src, "folder", docs.ID), row(src, "file", old.ID)}
}

// noChange lists every exported Store method that writes no change row, and why. A new method
// fails TestEveryMutatorWritesAChange until it is classified here or added to mutators.
var noChange = map[string]string{
	"AppendUpload":            "stages bytes; the item appears on CompleteUpload",
	"ApplyRetention":          "purges trashed items and old versions only (Decision 4)",
	"AgentTokenUser":          "credential bookkeeping",
	"AppPasswordUser":         "credential bookkeeping",
	"AppPasswords":            "read",
	"ApproveLink":             "device credential bookkeeping",
	"Authorize":               "read",
	"AuthorizeItem":           "read",
	"AuthorizeSharedManager":  "read",
	"CallbackActor":           "read",
	"CancelUpload":            "staging only",
	"Changes":                 "read",
	"Children":                "read",
	"CloseEditor":             "editor bookkeeping",
	"Colleagues":              "read",
	"CreateAgentToken":        "credential",
	"CreateAppPassword":       "credential",
	"CreateGuestLink":         "links never change items (Decision 4)",
	"CreateLink":              "links never change items (Decision 4)",
	"CreateServiceToken":      "credential",
	"CreateWorkspace":         "workspace lists carry access (Decision 1)",
	"DeferRevocation":         "editor bookkeeping",
	"DeviceUser":              "credential check",
	"Devices":                 "read",
	"EditorDownload":          "read",
	"EditorSession":           "editor bookkeeping",
	"EnsurePersonalWorkspace": "workspace lists carry access (Decision 1)",
	"Events":                  "read",
	"ExpireAgentTokens":       "credential bookkeeping",
	"ExpireUploads":           "staging only",
	"File":                    "read",
	"Files":                   "read",
	"FolderChildren":          "read",
	"FolderPath":              "read",
	"FolderTree":              "read",
	"Folders":                 "read",
	"Grant":                   "grants change access, not items (Decision 1)",
	"Grants":                  "read",
	"GuestFile":               "read",
	"GuestLinks":              "read",
	"GuestView":               "read",
	"Leavers":                 "read",
	"Links":                   "read",
	"Lookup":                  "read",
	"MarkDropped":             "editor bookkeeping",
	"OpenLink":                "links never change items (Decision 4)",
	"OperatorLeavers":         "read",
	"PendingRevocations":      "editor bookkeeping",
	"People":                  "read",
	"PruneChanges":            "the change log itself",
	"PruneDevices":            "device credential bookkeeping",
	"PurgeFile":               "trashed items only (Decision 4)",
	"PurgeFolder":             "trashed items only (Decision 4)",
	"PurgeVersion":            "non-current versions only; current state is unchanged",
	"Quota":                   "workspace setting",
	"RedeemLink":              "device credential bookkeeping",
	"RevocationStatus":        "read",
	"RevokeAppPassword":       "credential",
	"RevokeDevice":            "device credential bookkeeping",
	"RevokeLink":              "links never change items (Decision 4)",
	"RevokeServiceToken":      "credential",
	"RevokeUserEditors":       "editor bookkeeping",
	"SealLegacyBlobs":         "same bytes under a new blob ID; digest and revision unchanged",
	"UpgradeBlobs":            "same bytes and blob ID, new sealing format",
	"IndexChanges":            "read",
	"IndexFile":               "read",
	"IndexFiles":              "read",
	"Search":                  "read",
	"SearchContent":           "read",
	"SearchFolder":            "read",
	"SearchFolderContent":     "read",
	"ServiceToken":            "read",
	"SetAgentScope":           "credential",
	"SetEditorsShare":         "workspace setting",
	"SetRetention":            "workspace setting",
	"Share":                   "shares only add access inside a readable workspace (Decision 1)",
	"SharedWithMe":            "read",
	"Snapshot":                "read",
	"Shares":                  "read",
	"StartUpload":             "staging only",
	"UnlinkDevice":            "device credential bookkeeping",
	"Unshare":                 "shares only add access inside a readable workspace (Decision 1)",
	"SweepOrphans":            "unreferenced blobs only",
	"Upload":                  "read",
	"Versions":                "read",
	"Workspaces":              "read",
}

func TestEveryMutatorWritesAChange(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range mutators {
		covered[strings.Split(c.name, "/")[0]] = true
		t.Run(c.name, func(t *testing.T) {
			e := newChangeEnv(t)
			mutate := c.act(t, e)
			mark := lastChange(t, e.d)
			want, err := mutate()
			if err != nil {
				t.Fatal(err)
			}
			got, err := drive.ChangesAfter(e.ctx, e.d, mark)
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("rows %q, want %q", got, want)
			}
		})
	}
	store := reflect.TypeFor[*drive.Store]()
	for i := range store.NumMethod() {
		name := store.Method(i).Name
		if _, ok := noChange[name]; covered[name] == ok {
			t.Errorf("%s: classify it in exactly one of mutators and noChange", name)
		}
	}
}

// The change row commits with its mutation or not at all.
func TestRolledBackMutationLeavesNoChange(t *testing.T) {
	e := newChangeEnv(t)
	if err := drive.ExecSQL(e.ctx, e.d, `CREATE TRIGGER fail_events BEFORE INSERT ON drive_events BEGIN SELECT RAISE(ABORT,'boom'); END`); err != nil {
		t.Fatal(err)
	}
	mark := lastChange(t, e.d)
	if _, err := e.d.Publish(e.ctx, "worker", drive.File{Workspace: e.w, Name: "a.txt"}, blob(t, e.blobs, "a"), 0); err == nil {
		t.Fatal("publish succeeded without its event")
	}
	if got, err := drive.ChangesAfter(e.ctx, e.d, mark); err != nil || len(got) != 0 {
		t.Fatalf("rolled-back rows %q %v", got, err)
	}
}

func TestChangeSeqIncreasesAcrossConcurrentMutations(t *testing.T) {
	e := newChangeEnv(t)
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		v := blob(t, e.blobs, fmt.Sprint(i))
		wg.Go(func() {
			_, err := e.d.Publish(e.ctx, "worker", drive.File{Workspace: e.w, Name: fmt.Sprintf("%d.txt", i)}, v, 0)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seqs, err := drive.ChangeSeqs(e.ctx, e.d)
	if err != nil || len(seqs) != n {
		t.Fatalf("seqs %v %v", seqs, err)
	}
	for i := 1; i < n; i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("seq not strictly increasing in commit order: %v", seqs)
		}
	}
}

// Pruning drops old rows but keeps each workspace's newest, so an idle workspace's cursor lives.
func TestPruneChangesKeepsNewestPerWorkspace(t *testing.T) {
	e := newChangeEnv(t)
	a := e.file(t, "a.txt")
	b := e.file(t, "b.txt")
	idle := e.folder(t, e.other, "worker", "Idle")
	if err := drive.ExecSQL(e.ctx, e.d, `UPDATE drive_changes SET created=0`); err != nil {
		t.Fatal(err)
	}
	fresh := e.file(t, "fresh.txt")
	if err := e.d.PruneChanges(e.ctx, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := drive.ChangesAfter(e.ctx, e.d, 0)
	want := []string{row(e.other, "folder", idle.ID), row(e.w, "file", fresh.ID)}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("after prune %q %v, want %q (dropped %s, %s)", got, err, want, a.ID, b.ID)
	}
}

func changes(t *testing.T, e *changeEnv, workspace string, after int64) ([]drive.Change, int64) {
	t.Helper()
	cs, next, more, err := e.d.Changes(e.ctx, "worker", workspace, after, 1000)
	if err != nil || more {
		t.Fatalf("changes after %d: more=%v %v", after, more, err)
	}
	return cs, next
}

// Each changed item comes back once, as it is now.
func TestChangesReturnCurrentState(t *testing.T) {
	e := newChangeEnv(t)
	f := e.file(t, "a.txt")
	f, err := e.d.Publish(e.ctx, "worker", f, blob(t, e.blobs, "two"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.d.MoveFile(e.ctx, "worker", f.ID, 2, drive.Location{Workspace: e.w, Name: "b.txt"}); err != nil {
		t.Fatal(err)
	}
	dir := e.folder(t, e.w, "worker", "Docs")
	cs, next := changes(t, e, e.w, 0)
	if len(cs) != 2 || cs[0].Kind != "file" || cs[0].File == nil || cs[0].File.Name != "b.txt" || cs[0].File.Revision != 2 || cs[1].Folder == nil || cs[1].Folder.Name != "Docs" {
		t.Fatalf("changes %+v", cs)
	}
	if again, n := changes(t, e, e.w, next); len(again) != 0 || n != next {
		t.Fatalf("after next %+v %d", again, n)
	}

	// A file moved out is removed here and live there; a restored folder is live again.
	if _, err = e.d.MoveFile(e.ctx, "worker", f.ID, 2, drive.Location{Workspace: e.other, Name: "b.txt"}); err != nil {
		t.Fatal(err)
	}
	if err = e.d.SetFolderTrash(e.ctx, "worker", dir.ID, true); err != nil {
		t.Fatal(err)
	}
	cs, next = changes(t, e, e.w, next)
	if len(cs) != 2 || !cs[0].Removed || cs[0].ID != f.ID || cs[0].File != nil || !cs[1].Removed || cs[1].ID != dir.ID {
		t.Fatalf("removals %+v", cs)
	}
	if there, _ := changes(t, e, e.other, 0); len(there) != 1 || there[0].File == nil || there[0].File.Workspace != e.other {
		t.Fatalf("destination %+v", there)
	}
	if err = e.d.SetFolderTrash(e.ctx, "worker", dir.ID, false); err != nil {
		t.Fatal(err)
	}
	if cs, _ = changes(t, e, e.w, next); len(cs) != 1 || cs[0].Removed || cs[0].Folder == nil || cs[0].Folder.Name != "Docs" {
		t.Fatalf("restore %+v", cs)
	}
}

func TestChangesPage(t *testing.T) {
	e := newChangeEnv(t)
	for i := range 3 {
		e.file(t, fmt.Sprintf("%d.txt", i))
	}
	cs, next, more, err := e.d.Changes(e.ctx, "worker", e.w, 0, 2)
	if err != nil || len(cs) != 2 || !more {
		t.Fatalf("first page %d more=%v %v", len(cs), more, err)
	}
	if cs, _, more, err = e.d.Changes(e.ctx, "worker", e.w, next, 2); err != nil || len(cs) != 1 || more {
		t.Fatalf("last page %d more=%v %v", len(cs), more, err)
	}
	if _, _, _, err = e.d.Changes(e.ctx, "worker", e.w, 0, 0); !errors.Is(err, drive.ErrInvalid) {
		t.Fatalf("limit 0: %v", err)
	}
}

// A cursor that missed pruned rows is gone; a head cursor, an idle workspace's and a new
// workspace's are not.
func TestChangesGone(t *testing.T) {
	e := newChangeEnv(t)
	e.file(t, "a.txt")
	e.file(t, "b.txt")
	e.folder(t, e.other, "worker", "Idle")
	fresh, err := e.d.CreateWorkspace(e.ctx, "admin", "Fresh", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.d.Grant(e.ctx, "admin", fresh.ID, "team", "editor"); err != nil {
		t.Fatal(err)
	}
	_, idleHead := changes(t, e, e.other, 0)
	_, head := changes(t, e, e.w, 0)
	if err = drive.ExecSQL(e.ctx, e.d, `UPDATE drive_changes SET created=0`); err != nil {
		t.Fatal(err)
	}
	if err = e.d.PruneChanges(e.ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = e.d.Changes(e.ctx, "worker", e.w, 0, 10); !errors.Is(err, drive.ErrGone) {
		t.Fatalf("pruned cursor: %v", err)
	}
	changes(t, e, e.w, head)
	changes(t, e, e.other, idleHead)
	if _, err = e.d.AddFolder(e.ctx, "worker", fresh.ID, "", "New"); err != nil {
		t.Fatal(err)
	}
	if cs, _ := changes(t, e, fresh.ID, 0); len(cs) != 1 {
		t.Fatalf("new workspace %+v", cs)
	}
}

// A client that pages a snapshot while others write, then follows changes from the first
// page's head, ends with exactly the workspace's live items.
func TestSnapshotThenChangesMissesNothing(t *testing.T) {
	e := newChangeEnv(t)
	for _, w := range []string{e.w, e.other} {
		if err := e.d.Quota(e.ctx, "admin", w, 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	type item struct{ name, parent string }
	var seeds []drive.File
	var bodies []drive.Version
	for i := range 12 {
		seeds = append(seeds, e.file(t, fmt.Sprintf("seed-%d.txt", i)))
		bodies = append(bodies, blob(t, e.blobs, fmt.Sprint("new ", i)))
	}
	dir := e.folder(t, e.w, "worker", "Docs")
	// Two writes land after every page: creations, moves within and out, and trash, on items
	// the snapshot has and has not reached yet.
	write := func(i int) {
		f := seeds[i]
		var err error
		switch i % 4 {
		case 0:
			_, err = e.d.Publish(e.ctx, "worker", drive.File{Workspace: e.w, Parent: dir.ID, Name: fmt.Sprintf("new-%d.txt", i)}, bodies[i], 0)
		case 1:
			_, err = e.d.MoveFile(e.ctx, "worker", f.ID, 1, drive.Location{Workspace: e.w, Parent: dir.ID, Name: fmt.Sprintf("moved-%d.txt", i)})
		case 2:
			err = e.d.SetTrash(e.ctx, "worker", f.ID, 1, true)
		case 3:
			_, err = e.d.MoveFile(e.ctx, "worker", f.ID, 1, drive.Location{Workspace: e.other, Name: f.Name})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	local := map[string]item{}
	var head int64
	after, written := "", 0
	for page := 0; ; page++ {
		folders, files, h, next, err := e.d.Snapshot(e.ctx, "worker", e.w, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if page == 0 {
			head = h
		}
		for _, f := range folders {
			local["folder "+f.ID] = item{f.Name, f.Parent}
		}
		for _, f := range files {
			local["file "+f.ID] = item{f.Name, f.Parent}
		}
		if next == "" {
			break
		}
		after = next
		for ; written < len(seeds) && written < 2*(page+1); written++ {
			write(written)
		}
	}
	if written < 8 {
		t.Fatalf("only %d writes landed between pages", written)
	}
	for ; written < len(seeds); written++ {
		write(written)
	}
	for more := true; more; {
		var cs []drive.Change
		var err error
		if cs, head, more, err = e.d.Changes(e.ctx, "worker", e.w, head, 3); err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			switch {
			case c.Removed:
				delete(local, c.Kind+" "+c.ID)
			case c.File != nil:
				local["file "+c.ID] = item{c.File.Name, c.File.Parent}
			default:
				local["folder "+c.ID] = item{c.Folder.Name, c.Folder.Parent}
			}
		}
	}
	want := map[string]item{}
	folders, _ := e.d.Folders(e.ctx, "worker", e.w, false)
	for _, f := range folders {
		want["folder "+f.ID] = item{f.Name, f.Parent}
	}
	files, _ := e.d.Files(e.ctx, "worker", e.w, false)
	for _, f := range files {
		want["file "+f.ID] = item{f.Name, f.Parent}
	}
	// Docs, 3 untouched seeds, 3 moved in, 3 new.
	if len(want) != 10 || !reflect.DeepEqual(local, want) {
		t.Fatalf("client has %v\nworkspace has %v", local, want)
	}
}
