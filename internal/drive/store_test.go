package drive_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

func fixture(t *testing.T) (store.Store, drive.Workspace, string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if st.Drive() == nil {
		t.Skip("drive is SQLite-only")
	}
	for _, u := range []*store.User{{ID: "admin", Username: "admin", Email: "admin@local.test", Role: "admin", Status: "active", SSOProvider: "local"}, {ID: "worker", Username: "worker", Email: "worker@local.test", Role: "user", Status: "active", SSOProvider: "scim"}, {ID: "stranger", Username: "stranger", Email: "stranger@local.test", Role: "user", Status: "active", SSOProvider: "scim"}} {
		if err = st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	g := &store.Group{ID: "team", DisplayName: "Team", ExternalID: "external-team", Members: []string{"worker"}}
	if err = st.Groups().ReplaceGroup(ctx, g, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "admin", "Organization", 100)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "admin", w.ID, "team", "editor"); err != nil {
		t.Fatal(err)
	}
	return st, w, t.TempDir()
}
func blob(t *testing.T, root, body string) drive.Version {
	t.Helper()
	v, e := drive.WriteBlob(root, strings.NewReader(body), 1024)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func TestConcurrentPublicationPermissionsAndOffboarding(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "notes.txt"}, blob(t, root, "original"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.File(ctx, "admin", f.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatalf("admin bypass: %v", err)
	}
	if _, err = d.File(ctx, "stranger", f.ID, 1); !errors.Is(err, drive.ErrDenied) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, s := range []string{"save a", "save b"} {
		v := blob(t, root, s)
		wg.Go(func() { _, e := d.Publish(ctx, "worker", f, v, 1); results <- e })
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, drive.ErrConflict) {
			conflict++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("success %d conflict %d", success, conflict)
	}
	session, err := d.EditorSession(ctx, "worker", f.ID, "randomtoken")
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Users().DeleteUser(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.EditorDownload(ctx, session.ID, "randomtoken"); !errors.Is(err, drive.ErrDenied) {
		t.Fatal(err)
	}
	pending, err := d.PendingRevocations(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("deleted user pending: %v %v", pending, err)
	}
	// Workspace data and immutable history survive employee deletion.
	users, _, err := st.Users().ListUsers(ctx, 0, 100, "")
	if err != nil || len(users) != 2 {
		t.Fatal(err)
	}
	g, _ := st.Groups().GetGroupByID(ctx, "team")
	g.Members = []string{"stranger"}
	if err = st.Groups().ReplaceGroup(ctx, g, false); err != nil {
		t.Fatal(err)
	}
	versions, err := d.Versions(ctx, "stranger", f.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("history %v %v", versions, err)
	}
	for _, v := range versions {
		p, _ := drive.BlobPath(root, v.Blob)
		if _, err = os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
}
func TestEditorForceSaveRetriesAndCompetingUpload(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "report.docx"}, blob(t, root, "base"), 0)
	if err != nil {
		t.Fatal(err)
	}
	e, err := d.EditorSession(ctx, "worker", f.ID, "token")
	if err != nil {
		t.Fatal(err)
	}
	v := blob(t, root, "first edit")
	duplicate, err := d.PublishEditor(ctx, "worker", f, v, e.Key, false)
	if err != nil || duplicate {
		t.Fatal(err)
	}
	duplicate, err = d.PublishEditor(ctx, "worker", f, v, e.Key, false)
	if err != nil || !duplicate {
		t.Fatalf("retry: %v %v", duplicate, err)
	}
	_, err = d.PublishEditor(ctx, "worker", f, blob(t, root, "second edit"), e.Key, false)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := d.File(ctx, "worker", f.ID, 2)
	if current.Revision != 3 {
		t.Fatal(current)
	}
	_, err = d.Publish(ctx, "worker", current, blob(t, root, "outside update"), 3)
	if err != nil {
		t.Fatal(err)
	}
	_, err = d.PublishEditor(ctx, "worker", f, blob(t, root, "stale editor save"), e.Key, true)
	if !errors.Is(err, drive.ErrConflict) {
		t.Fatalf("silent stale save: %v", err)
	}
}
func TestQuotaTrashAndAtomicGroups(t *testing.T) {
	st, w, root := fixture(t)
	ctx := context.Background()
	d := st.Drive()
	f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "../escape"}, blob(t, root, "x"), 0)
	if !errors.Is(err, drive.ErrInvalid) {
		t.Fatal(err)
	}
	f, err = d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "notes.txt"}, blob(t, root, "12345678"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Quota(ctx, "admin", w.ID, 10); err != nil {
		t.Fatal(err)
	}
	_, err = d.Publish(ctx, "worker", f, blob(t, root, "123"), 1)
	if !errors.Is(err, drive.ErrQuota) {
		t.Fatal(err)
	}
	if err = d.SetTrash(ctx, "worker", f.ID, 1, true); err != nil {
		t.Fatal(err)
	}
	trash, err := d.Files(ctx, "worker", w.ID, true)
	if err != nil || len(trash) != 1 {
		t.Fatal(err)
	}
	if err = d.SetTrash(ctx, "worker", f.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	g, _ := st.Groups().GetGroupByID(ctx, "team")
	g.Members = []string{"missing"}
	if err = st.Groups().ReplaceGroup(ctx, g, false); err == nil {
		t.Fatal("missing member accepted")
	}
	if err = d.Authorize(ctx, "worker", w.ID, 2); err != nil {
		t.Fatal("failed replacement removed prior membership", err)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("simulated interrupted upload") }
func TestInterruptedAndOversizedBlobsNeverPublish(t *testing.T) {
	root := t.TempDir()
	for _, reader := range []struct {
		body  interface{ Read([]byte) (int, error) }
		limit int64
	}{{brokenReader{}, 8}, {strings.NewReader("too large"), 2}} {
		if _, err := drive.WriteBlob(root, reader.body, reader.limit); err == nil {
			t.Fatal("failed stream accepted")
		}
		files, err := os.ReadDir(root)
		if err != nil || len(files) != 0 {
			t.Fatal("failed stream left a published blob", files, err)
		}
	}
	for _, id := range []string{"../escape", "urn:uuid:00000000-0000-0000-0000-000000000000", "bad"} {
		if _, err := drive.BlobPath(root, id); err == nil {
			t.Fatal("unsafe blob identifier accepted")
		}
	}
}
