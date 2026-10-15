package dav_test

import (
	"context"
	"io"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/iotest"

	"golang.org/x/net/webdav"

	"github.com/Busnes-app/kydrive-server/internal/dav"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

type env struct {
	t      *testing.T
	st     store.Store
	blobs  string
	locks  webdav.LockSystem
	team   drive.Workspace
	shared drive.Workspace // another workspace also named "Organization"
}

func setup(t *testing.T) *env {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, testdb.Config(t), testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if st.Drive() == nil {
		t.Skip("drive is SQLite-only")
	}
	for _, u := range []*store.User{{ID: "admin", Username: "admin", Email: "admin@local.test", Role: "admin", Status: "active", SSOProvider: "local"}, {ID: "worker", Username: "worker", Email: "worker@local.test", Role: "user", Status: "active", SSOProvider: "scim"}, {ID: "reader", Username: "reader", Email: "reader@local.test", Role: "user", Status: "active", SSOProvider: "scim"}} {
		if err = st.Users().CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []*store.Group{{ID: "team", DisplayName: "Team", ExternalID: "x-team", Members: []string{"worker"}}, {ID: "readers", DisplayName: "Readers", ExternalID: "x-readers", Members: []string{"reader", "worker"}}} {
		if err = st.Groups().ReplaceGroup(ctx, g, true); err != nil {
			t.Fatal(err)
		}
	}
	e := &env{t: t, st: st, blobs: t.TempDir(), locks: webdav.NewMemLS()}
	for _, w := range []*drive.Workspace{&e.team, &e.shared} {
		if *w, err = st.Drive().CreateWorkspace(ctx, "admin", "Organization", 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	grants := [][3]string{{e.team.ID, "team", "editor"}, {e.team.ID, "readers", "reader"}, {e.shared.ID, "team", "editor"}}
	for _, g := range grants {
		if err = st.Drive().Grant(ctx, "admin", g[0], g[1], g[2]); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// org is the team workspace's WebDAV path: the lower workspace ID keeps the plain name.
func (e *env) org() string {
	if e.team.ID < e.shared.ID {
		return "/dav/Organization"
	}
	return "/dav/Organization%20(2)"
}

func (e *env) do(user, method, path string, body io.Reader, headers ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, path, body)
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	dav.Serve(w, r, "/dav", dav.New(e.st.Drive(), user, e.blobs, testdb.Key, 1<<20), e.locks)
	return w
}

func (e *env) want(got *httptest.ResponseRecorder, code int, what string) {
	e.t.Helper()
	if got.Code != code {
		e.t.Fatalf("%s: %d, want %d: %s", what, got.Code, code, got.Body.String())
	}
}

func (e *env) versions(name string) int {
	e.t.Helper()
	files, err := e.st.Drive().Files(context.Background(), "worker", e.team.ID, false)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, f := range files {
		if f.Name == name {
			return int(f.Revision)
		}
	}
	return 0
}

func TestWorkspacesAndFolders(t *testing.T) {
	e := setup(t)
	root := e.do("worker", "PROPFIND", "/dav/", nil, "Depth", "1")
	e.want(root, 207, "propfind root")
	for _, name := range []string{"/dav/Organization/", "/dav/Organization%20%282%29/"} {
		if !strings.Contains(root.Body.String(), "<D:href>"+name+"</D:href>") {
			t.Fatalf("root lacks %s:\n%s", name, root.Body.String())
		}
	}
	e.want(e.do("worker", "MKCOL", e.org()+"/Docs", nil), 201, "mkcol")
	e.want(e.do("worker", "MKCOL", e.org()+"/Docs", nil), 405, "mkcol existing")
	e.want(e.do("worker", "MKCOL", e.org()+"/Missing/Sub", nil), 409, "mkcol without parent")
	e.want(e.do("worker", "MKCOL", "/dav/NewSpace", nil), 403, "create workspace")
	e.want(e.do("worker", "DELETE", e.org(), nil), 403, "delete workspace")
	e.want(e.do("reader", "MKCOL", "/dav/Organization/Mine", nil), 403, "reader mkcol")
	listing := e.do("reader", "PROPFIND", "/dav/Organization/", nil, "Depth", "1")
	e.want(listing, 207, "reader propfind")
	if !strings.Contains(listing.Body.String(), "/Docs/</D:href>") {
		t.Fatalf("folder missing:\n%s", listing.Body.String())
	}
	// The reader cannot see the second workspace at all.
	e.want(e.do("reader", "PROPFIND", "/dav/Organization%20(2)/", nil, "Depth", "0"), 404, "unreadable workspace")
}

func TestPutGetAndVersions(t *testing.T) {
	e := setup(t)
	path := e.org() + "/R%C3%A9sum%C3%A9%202026.txt" // "Résumé 2026.txt"
	put := e.do("worker", "PUT", path, strings.NewReader("first draft"))
	e.want(put, 201, "put")
	if put.Header().Get("ETag") != `"1"` {
		t.Fatalf("etag %q", put.Header().Get("ETag"))
	}
	get := e.do("worker", "GET", path, nil)
	e.want(get, 200, "get")
	if get.Body.String() != "first draft" || get.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("get %q %q", get.Body.String(), get.Header().Get("Content-Type"))
	}
	ranged := e.do("worker", "GET", path, nil, "Range", "bytes=6-10")
	e.want(ranged, 206, "range")
	if ranged.Body.String() != "draft" {
		t.Fatalf("range %q", ranged.Body.String())
	}

	// Overwrite keeps the old revision in the history.
	e.want(e.do("worker", "PUT", path, strings.NewReader("second")), 201, "overwrite")
	if v := e.versions("Résumé 2026.txt"); v != 2 {
		t.Fatalf("revision %d", v)
	}
	// If-Match against a stale ETag publishes nothing; the current one wins.
	e.want(e.do("worker", "PUT", path, strings.NewReader("stale"), "If-Match", `"1"`), 412, "stale if-match")
	e.want(e.do("worker", "PUT", path, strings.NewReader("third"), "If-Match", `"2"`), 201, "fresh if-match")
	if v := e.versions("Résumé 2026.txt"); v != 3 {
		t.Fatalf("revision %d", v)
	}
	e.want(e.do("worker", "PUT", e.org()+"/new.txt", strings.NewReader("x"), "If-Match", `*`), 412, "if-match on a missing file")
	// The reader sees only the team workspace, so its path has no suffix.
	e.want(e.do("reader", "PUT", "/dav/Organization/R%C3%A9sum%C3%A9%202026.txt", strings.NewReader("reader")), 403, "reader put")
	e.want(e.do("worker", "PUT", e.org()+"/Missing/x.txt", strings.NewReader("x")), 409, "put without parent")
	e.want(e.do("worker", "PUT", e.org()+"/big.bin", strings.NewReader(strings.Repeat("x", 1<<20))), 507, "over quota")
}

// A body cut off mid-upload must not publish what arrived.
func TestInterruptedPutPublishesNothing(t *testing.T) {
	e := setup(t)
	cut := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(io.ErrUnexpectedEOF))
	got := e.do("worker", "PUT", e.org()+"/cut.txt", cut)
	if got.Code < 400 {
		t.Fatalf("interrupted put answered %d", got.Code)
	}
	if v := e.versions("cut.txt"); v != 0 {
		t.Fatalf("partial upload published as revision %d", v)
	}
}

func TestMoveCopyDelete(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.want(e.do("worker", "MKCOL", e.org()+"/A", nil), 201, "mkcol A")
	e.want(e.do("worker", "MKCOL", e.org()+"/B", nil), 201, "mkcol B")
	e.want(e.do("worker", "PUT", e.org()+"/A/note.txt", strings.NewReader("hello")), 201, "put")
	host := "http://example.com"
	e.want(e.do("worker", "MOVE", e.org()+"/A/note.txt", nil, "Destination", host+e.org()+"/A/renamed.txt"), 201, "rename")
	e.want(e.do("worker", "MOVE", e.org()+"/A/renamed.txt", nil, "Destination", host+e.org()+"/B/renamed.txt"), 201, "move into folder")
	e.want(e.do("worker", "MOVE", e.org()+"/B", nil, "Destination", host+e.org()+"/A/B"), 201, "move folder")
	if got := e.do("worker", "GET", e.org()+"/A/B/renamed.txt", nil); got.Body.String() != "hello" {
		t.Fatalf("after moves: %d %q", got.Code, got.Body.String())
	}
	e.want(e.do("worker", "COPY", e.org()+"/A/B/renamed.txt", nil, "Destination", host+e.org()+"/copy.txt"), 201, "copy")
	if got := e.do("worker", "GET", e.org()+"/copy.txt", nil); got.Body.String() != "hello" {
		t.Fatalf("copy: %q", got.Body.String())
	}
	e.want(e.do("worker", "DELETE", e.org()+"/copy.txt", nil), 204, "delete")
	e.want(e.do("worker", "GET", e.org()+"/copy.txt", nil), 404, "deleted")
	trashed, err := e.st.Drive().Files(ctx, "worker", e.team.ID, true)
	if err != nil || len(trashed) != 1 || trashed[0].Name != "copy.txt" {
		t.Fatalf("delete did not trash: %+v %v", trashed, err)
	}
	if err = e.st.Drive().SetTrash(ctx, "worker", trashed[0].ID, trashed[0].Revision, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	e.want(e.do("worker", "DELETE", e.org()+"/A", nil), 204, "delete folder")
	trashedFolders, _ := e.st.Drive().Folders(ctx, "worker", e.team.ID, true)
	if len(trashedFolders) != 2 {
		t.Fatalf("folder delete trashed %d folders", len(trashedFolders))
	}
}

func TestMoveAcrossWorkspacesNeedsManager(t *testing.T) {
	e := setup(t)
	e.want(e.do("worker", "PUT", e.org()+"/x.txt", strings.NewReader("x")), 201, "put")
	otherRoot := "/dav/Organization%20(2)"
	if e.org() != "/dav/Organization" {
		otherRoot = "/dav/Organization"
	}
	e.want(e.do("worker", "MOVE", e.org()+"/x.txt", nil, "Destination", "http://example.com"+otherRoot+"/x.txt"), 403, "editor cross-workspace move")
	if err := e.st.Drive().Grant(context.Background(), "admin", e.team.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
	if err := e.st.Drive().Grant(context.Background(), "admin", e.shared.ID, "team", "manager"); err != nil {
		t.Fatal(err)
	}
	e.want(e.do("worker", "MOVE", e.org()+"/x.txt", nil, "Destination", "http://example.com"+otherRoot+"/x.txt"), 201, "manager cross-workspace move")
}

func TestLocks(t *testing.T) {
	e := setup(t)
	path := e.org() + "/locked.odt"
	e.want(e.do("worker", "PUT", path, strings.NewReader("v1")), 201, "put")
	lock := e.do("worker", "LOCK", path, strings.NewReader(`<?xml version="1.0"?><D:lockinfo xmlns:D="DAV:"><D:lockscope><D:exclusive/></D:lockscope><D:locktype><D:write/></D:locktype><D:owner>LibreOffice</D:owner></D:lockinfo>`), "Timeout", "Second-60")
	e.want(lock, 200, "lock")
	token := lock.Header().Get("Lock-Token")
	if !regexp.MustCompile(`^<.+>$`).MatchString(token) {
		t.Fatalf("lock token %q", token)
	}
	e.want(e.do("worker", "PUT", path, strings.NewReader("no token")), 423, "put without the lock token")
	e.want(e.do("worker", "PUT", path, strings.NewReader("v2"), "If", "("+token+")"), 201, "put with the lock token")
	e.want(e.do("worker", "UNLOCK", path, nil, "Lock-Token", token), 204, "unlock")
}

func TestProppatchIsAcceptedAndDropped(t *testing.T) {
	e := setup(t)
	e.want(e.do("worker", "PUT", e.org()+"/w.txt", strings.NewReader("x")), 201, "put")
	got := e.do("worker", "PROPPATCH", e.org()+"/w.txt", strings.NewReader(`<?xml version="1.0"?><D:propertyupdate xmlns:D="DAV:" xmlns:Z="urn:schemas-microsoft-com:"><D:set><D:prop><Z:Win32LastModifiedTime>Wed, 15 Oct 2026 10:00:00 GMT</Z:Win32LastModifiedTime></D:prop></D:set></D:propertyupdate>`))
	e.want(got, 207, "proppatch")
	if !strings.Contains(got.Body.String(), "200 OK") {
		t.Fatalf("proppatch refused:\n%s", got.Body.String())
	}
}
