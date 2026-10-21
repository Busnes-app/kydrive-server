package drive_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// blobMatcher stands in for the index: a file matches when its current blob is listed. It
// records every file it was offered, which must be exactly the caller's readable live files.
type blobMatcher struct {
	mu      sync.Mutex
	blobs   map[string]float64
	offered []string
}

func (m *blobMatcher) Match(_ context.Context, _ string, files []drive.File) (map[string]float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.offered = m.offered[:0]
	out := map[string]float64{}
	for _, f := range files {
		m.offered = append(m.offered, f.ID)
		if s, ok := m.blobs[f.Blob]; ok {
			out[f.ID] = s
		}
	}
	return out, nil
}

func (m *blobMatcher) saw(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Contains(m.offered, id)
}

// The mandatory cross-user test at the drive level: content matching is offered only the files
// the caller can read in their workspaces, so a file in someone else's My files never matches,
// even when a readable copy shares its blob; shares stay out of search (workspace scope only).
func TestSearchContentOffersOnlyReadableFiles(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	mine, err := d.EnsurePersonalWorkspace(ctx, "stranger")
	if err != nil {
		t.Fatal(err)
	}
	diary, err := d.Publish(ctx, "stranger", drive.File{Workspace: mine, Name: "diary.txt"}, blob(t, root, "zebracanary"), 0)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Name: "plan.txt"}, blob(t, root, "plan"), 0)
	if err != nil {
		t.Fatal(err)
	}
	m := &blobMatcher{blobs: map[string]float64{diary.Blob: 2, plan.Blob: 1}}
	search := func(ctx context.Context, user string) []string {
		t.Helper()
		m.offered = nil
		hits, _, err := d.SearchContent(ctx, user, "zebracanary", 200, m)
		if err != nil {
			t.Fatal(err)
		}
		out := []string{}
		for _, h := range hits {
			out = append(out, fmt.Sprintf("%s %v", h.ID, h.Match))
		}
		return out
	}
	if hits := search(ctx, "worker"); fmt.Sprint(hits) != fmt.Sprint([]string{plan.ID + " [content]"}) || m.saw(diary.ID) {
		t.Fatalf("worker: %v, offered the diary: %v", hits, m.saw(diary.ID))
	}
	if hits := search(ctx, "admin"); len(hits) != 0 || len(m.offered) != 0 {
		t.Fatalf("admin bypass: %v offered %v", hits, m.offered)
	}
	if hits := search(ctx, "stranger"); fmt.Sprint(hits) != fmt.Sprint([]string{diary.ID + " [content]"}) {
		t.Fatalf("owner: %v", hits)
	}

	// Shared with the worker, the diary is readable to them but not searched (workspace scope).
	if _, err = d.Share(ctx, "stranger", "file", diary.ID, "user", "worker", "reader"); err != nil {
		t.Fatal(err)
	}
	if _, err = d.File(ctx, "worker", diary.ID, 1); err != nil {
		t.Fatalf("share not readable: %v", err)
	}
	if search(ctx, "worker"); m.saw(diary.ID) {
		t.Fatal("a share-only file was offered to content search")
	}

	// A copy in the shared workspace shares the diary's blob: the worker finds the copy only.
	if err = st.Groups().ReplaceGroup(ctx, &store.Group{ID: "late", DisplayName: "Late", ExternalID: "external-late", Members: []string{"stranger"}}, true); err != nil {
		t.Fatal(err)
	}
	if err = d.Grant(ctx, "admin", w.ID, "late", "editor"); err != nil {
		t.Fatal(err)
	}
	copied, err := d.CopyFile(ctx, "stranger", diary.ID, drive.Location{Workspace: w.ID, Name: "copy.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if hits := search(ctx, "worker"); fmt.Sprint(hits) != fmt.Sprint([]string{copied.ID + " [content]", plan.ID + " [content]"}) || m.saw(diary.ID) {
		t.Fatalf("worker after the copy: %v", hits)
	}

	// Trash takes the copy out; a service token sees only its workspace.
	if err = d.SetTrash(ctx, "worker", copied.ID, copied.Revision, true); err != nil {
		t.Fatal(err)
	}
	if search(ctx, "worker"); m.saw(copied.ID) {
		t.Fatal("trashed file offered")
	}
	scoped := drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: mine, Rank: 1})
	if hits := search(scoped, "worker"); len(hits) != 0 || len(m.offered) != 0 {
		t.Fatalf("service token for another workspace: %v %v", hits, m.offered)
	}

	// Losing the workspace grant ends it at once.
	if err = st.Groups().ReplaceGroup(ctx, &store.Group{ID: "team", DisplayName: "Team", ExternalID: "external-team"}, false); err != nil {
		t.Fatal(err)
	}
	if hits := search(ctx, "worker"); len(hits) != 0 || m.saw(plan.ID) {
		t.Fatalf("removed member: %v", hits)
	}
	// A deactivated owner's My files is out of everyone's search, the owner's included.
	if err = drive.ExecSQL(ctx, d, `UPDATE users SET status='inactive' WHERE id='stranger'`); err != nil {
		t.Fatal(err)
	}
	if hits := search(ctx, "stranger"); len(hits) != 0 || m.saw(diary.ID) {
		t.Fatalf("inactive owner: %v", hits)
	}
}

// Name matches come first in name order, marked when the content matched too; content-only
// matches follow by score. A folder search offers only its subtree.
func TestSearchContentOrderAndFolderScope(t *testing.T) {
	st, w, root := fixture(t)
	d, ctx := st.Drive(), context.Background()
	sub, err := d.AddFolder(ctx, "worker", w.ID, "", "Sub")
	if err != nil {
		t.Fatal(err)
	}
	pub := func(parent, name, body string) drive.File {
		f, err := d.Publish(ctx, "worker", drive.File{Workspace: w.ID, Parent: parent, Name: name}, blob(t, root, body), 0)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	named := pub("", "budget notes.txt", "a")
	low := pub("", "x.txt", "b")
	high := pub(sub.ID, "y.txt", "c")
	other := pub("", "budget.txt", "d")
	m := &blobMatcher{blobs: map[string]float64{named.Blob: 1, low.Blob: 1, high.Blob: 5}}
	hits, _, err := d.SearchContent(ctx, "worker", "budget", 200, m)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range hits {
		got = append(got, fmt.Sprintf("%s %v", h.Name, h.Match))
	}
	want := []string{"budget notes.txt [name content]", "budget.txt [name]", "y.txt [content]", "x.txt [content]"}
	if fmt.Sprint(got) != fmt.Sprint(want) || other.ID == "" {
		t.Fatalf("got %v want %v", got, want)
	}
	hits, _, err = d.SearchFolderContent(ctx, "worker", w.ID, sub.ID, "budget", 200, m)
	if err != nil || len(hits) != 1 || hits[0].ID != high.ID || hits[0].Path != "" || m.saw(low.ID) {
		t.Fatalf("folder scope: %v %v offered %v", hits, err, m.offered)
	}
	// Without a matcher, the search is by name as before.
	if hits, _, _ = d.SearchContent(ctx, "worker", "budget", 200, nil); len(hits) != 2 {
		t.Fatalf("names only: %v", hitNames(hits))
	}
}
