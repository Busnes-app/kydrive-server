package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestShareRoutes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "shareadmin", "admin")
	owner := loginAs(t, srv, st, "owner", "user")
	guest := loginAs(t, srv, st, "guest", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "owners", DisplayName: "Owners", Members: []string{"usr_owner"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_shareadmin", "Projects", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_shareadmin", w.ID, "owners", "manager"); err != nil {
		t.Fatal(err)
	}
	folder, err := st.Drive().AddFolder(ctx, "usr_owner", w.ID, "", "Proposal")
	if err != nil {
		t.Fatal(err)
	}
	out := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=draft.txt&parent="+folder.ID+"&revision=0", "draft text", owner, "")
	var file struct{ ID string }
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &file) != nil {
		t.Fatalf("upload %d %s", out.Code, out.Body.String())
	}
	expect := func(got int, want int, what string) {
		t.Helper()
		if got != want {
			t.Fatalf("%s: %d, want %d", what, got, want)
		}
	}

	// The owner finds the guest by name and shares the folder.
	people := send(t, srv, "GET", "/api/drive/people?q=gue", "", owner, "")
	if people.Code != 200 || !strings.Contains(people.Body.String(), `"id":"usr_guest"`) || strings.Contains(people.Body.String(), "@") {
		t.Fatalf("people %d %s", people.Code, people.Body.String())
	}
	expect(send(t, srv, "GET", "/api/drive/folders/"+folder.ID+"/children", "", guest, "").Code, 403, "children before sharing")
	share := send(t, srv, "PUT", "/api/drive/folders/"+folder.ID+"/shares", `{"principal_kind":"user","principal_id":"usr_guest","role":"reader"}`, owner, "")
	var sh struct{ ID string }
	if share.Code != 200 || json.Unmarshal(share.Body.Bytes(), &sh) != nil {
		t.Fatalf("share %d %s", share.Code, share.Body.String())
	}
	mine := send(t, srv, "GET", "/api/drive/shared-with-me", "", guest, "")
	if mine.Code != 200 || !strings.Contains(mine.Body.String(), `"name":"Proposal"`) || !strings.Contains(mine.Body.String(), `"role":"reader"`) {
		t.Fatalf("shared with me %d %s", mine.Code, mine.Body.String())
	}
	children := send(t, srv, "GET", "/api/drive/folders/"+folder.ID+"/children", "", guest, "")
	if children.Code != 200 || !strings.Contains(children.Body.String(), `"name":"draft.txt"`) {
		t.Fatalf("children %d %s", children.Code, children.Body.String())
	}
	if got := send(t, srv, "GET", "/api/drive/files/"+file.ID+"/download", "", guest, ""); got.Code != 200 || got.Body.String() != "draft text" {
		t.Fatalf("download %d", got.Code)
	}
	expect(send(t, srv, "GET", "/api/drive/workspaces/"+w.ID+"/files", "", guest, "").Code, 403, "whole workspace")
	expect(send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=x.txt&parent="+folder.ID+"&revision=0", "x", guest, "").Code, 403, "reader upload")
	expect(send(t, srv, "GET", "/api/drive/folders/"+folder.ID+"/shares", "", guest, "").Code, 403, "share holder lists shares")
	expect(send(t, srv, "PUT", "/api/drive/workspaces/"+w.ID+"/sharing", `{"editors_share":true}`, guest, "").Code, 403, "outsider changes sharing")
	expect(send(t, srv, "PUT", "/api/drive/workspaces/"+w.ID+"/sharing", `{"editors_share":true}`, owner, "").Code, 200, "manager changes sharing")

	// Editor rights let the guest upload into the shared folder only.
	if got := send(t, srv, "PUT", "/api/drive/folders/"+folder.ID+"/shares", `{"principal_kind":"user","principal_id":"usr_guest","role":"editor"}`, owner, ""); got.Code != 200 {
		t.Fatalf("upgrade %d %s", got.Code, got.Body.String())
	}
	expect(send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=notes.txt&parent="+folder.ID+"&revision=0", "n", guest, "").Code, 201, "editor upload into share")
	expect(send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=root.txt&revision=0", "n", guest, "").Code, 403, "editor upload outside share")

	expect(send(t, srv, "DELETE", "/api/drive/shares/"+sh.ID, "", guest, "").Code, 403, "share holder removes share")
	expect(send(t, srv, "DELETE", "/api/drive/shares/"+sh.ID, "", owner, "").Code, 204, "owner removes share")
	expect(send(t, srv, "GET", "/api/drive/folders/"+folder.ID+"/children", "", guest, "").Code, 403, "children after unsharing")
}
