package drive_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// everyMatcher matches every file it is offered, records them, and runs during first, once.
type everyMatcher struct {
	mu      sync.Mutex
	offered []string
	during  func()
}

func (m *everyMatcher) Match(_ context.Context, _ string, files []drive.File) (map[string]float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.offered = m.offered[:0]
	out := map[string]float64{}
	for _, f := range files {
		m.offered = append(m.offered, f.ID)
		out[f.ID] = 1
	}
	if m.during != nil {
		m.during()
		m.during = nil
	}
	return out, nil
}

// Search with shared covers the live items in Shared with me, checked at query time: each
// way access ends takes the item out of the next search, and nothing else is ever offered.
func TestSearchSharedWithMe(t *testing.T) {
	st, w, blobs := fixture(t)
	d, ctx := st.Drive(), context.Background()
	managers(t, d, w)
	tr := layout(t, d, w, blobs) // a/b/inB.txt, a/inA.txt, c/inC.txt, root.txt
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	share := func(kind, id, pkind, pid, role string) drive.Share {
		t.Helper()
		sh, err := d.Share(ctx, "worker", kind, id, pkind, pid, role)
		must(err)
		return sh
	}
	m := &everyMatcher{}
	// search lists "name path from match" for each hit, and what m was offered, by name.
	names := map[string]string{tr.a.ID: "a", tr.b.ID: "b", tr.c.ID: "c", tr.inA.ID: "inA.txt", tr.inB.ID: "inB.txt", tr.inC.ID: "inC.txt", tr.rootFile: "root.txt"}
	search := func(ctx context.Context, user, q, typ string, shared bool) (string, string) {
		t.Helper()
		m.offered = nil
		hits, _, err := d.SearchContent(ctx, user, q, typ, 200, m, shared)
		must(err)
		var out []string
		for _, h := range hits {
			from := "-"
			if h.Shared != nil {
				from = h.Shared.From + ":" + h.Shared.Role
			}
			out = append(out, fmt.Sprintf("%s %q %s %s", h.Name, h.Path, from, strings.Join(h.Match, "+")))
		}
		var offered []string
		for _, id := range m.offered {
			offered = append(offered, names[id])
		}
		slices.Sort(offered)
		return strings.Join(out, "; "), strings.Join(offered, ",")
	}
	expect := func(ctx context.Context, user, q, typ, hits, offered string) {
		t.Helper()
		if h, o := search(ctx, user, q, typ, true); h != hits || o != offered {
			t.Fatalf("%s %q %q:\n hits    %s\n want    %s\n offered %s\n want    %s", user, q, typ, h, hits, o, offered)
		}
	}

	// Nothing shared yet: the stranger finds and is offered nothing of the workspace.
	expect(ctx, "stranger", "in", "", "", "")

	// A folder share reaches its live subtree, paths starting at the shared folder; a file
	// share reaches the file alone. The owner's private names above them never show.
	shareA := share("folder", tr.a.ID, "user", "stranger", "reader")
	shareC := share("file", tr.inC.ID, "user", "stranger", "editor")
	expect(ctx, "stranger", "in", "",
		`inA.txt "a" Organization:reader name+content; inB.txt "a/b" Organization:reader name+content; inC.txt "" Organization:editor name+content`,
		"inA.txt,inB.txt,inC.txt")
	expect(ctx, "stranger", "", "folders", `a "" Organization:reader name; b "a" Organization:reader name`, "")
	expect(ctx, "stranger", "zz", "documents", `inA.txt "a" Organization:reader content; inB.txt "a/b" Organization:reader content; inC.txt "" Organization:editor content`, "inA.txt,inB.txt,inC.txt")
	expect(ctx, "stranger", "in", "pdfs", "", "")
	// Without shared (MCP) the shares stay out.
	if h, o := search(ctx, "stranger", "in", "", false); h != "" || o != "" {
		t.Fatalf("shared searched without shared: %s / %s", h, o)
	}
	// Administration, a service token, a device and KyPost get no shares.
	expect(ctx, "admin", "in", "", "", "")
	mine, err := d.EnsurePersonalWorkspace(ctx, "stranger")
	must(err)
	expect(drive.WithServiceAccess(ctx, drive.ServiceAccess{Workspace: mine, Rank: 1}), "stranger", "in", "", "", "")
	expect(drive.WithDevice(ctx), "stranger", "in", "", "", "")

	// A share revoked while the search runs is gone from the answer.
	m.during = func() { must(d.Unshare(ctx, "worker", shareC.ID)) }
	expect(ctx, "stranger", "in", "", `inA.txt "a" Organization:reader name+content; inB.txt "a/b" Organization:reader name+content`, "inA.txt,inB.txt,inC.txt")
	expect(ctx, "stranger", "in", "", `inA.txt "a" Organization:reader name+content; inB.txt "a/b" Organization:reader name+content`, "inA.txt,inB.txt")

	// Moved out of the shared folder, trashed, or the folder share revoked: gone at once.
	_, err = d.MoveFile(ctx, "worker", tr.inB.ID, tr.inB.Revision, drive.Location{Workspace: w.ID, Parent: tr.c.ID, Name: "inB.txt"})
	must(err)
	expect(ctx, "stranger", "in", "", `inA.txt "a" Organization:reader name+content`, "inA.txt")
	must(d.SetTrash(ctx, "worker", tr.inA.ID, tr.inA.Revision, true))
	expect(ctx, "stranger", "", "folders", `a "" Organization:reader name; b "a" Organization:reader name`, "")
	expect(ctx, "stranger", "in", "", "", "")
	must(d.SetTrash(ctx, "worker", tr.inA.ID, tr.inA.Revision, false))
	must(d.Unshare(ctx, "worker", shareA.ID))
	expect(ctx, "stranger", "in", "", "", "")

	// An organisation link counts once the stranger opened it, while it is live.
	link, token, err := d.CreateLink(ctx, "worker", "folder", tr.c.ID, "reader", 0)
	must(err)
	expect(ctx, "stranger", "in", "", "", "")
	_, err = d.OpenLink(ctx, "stranger", token)
	must(err)
	inC := `inB.txt "c" Organization:reader name+content; inC.txt "c" Organization:reader name+content`
	expect(ctx, "stranger", "in", "", inC, "inB.txt,inC.txt")
	must(drive.ExecSQL(ctx, d, `UPDATE drive_links SET expires=1 WHERE id='`+link.ID+`'`))
	expect(ctx, "stranger", "in", "", "", "")
	must(drive.ExecSQL(ctx, d, `UPDATE drive_links SET expires=0 WHERE id='`+link.ID+`'`))
	expect(ctx, "stranger", "in", "", inC, "inB.txt,inC.txt")
	must(d.RevokeLink(ctx, "worker", link.ID))
	expect(ctx, "stranger", "in", "", "", "")

	// A group share lasts while the stranger is in the group.
	must(st.Groups().ReplaceGroup(ctx, &store.Group{ID: "partners", DisplayName: "Partners", ExternalID: "x-partners", Members: []string{"stranger"}}, true))
	share("folder", tr.c.ID, "group", "partners", "reader")
	expect(ctx, "stranger", "in", "", inC, "inB.txt,inC.txt")
	must(st.Groups().ReplaceGroup(ctx, &store.Group{ID: "partners", DisplayName: "Partners", ExternalID: "x-partners"}, false))
	expect(ctx, "stranger", "in", "", "", "")

	// As a workspace member the stranger searches it once, by workspace path; a share above
	// the membership adds no second hit. Leaving the workspace keeps only the shared subtree.
	must(st.Groups().ReplaceGroup(ctx, &store.Group{ID: "partners", DisplayName: "Partners", ExternalID: "x-partners", Members: []string{"stranger"}}, false))
	must(d.Grant(ctx, "admin", w.ID, "partners", "reader"))
	share("folder", tr.c.ID, "user", "stranger", "editor")
	expect(ctx, "stranger", "in", "", `inA.txt "a" - name+content; inB.txt "c" - name+content; inC.txt "c" - name+content; root.txt "" - content`, "inA.txt,inB.txt,inC.txt,root.txt")
	must(st.Groups().ReplaceGroup(ctx, &store.Group{ID: "partners", DisplayName: "Partners", ExternalID: "x-partners"}, false))
	expect(ctx, "stranger", "in", "", `inB.txt "c" Organization:editor name+content; inC.txt "c" Organization:editor name+content`, "inB.txt,inC.txt")

	// Another user's unshared file is never found; their shared one stops with their account.
	personal, err := d.EnsurePersonalWorkspace(ctx, "worker")
	must(err)
	diary, err := d.Publish(ctx, "worker", drive.File{Workspace: personal, Name: "indiary.txt"}, blob(t, blobs, "x"), 0)
	must(err)
	names[diary.ID] = "indiary.txt"
	_, err = d.Publish(ctx, "worker", drive.File{Workspace: personal, Name: "inprivate.txt"}, blob(t, blobs, "x"), 0)
	must(err)
	share("file", diary.ID, "user", "stranger", "reader")
	expect(ctx, "stranger", "indiary", "", `indiary.txt "" worker:reader name+content; inB.txt "c" Organization:editor content; inC.txt "c" Organization:editor content`, "inB.txt,inC.txt,indiary.txt")
	must(drive.ExecSQL(ctx, d, `UPDATE users SET status='inactive' WHERE id='worker'`))
	expect(ctx, "stranger", "indiary", "", `inB.txt "c" Organization:editor content; inC.txt "c" Organization:editor content`, "inB.txt,inC.txt")
}
