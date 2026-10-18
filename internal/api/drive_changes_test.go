package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

type changePage struct {
	Changes []struct {
		Kind    string
		ID      string
		Removed bool
		File    *struct{ Name, Workspace string }
	}
	Next int64
	More bool
}

type snapshotPage struct {
	Folders []struct{ ID, Name string }
	Files   []struct{ ID, Name string }
	Head    int64
	Next    string
}

func decode(t *testing.T, code int, body []byte, out any) {
	t.Helper()
	if code != 200 || json.Unmarshal(body, out) != nil {
		t.Fatalf("%d %s", code, body)
	}
}

// Only a current reader of the workspace sees its feed or snapshot, and the feed never
// reveals where a file went.
func TestChangesAuthorize(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	admin := loginAs(t, srv, st, "feedadmin", "admin")
	member := loginAs(t, srv, st, "member", "user")
	outsider := loginAs(t, srv, st, "outsider", "user")
	d := st.Drive()
	team := &store.Group{ID: "readers", DisplayName: "Readers", Members: []string{"usr_member"}}
	if err := st.Groups().ReplaceGroup(ctx, team, true); err != nil {
		t.Fatal(err)
	}
	w, err := d.CreateWorkspace(ctx, "usr_feedadmin", "Projects", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	other, err := d.CreateWorkspace(ctx, "usr_feedadmin", "Elsewhere", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, ws := range []string{w.ID, other.ID} {
		if err = d.Grant(ctx, "usr_feedadmin", ws, "readers", "manager"); err != nil {
			t.Fatal(err)
		}
	}
	up := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=plan.txt&revision=0", "plan", member, "")
	var file struct{ ID string }
	if up.Code != 201 || json.Unmarshal(up.Body.Bytes(), &file) != nil {
		t.Fatalf("upload %d %s", up.Code, up.Body.String())
	}
	if out := send(t, srv, "PUT", "/api/drive/files/"+file.ID+"/location", fmt.Sprintf(`{"revision":1,"workspace":%q,"parent":"","name":"secret-destination.txt"}`, other.ID), member, ""); out.Code != 200 {
		t.Fatalf("move %d %s", out.Code, out.Body.String())
	}
	feed := "/api/drive/workspaces/" + w.ID + "/changes"
	snap := "/api/drive/workspaces/" + w.ID + "/snapshot"

	out := send(t, srv, "GET", feed, "", member, "")
	var page changePage
	decode(t, out.Code, out.Body.Bytes(), &page)
	if len(page.Changes) != 1 || !page.Changes[0].Removed || page.Changes[0].File != nil || strings.Contains(out.Body.String(), "secret-destination") || strings.Contains(out.Body.String(), other.ID) {
		t.Fatalf("moved-out file leaked its destination: %s", out.Body.String())
	}
	if out = send(t, srv, "GET", snap, "", member, ""); out.Code != 200 {
		t.Fatalf("member snapshot %d", out.Code)
	}

	token := strings.Repeat("e", 64)
	if _, err = d.CreateServiceToken(ctx, "usr_member", other.ID, "Sync", "reader", token); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{feed, snap} {
		for who, cookie := range map[string]*http.Cookie{"outsider": outsider, "admin without a grant": admin} {
			if got := send(t, srv, "GET", path, "", cookie, ""); got.Code != 403 {
				t.Fatalf("%s %s: %d %s", who, path, got.Code, got.Body.String())
			}
		}
		if got := send(t, srv, "GET", path, "", nil, token); got.Code != 403 {
			t.Fatalf("token for another workspace %s: %d", path, got.Code)
		}
	}

	team.Members = nil
	if err = st.Groups().ReplaceGroup(ctx, team, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{feed, snap} {
		if got := send(t, srv, "GET", path, "", member, ""); got.Code != 403 {
			t.Fatalf("removed member %s: %d %s", path, got.Code, got.Body.String())
		}
	}
}

func TestChangeRoutes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "feedadmin", "admin")
	user := loginAs(t, srv, st, "syncer", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "sync", DisplayName: "Sync", Members: []string{"usr_syncer"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_feedadmin", "Projects", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_feedadmin", w.ID, "sync", "editor"); err != nil {
		t.Fatal(err)
	}
	base := "/api/drive/workspaces/" + w.ID
	if out := send(t, srv, "POST", base+"/folders", `{"name":"Docs"}`, user, ""); out.Code != 201 && out.Code != 200 {
		t.Fatalf("folder %d %s", out.Code, out.Body.String())
	}
	if out := send(t, srv, "POST", base+"/uploads?name=a.txt&revision=0", "a", user, ""); out.Code != 201 {
		t.Fatalf("upload %d", out.Code)
	}

	var snap snapshotPage
	out := send(t, srv, "GET", base+"/snapshot?limit=1", "", user, "")
	decode(t, out.Code, out.Body.Bytes(), &snap)
	if len(snap.Folders)+len(snap.Files) != 1 || snap.Next == "" || snap.Head == 0 {
		t.Fatalf("first snapshot page %s", out.Body.String())
	}
	head := snap.Head
	out = send(t, srv, "GET", base+"/snapshot?limit=1&after="+snap.Next, "", user, "")
	decode(t, out.Code, out.Body.Bytes(), &snap)
	if len(snap.Folders)+len(snap.Files) != 1 || snap.Next != "" {
		t.Fatalf("last snapshot page %s", out.Body.String())
	}
	lists := send(t, srv, "GET", "/api/drive/workspaces", "", user, "")
	if !strings.Contains(lists.Body.String(), fmt.Sprintf(`"head":%d`, head)) {
		t.Fatalf("workspace head %s", lists.Body.String())
	}

	var page changePage
	out = send(t, srv, "GET", fmt.Sprintf("%s/changes?after=%d", base, head), "", user, "")
	decode(t, out.Code, out.Body.Bytes(), &page)
	if len(page.Changes) != 0 || page.Next != head || page.More || !strings.Contains(out.Body.String(), `"changes":[]`) {
		t.Fatalf("idle feed %s", out.Body.String())
	}
	up := send(t, srv, "POST", base+"/uploads?name=b.txt&revision=0", "b", user, "")
	var b struct{ ID string }
	if up.Code != 201 || json.Unmarshal(up.Body.Bytes(), &b) != nil {
		t.Fatalf("upload %d", up.Code)
	}
	out = send(t, srv, "GET", fmt.Sprintf("%s/changes?after=%d", base, head), "", user, "")
	decode(t, out.Code, out.Body.Bytes(), &page)
	if len(page.Changes) != 1 || page.Changes[0].ID != b.ID || page.Changes[0].File == nil || page.Changes[0].File.Name != "b.txt" {
		t.Fatalf("upload change %s", out.Body.String())
	}
	head = page.Next
	if out = send(t, srv, "PUT", "/api/drive/files/"+b.ID+"/trash", `{"revision":1,"trashed":true}`, user, ""); out.Code != 200 {
		t.Fatalf("trash %d", out.Code)
	}
	out = send(t, srv, "GET", fmt.Sprintf("%s/changes?after=%d&limit=1", base, head), "", user, "")
	decode(t, out.Code, out.Body.Bytes(), &page)
	if len(page.Changes) != 1 || !page.Changes[0].Removed || page.More {
		t.Fatalf("trash change %s", out.Body.String())
	}

	for _, q := range []string{"limit=0", "limit=1001", "limit=x", "after=-1", "after=x"} {
		if got := send(t, srv, "GET", base+"/changes?"+q, "", user, ""); got.Code != 400 {
			t.Fatalf("changes?%s: %d", q, got.Code)
		}
	}
	if got := send(t, srv, "GET", base+"/snapshot?limit=1001", "", user, ""); got.Code != 400 {
		t.Fatalf("snapshot limit: %d", got.Code)
	}

	if err = st.Drive().PruneChanges(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := send(t, srv, "GET", base+"/changes?after=0", "", user, ""); got.Code != 410 || !strings.Contains(got.Body.String(), `"error"`) {
		t.Fatalf("pruned cursor %d %s", got.Code, got.Body.String())
	}
	if got := send(t, srv, "GET", fmt.Sprintf("%s/changes?after=%d", base, page.Next), "", user, ""); got.Code != 200 {
		t.Fatalf("head cursor after prune %d", got.Code)
	}
}
