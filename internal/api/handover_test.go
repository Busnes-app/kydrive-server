package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLeaverHandoverRoutes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	boss := loginAs(t, srv, st, "boss", "admin")
	leaver := loginAs(t, srv, st, "leaver", "user")
	heir := loginAs(t, srv, st, "heir", "user")
	installer := loginAs(t, srv, st, "installer", "admin")
	if err := st.Settings().SetSetting(ctx, "installer.user.test", `{"userId":"usr_installer","role":"admin"}`); err != nil {
		t.Fatal(err)
	}
	var src struct{ ID string }
	out := send(t, srv, "POST", "/api/drive/personal-workspace", "{}", leaver, "")
	if json.Unmarshal(out.Body.Bytes(), &src) != nil || src.ID == "" {
		t.Fatalf("personal workspace %s", out.Body.String())
	}
	if out = send(t, srv, "POST", "/api/drive/workspaces/"+src.ID+"/uploads?name=secret-plan.txt&revision=0", "hello", leaver, ""); out.Code != 201 {
		t.Fatalf("upload %d", out.Code)
	}
	token := strings.Repeat("h", 64)
	if _, err := st.Drive().CreateServiceToken(ctx, "usr_leaver", src.ID, "Sync", "editor", token); err != nil {
		t.Fatal(err)
	}
	path := "/api/drive/leavers/" + src.ID + "/handover"
	body := `{"user":"usr_heir"}`
	if out = send(t, srv, "POST", path, body, boss, ""); out.Code != 409 {
		t.Fatalf("active owner handed over: %d %s", out.Code, out.Body.String())
	}
	u, err := st.Users().GetUserByID(ctx, "usr_leaver")
	if err != nil {
		t.Fatal(err)
	}
	u.Status = "inactive"
	if err = st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct {
		method, path, body string
		cookie             bool
		bearer             string
		want               int
	}{
		"anonymous list":     {"GET", "/api/drive/leavers", "", false, "", 401},
		"anonymous handover": {"POST", path, body, false, "", 401},
		"service list":       {"GET", "/api/drive/leavers", "", false, token, 401},
		"service handover":   {"POST", path, body, false, token, 401},
	} {
		if out = send(t, srv, c.method, c.path, c.body, nil, c.bearer); out.Code != c.want {
			t.Errorf("%s: %d", name, out.Code)
		}
	}
	if out = send(t, srv, "GET", "/api/drive/leavers", "", heir, ""); out.Code != 403 {
		t.Fatalf("user listed leavers: %d", out.Code)
	}
	if out = send(t, srv, "POST", path, body, heir, ""); out.Code != 403 {
		t.Fatalf("user handed over: %d", out.Code)
	}
	if out = send(t, srv, "POST", path, `{"user":"usr_installer"}`, boss, ""); out.Code != 403 {
		t.Fatalf("handed over to an installer administrator: %d", out.Code)
	}

	out = send(t, srv, "GET", "/api/drive/leavers", "", boss, "")
	if out.Code != 200 || strings.Contains(out.Body.String(), "secret-plan") {
		t.Fatalf("list %d %s", out.Code, out.Body.String())
	}
	var listing struct {
		Leavers []struct {
			Workspace, Status string
			Size              int64
			Items             int
		}
		Colleagues []struct{ ID string }
	}
	if err = json.Unmarshal(out.Body.Bytes(), &listing); err != nil || len(listing.Leavers) != 1 || listing.Leavers[0].Workspace != src.ID || listing.Leavers[0].Status != "inactive" || listing.Leavers[0].Size != 5 || listing.Leavers[0].Items != 1 {
		t.Fatalf("listing %s %v", out.Body.String(), err)
	}
	for _, c := range listing.Colleagues {
		if c.ID == "usr_installer" || c.ID == "usr_leaver" {
			t.Fatalf("offered %s as a colleague", c.ID)
		}
	}
	if len(listing.Colleagues) != 2 {
		t.Fatalf("colleagues %+v", listing.Colleagues)
	}

	if out = send(t, srv, "POST", path, body, boss, ""); out.Code != 200 {
		t.Fatalf("handover %d %s", out.Code, out.Body.String())
	}
	if out = send(t, srv, "POST", path, body, boss, ""); out.Code != 409 {
		t.Fatalf("second handover %d", out.Code)
	}
	var mine struct{ ID string }
	out = send(t, srv, "POST", "/api/drive/personal-workspace", "{}", heir, "")
	if json.Unmarshal(out.Body.Bytes(), &mine) != nil {
		t.Fatal(out.Body.String())
	}
	if out = send(t, srv, "GET", "/api/drive/workspaces/"+mine.ID+"/files?trash=false", "", heir, ""); !strings.Contains(out.Body.String(), "secret-plan.txt") {
		t.Fatalf("heir cannot browse: %s", out.Body.String())
	}
	if out = send(t, srv, "GET", "/api/drive/workspaces/"+mine.ID+"/files?trash=false", "", installer, ""); out.Code != 403 {
		t.Fatalf("installer reads: %d", out.Code)
	}
	if out = send(t, srv, "GET", "/api/drive/audit", "", boss, ""); strings.Contains(out.Body.String(), "secret-plan") || !strings.Contains(out.Body.String(), "workspace.handed_over") {
		t.Fatalf("audit %s", out.Body.String())
	}
}
