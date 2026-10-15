package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestAppPasswordRoutes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "apadmin", "admin")
	user := loginAs(t, srv, st, "appuser", "user")
	out := send(t, srv, "POST", "/api/auth/app-passwords", `{"name":"Laptop"}`, user, "")
	var created struct{ ID, Name, Password, Username, URL string }
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &created) != nil || created.Password == "" || created.Username != "appuser" || !strings.HasSuffix(created.URL, "/dav/") {
		t.Fatalf("create %d %s", out.Code, out.Body.String())
	}
	if out.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("secret response cacheable")
	}
	out = send(t, srv, "GET", "/api/auth/app-passwords", "", user, "")
	if out.Code != 200 || !strings.Contains(out.Body.String(), `"name":"Laptop"`) || strings.Contains(out.Body.String(), created.Password) {
		t.Fatalf("list %d %s", out.Code, out.Body.String())
	}

	// A drive service token is not a session: it can neither list nor mint app passwords.
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "apgroup", DisplayName: "AP", Members: []string{"usr_appuser"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_apadmin", "AP", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_apadmin", w.ID, "apgroup", "manager"); err != nil {
		t.Fatal(err)
	}
	const token = "kyd_app_password_route_test_token_0123456789"
	if _, err = st.Drive().CreateServiceToken(ctx, "usr_appuser", w.ID, "Robot", "editor", token); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		if got := send(t, srv, method, "/api/auth/app-passwords", `{"name":"x"}`, nil, token); got.Code != 401 {
			t.Fatalf("%s with service token: %d", method, got.Code)
		}
	}
	other := loginAs(t, srv, st, "otheruser", "user")
	if got := send(t, srv, "DELETE", "/api/auth/app-passwords/"+created.ID, "", other, ""); got.Code != 403 {
		t.Fatalf("revoke another user's password: %d", got.Code)
	}
	if got := send(t, srv, "DELETE", "/api/auth/app-passwords/"+created.ID, "", user, ""); got.Code != 204 {
		t.Fatalf("revoke %d %s", got.Code, got.Body.String())
	}
	if got := send(t, srv, "GET", "/api/auth/app-passwords", "", user, ""); got.Body.String() != "[]\n" {
		t.Fatalf("after revoke %s", got.Body.String())
	}
}
