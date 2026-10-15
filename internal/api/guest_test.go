package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func guestForm(srv *api.Server, values url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/public/links/download", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

func guestUploadReq(srv *api.Server, token, name, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/public/links/upload?name="+url.QueryEscape(name), strings.NewReader(body))
	r.Header.Set("X-Link-Token", token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

func TestGuestLinkRoutes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	admin := loginAs(t, srv, st, "guestadmin", "admin")
	owner := loginAs(t, srv, st, "guestowner", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "gowners", DisplayName: "Owners", Members: []string{"usr_guestowner"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_guestadmin", "Clients", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_guestadmin", w.ID, "gowners", "manager"); err != nil {
		t.Fatal(err)
	}
	folder, _ := st.Drive().AddFolder(ctx, "usr_guestowner", w.ID, "", "Deliverables")
	other, _ := st.Drive().AddFolder(ctx, "usr_guestowner", w.ID, "", "Internal")
	upload := func(parent, name, body string) string {
		t.Helper()
		out := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name="+name+"&parent="+parent+"&revision=0", body, owner, "")
		var f struct{ ID string }
		if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &f) != nil {
			t.Fatalf("upload %d %s", out.Code, out.Body.String())
		}
		return f.ID
	}
	report := upload(folder.ID, "report.pdf", "the report")
	secret := upload(other.ID, "secret.txt", "internal only")
	expect := func(got *httptest.ResponseRecorder, want int, what string) {
		t.Helper()
		if got.Code != want {
			t.Fatalf("%s: %d, want %d: %s", what, got.Code, want, got.Body.String())
		}
	}

	// Off by default: nothing can be created and nothing answers.
	expect(send(t, srv, "POST", "/api/drive/folders/"+folder.ID+"/guest-links", `{"days":7}`, owner, ""), 403, "create while off")
	expect(send(t, srv, "POST", "/api/public/links/view", `{"token":"x"}`, nil, ""), 404, "public while off")
	expect(send(t, srv, "PUT", "/api/drive/guest-links-policy", `{"enabled":true,"max_days":14}`, owner, ""), 403, "non-admin turns guest links on")
	expect(send(t, srv, "PUT", "/api/drive/guest-links-policy", `{"enabled":true,"max_days":14}`, admin, ""), 200, "admin turns guest links on")
	expect(send(t, srv, "POST", "/api/drive/folders/"+folder.ID+"/guest-links", `{"days":15}`, owner, ""), 400, "beyond max days")

	created := send(t, srv, "POST", "/api/drive/folders/"+folder.ID+"/guest-links", `{"days":7,"password":"correct horse"}`, owner, "")
	var link struct{ URL string }
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &link) != nil || !strings.Contains(link.URL, "/g/#") {
		t.Fatalf("create %d %s", created.Code, created.Body.String())
	}
	token := link.URL[strings.Index(link.URL, "#")+1:]

	view := send(t, srv, "POST", "/api/public/links/view", `{"token":"`+token+`"}`, nil, "")
	expect(view, 401, "no password")
	if view.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(view.Header().Get("X-Robots-Tag"), "noindex") || view.Header().Get("Set-Cookie") != "" {
		t.Fatalf("guest headers %v", view.Header())
	}
	ok := send(t, srv, "POST", "/api/public/links/view", `{"token":"`+token+`","password":"correct horse"}`, nil, "")
	if ok.Code != 200 || !strings.Contains(ok.Body.String(), `"name":"report.pdf"`) || strings.Contains(ok.Body.String(), "secret.txt") || strings.Contains(ok.Body.String(), w.ID) {
		t.Fatalf("view %d %s", ok.Code, ok.Body.String())
	}
	if got := guestForm(srv, url.Values{"token": {token}, "password": {"correct horse"}, "file": {report}}); got.Code != 200 || got.Body.String() != "the report" || !strings.HasPrefix(got.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download %d %q", got.Code, got.Body.String())
	}
	expect(guestForm(srv, url.Values{"token": {token}, "password": {"correct horse"}, "file": {secret}}), 404, "file outside the link")
	expect(guestUploadReq(srv, token, "x.txt", "x"), 401, "upload without password")

	// A signed-in visitor's cookie changes nothing on the public routes.
	cookied := httptest.NewRequest("POST", "/api/public/links/view", strings.NewReader(`{"token":"`+token+`","password":"correct horse"}`))
	cookied.AddCookie(owner)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, cookied)
	expect(rec, 200, "view with a session cookie and no CSRF header")

	// Wrong passwords lock the link after five.
	for range 4 {
		send(t, srv, "POST", "/api/public/links/view", `{"token":"`+token+`","password":"guess"}`, nil, "")
	}
	expect(send(t, srv, "POST", "/api/public/links/view", `{"token":"`+token+`","password":"guess"}`, nil, ""), 429, "fifth wrong password")

	// Upload-only: files go in, nothing comes out.
	created = send(t, srv, "POST", "/api/drive/folders/"+folder.ID+"/guest-links", `{"days":3,"upload_only":true}`, owner, "")
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &link) != nil {
		t.Fatalf("create upload-only %d %s", created.Code, created.Body.String())
	}
	drop := link.URL[strings.Index(link.URL, "#")+1:]
	expect(guestUploadReq(srv, drop, "invoice.pdf", "invoice"), 201, "guest upload")
	expect(guestForm(srv, url.Values{"token": {drop}, "file": {report}}), 404, "download through upload-only")
	if got := send(t, srv, "POST", "/api/public/links/view", `{"token":"`+drop+`"}`, nil, ""); got.Code != 200 || strings.Contains(got.Body.String(), "report.pdf") {
		t.Fatalf("upload-only view %d %s", got.Code, got.Body.String())
	}

	expect(send(t, srv, "PUT", "/api/drive/guest-links-policy", `{"enabled":false,"max_days":14}`, admin, ""), 200, "admin turns guest links off")
	expect(guestUploadReq(srv, drop, "late.pdf", "late"), http.StatusNotFound, "upload while off")
}
