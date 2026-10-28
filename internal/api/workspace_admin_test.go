package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestWorkspaceRenameAndDeleteRoutes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	boss := loginAs(t, srv, st, "boss", "admin")
	bob := loginAs(t, srv, st, "bob", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "crew", DisplayName: "Crew", ExternalID: "x-crew", Members: []string{"usr_boss"}}, true); err != nil {
		t.Fatal(err)
	}
	create := func(name string) string {
		t.Helper()
		out := send(t, srv, "POST", "/api/drive/workspaces", `{"name":"`+name+`","quota":1048576}`, boss, "")
		var w struct{ ID string }
		if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &w) != nil {
			t.Fatalf("create %s: %d %s", name, out.Code, out.Body.String())
		}
		return w.ID
	}
	studio, _ := create("Studio"), create("Other")
	if err := st.Drive().Grant(ctx, "usr_boss", studio, "crew", "manager"); err != nil {
		t.Fatal(err)
	}
	var mine struct{ ID string }
	if out := send(t, srv, "POST", "/api/drive/personal-workspace", "{}", bob, ""); json.Unmarshal(out.Body.Bytes(), &mine) != nil || mine.ID == "" {
		t.Fatalf("personal workspace %s", out.Body.String())
	}
	expect := func(method, path, body string, as string, want int) string {
		t.Helper()
		cookie := boss
		if as == "bob" {
			cookie = bob
		}
		out := send(t, srv, method, path, body, cookie, "")
		if out.Code != want {
			t.Fatalf("%s %s as %s: %d, want %d: %s", method, path, as, out.Code, want, out.Body.String())
		}
		return out.Body.String()
	}

	name := "/api/drive/workspaces/" + studio + "/name"
	expect("PUT", name, `{"name":"Design"}`, "bob", 403)
	if body := expect("PUT", name, `{"name":"other"}`, "boss", 409); !strings.Contains(body, "workspace name exists") {
		t.Fatalf("duplicate rename: %s", body)
	}
	expect("PUT", name, `{"name":""}`, "boss", 400)
	expect("PUT", "/api/drive/workspaces/"+mine.ID+"/name", `{"name":"Mine"}`, "boss", 403)
	expect("PUT", name, `{"name":"Design"}`, "boss", 200)
	if body := expect("GET", "/api/drive/workspaces?admin=true", "", "boss", 200); !strings.Contains(body, `"name":"Design"`) || strings.Contains(body, `"name":"Studio"`) {
		t.Fatalf("listing after rename: %s", body)
	}

	ws := "/api/drive/workspaces/" + studio
	var folder struct{ ID string }
	if json.Unmarshal([]byte(expect("POST", ws+"/folders", `{"parent":"","name":"Drafts"}`, "boss", 201)), &folder) != nil {
		t.Fatal("folder")
	}
	expect("DELETE", ws, "", "bob", 403)
	expect("DELETE", "/api/drive/workspaces/"+mine.ID, "", "boss", 403)
	if body := expect("DELETE", ws, "", "boss", 409); !strings.Contains(body, "not empty") {
		t.Fatalf("non-empty delete: %s", body)
	}
	expect("PUT", "/api/drive/folders/"+folder.ID+"/trash", `{"trashed":true}`, "boss", 200)
	expect("DELETE", ws, "", "boss", 409)
	expect("DELETE", "/api/drive/folders/"+folder.ID, "", "boss", 200)
	expect("DELETE", ws, "", "boss", 200)
	expect("DELETE", ws, "", "boss", 200) // already gone
	if body := expect("GET", "/api/drive/workspaces?admin=true", "", "boss", 200); strings.Contains(body, studio) {
		t.Fatalf("deleted workspace listed: %s", body)
	}
	expect("GET", ws+"/changes", "", "boss", 403)
	expect("GET", ws+"/snapshot", "", "boss", 403)
	body := expect("GET", "/api/drive/audit", "", "boss", 200)
	if strings.Count(body, `"workspace.deleted"`) != 1 || !strings.Contains(body, `name=\"Design\"`) || !strings.Contains(body, `from=\"Studio\" to=\"Design\"`) {
		t.Fatalf("audit: %s", body)
	}
}
