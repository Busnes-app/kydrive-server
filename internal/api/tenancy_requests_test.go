package api_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/google/uuid"
)

// Compare the response before following redirects or decoding the body. A location,
// filename or resumable offset in a header would reveal a resource even on refusal.
func sameOrgResponse(t *testing.T, foreign, missing *httptest.ResponseRecorder) {
	t.Helper()
	if foreign.Code != missing.Code || !bytes.Equal(foreign.Body.Bytes(), missing.Body.Bytes()) {
		t.Fatalf("foreign: %d %q; missing: %d %q", foreign.Code, foreign.Body.Bytes(), missing.Code, missing.Body.Bytes())
	}
	for _, h := range []string{"Location", "Content-Type", "Content-Disposition", "Content-Length", "Content-Range", "ETag", "Last-Modified", "Upload-Offset", "DAV", "Lock-Token"} {
		if foreign.Header().Get(h) != missing.Header().Get(h) {
			t.Fatalf("foreign and missing responses differ in %s", h)
		}
	}
}

func requireStatus(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("got %d %s, want %d", w.Code, w.Body.String(), status)
	}
}

// Exercise valid, typed IDs in bodies, queries and compound paths. Keep A's
// authorized source or destination fixed when the other operand belongs to B.
func TestCrossOrgDriveRequests(t *testing.T) {
	editorFixture(t)
	m := setupMultiServer(t)
	a, b := m.ids["org-a"], m.ids["org-b"]
	aw := "/api/drive/workspaces/" + a["workspace"]
	af := "/api/drive/files/" + a["file"]
	ctx, d := t.Context(), m.st.Drive()
	if err := m.st.Settings().SetSetting(ctx, "drive_guest_links", "on"); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, send(t, m.srv, "POST", "/api/auth/step-up", `{"password":"SuperSecretPass123!"}`, m.session["a1"], ""), 200)

	// Retain an actual older revision. Purge and restore would both be valid for B.
	upload := "/api/drive/workspaces/" + b["workspace"] + "/uploads"
	requireStatus(t, send(t, m.srv, "POST", upload+"?name=plan.txt&file="+b["file"]+"&revision=1", "revised plan", m.session["b1"], ""), 201)
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	uploaded := send(t, m.srv, "POST", upload+"?name=sketch.png&revision=0", img.String(), m.session["b1"], "")
	requireStatus(t, uploaded, 201)
	var sketch drive.File
	if err := json.Unmarshal(uploaded.Body.Bytes(), &sketch); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, m.call("b1", "", "GET", "/api/drive/files/"+sketch.ID+"/thumbnail"), 200)

	trash, err := d.AddFolder(ctx, "b1", b["workspace"], "", "Old plans")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := d.CopyFile(ctx, "b1", b["file"], drive.Location{Workspace: b["workspace"], Parent: trash.ID, Name: "discard.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if err = d.SetFolderTrash(ctx, "b1", trash.ID, true); err != nil {
		t.Fatal(err)
	}

	before := m.orgBState(t)
	type request struct{ method, path, body, foreign string }
	cases := []request{
		{"POST", "/api/drive/workspaces/ID/folders", `{"name":"new"}`, b["workspace"]},
		{"POST", aw + "/folders", `{"name":"new","parent":"ID"}`, b["folder"]},
		{"POST", aw + "/documents", `{"name":"new","kind":"document","parent":"ID"}`, b["folder"]},
		{"POST", "/api/drive/workspaces/ID/documents", `{"name":"new","kind":"document"}`, b["workspace"]},
		{"POST", "/api/drive/workspaces/ID/uploads?name=new.txt&revision=0", "contents", b["workspace"]},
		{"POST", aw + "/uploads?name=new.txt&parent=ID&revision=0", "contents", b["folder"]},
		{"POST", aw + "/uploads?name=plan.txt&file=ID&revision=2", "contents", b["file"]},
		{"POST", "/api/drive/workspaces/ID/upload-sessions", `{"name":"new.txt","size":4}`, b["workspace"]},
		{"POST", aw + "/upload-sessions", `{"name":"new.txt","size":4,"parent":"ID"}`, b["folder"]},
		{"POST", aw + "/upload-sessions", `{"file":"ID","size":4,"revision":2}`, b["file"]},
		{"GET", "/api/drive/workspaces/ID/files?trash=true", "", b["workspace"]},
		{"GET", "/api/drive/workspaces/ID/folders?trash=true", "", b["workspace"]},
		{"GET", "/api/drive/files/ID/versions", "", b["file"]},
		{"DELETE", "/api/drive/files/ID/versions/1", "", b["file"]},
		{"POST", "/api/drive/files/ID/restore", `{"revision":1,"expected":2}`, b["file"]},
		{"PUT", "/api/drive/files/ID/trash", `{"revision":2,"trashed":true}`, b["file"]},
		{"PUT", "/api/drive/folders/ID/trash", `{"trashed":true}`, b["folder"]},
		{"PUT", "/api/drive/folders/ID/trash", `{"trashed":false}`, trash.ID},
		{"DELETE", "/api/drive/folders/ID", "", trash.ID},
		{"DELETE", "/api/drive/files/ID", "", deleted.ID},
		{"PUT", af + "/location", `{"workspace":"ID","revision":1,"name":"moved.txt"}`, b["workspace"]},
		{"POST", af + "/copy", `{"workspace":"ID","name":"copy.txt"}`, b["workspace"]},
		{"PUT", "/api/drive/folders/" + a["folder"] + "/location", `{"parent":"ID","name":"moved"}`, b["folder"]},
		{"PUT", aw + "/grants/ID", `{"role":"reader"}`, b["group"]},
		{"PUT", "/api/drive/workspaces/ID/grants/" + a["group"], `{"role":"editor"}`, b["workspace"]},
		{"PUT", "/api/drive/workspaces/ID/sharing", `{"editors_share":true}`, b["workspace"]},
		{"PUT", af + "/shares", `{"principal_kind":"user","principal_id":"ID","role":"reader"}`, "b2"},
		{"PUT", af + "/shares", `{"principal_kind":"group","principal_id":"ID","role":"reader"}`, b["group"]},
		{"POST", "/api/drive/links/open", `{"token":"ID"}`, b["linkToken"]},
		{"GET", "/api/drive/files/ID/preview", "", b["file"]},
		{"GET", "/api/drive/files/ID/thumbnail", "", sketch.ID},
		{"POST", "/api/drive/files/ID/editor", "{}", b["file"]},
	}
	for _, kind := range []string{"file", "folder"} {
		path := "/api/drive/" + kind + "s/ID/"
		cases = append(cases,
			request{"GET", path + "shares", "", b[kind]},
			request{"PUT", path + "shares", `{"principal_kind":"user","principal_id":"a2","role":"editor"}`, b[kind]},
			request{"GET", path + "links", "", b[kind]},
			request{"POST", path + "links", `{"role":"reader"}`, b[kind]},
			request{"GET", path + "guest-links", "", b[kind]},
			request{"POST", path + "guest-links", `{"days":1}`, b[kind]},
		)
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			call := func(id string) *httptest.ResponseRecorder {
				return send(t, m.srv, tc.method, strings.ReplaceAll(tc.path, "ID", id), strings.ReplaceAll(tc.body, "ID", id), m.session["a1"], "")
			}
			foreign, missing := call(tc.foreign), call(uuid.NewString())
			sameOrgResponse(t, foreign, missing)
			switch missing.Code {
			case 400, 403, 404:
			default:
				t.Fatalf("expected refusal, got %d %s", missing.Code, missing.Body.String())
			}
		})
	}
	if m.orgBState(t) != before {
		t.Fatal("org B's drive state changed")
	}
}

func compareMissing(t *testing.T, foreignID string, want int, call func(string) *httptest.ResponseRecorder) {
	t.Helper()
	foreign, missing := call(foreignID), call(uuid.NewString())
	sameOrgResponse(t, foreign, missing)
	requireStatus(t, missing, want)
}

func TestCrossOrgResumableUpload(t *testing.T) {
	m := setupMultiServer(t)
	path := "/api/drive/workspaces/" + m.ids["org-b"]["workspace"] + "/upload-sessions"
	start := send(t, m.srv, "POST", path, `{"name":"report.txt","size":6}`, m.session["b1"], "")
	requireStatus(t, start, 201)
	var session drive.UploadSession
	if err := json.Unmarshal(start.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatalf("start upload: %s (%v)", start.Body, err)
	}
	const base = "/api/drive/upload-sessions/"
	requireStatus(t, putChunk(t, m.srv, m.session["b1"], session.ID, "bytes 0-2/6", "one"), 204)
	before := m.orgBState(t)
	compareMissing(t, session.ID, 403, func(id string) *httptest.ResponseRecorder {
		return m.call("a1", "", "GET", base+id)
	})
	compareMissing(t, session.ID, 403, func(id string) *httptest.ResponseRecorder {
		return putChunk(t, m.srv, m.session["a1"], id, "bytes 3-5/6", "bad")
	})
	if m.orgBState(t) != before {
		t.Fatal("foreign append changed staged upload")
	}
	requireStatus(t, putChunk(t, m.srv, m.session["b1"], session.ID, "bytes 3-5/6", "two"), 204)
	before = m.orgBState(t)
	compareMissing(t, session.ID, 403, func(id string) *httptest.ResponseRecorder {
		return m.call("a1", "", "POST", base+id+"/complete")
	})
	compareMissing(t, session.ID, 403, func(id string) *httptest.ResponseRecorder {
		return m.call("a1", "", "DELETE", base+id)
	})
	if m.orgBState(t) != before {
		t.Fatal("foreign completion or cancellation changed org B")
	}
	completed := m.call("b1", "", "POST", base+session.ID+"/complete")
	requireStatus(t, completed, 201)
	var f drive.File
	if err := json.Unmarshal(completed.Body.Bytes(), &f); err != nil {
		t.Fatal(err)
	}
	got := m.call("b1", "", "GET", "/api/drive/files/"+f.ID+"/download")
	requireStatus(t, got, 200)
	if got.Body.String() != "onetwo" {
		t.Fatal("staged bytes changed")
	}
}

func TestCrossOrgDAV(t *testing.T) {
	m := setupMultiServer(t)
	if _, err := m.db.Exec(`UPDATE drive_workspaces SET name='BTeam' WHERE id=?`, m.ids["org-b"]["workspace"]); err != nil {
		t.Fatal(err)
	}
	secret := map[string]string{}
	for _, user := range []string{"a1", "b1"} {
		_, pass, err := m.st.Drive().CreateAppPassword(t.Context(), user, "Laptop")
		if err != nil {
			t.Fatal(err)
		}
		secret[user] = pass
	}
	request := func(user, method, path, body string, headers ...string) *httptest.ResponseRecorder {
		return davRequest(m.srv, method, "/dav/"+path, user, secret[user], strings.NewReader(body), headers...)
	}
	const path = "/Plans/plan.txt"
	control := request("b1", "GET", "BTeam"+path, "")
	requireStatus(t, control, 200)
	if control.Body.String() != "secret plan of org-b" {
		t.Fatal("wrong DAV fixture")
	}
	list := request("a1", "PROPFIND", "", "", "Depth", "1")
	requireStatus(t, list, 207)
	if !strings.Contains(list.Body.String(), "/dav/Team/") || strings.Contains(list.Body.String(), "BTeam") {
		t.Fatal("DAV root lists the wrong workspaces")
	}
	before := m.orgBState(t)
	for _, method := range []string{"GET", "HEAD", "PROPFIND", "PUT", "DELETE", "COPY", "MOVE"} {
		t.Run(method, func(t *testing.T) {
			call := func(ws string) *httptest.ResponseRecorder {
				switch method {
				case "COPY", "MOVE":
					return request("a1", method, "Team"+path, "", "Destination", "/dav/"+ws+"/copy.txt")
				case "PROPFIND":
					return request("a1", method, ws+path, "", "Depth", "0")
				case "PUT":
					return request("a1", method, ws+path, "revised")
				default:
					return request("a1", method, ws+path, "")
				}
			}
			foreign, missing := call("BTeam"), call("MissingTeam")
			sameOrgResponse(t, foreign, missing)
			switch missing.Code {
			case 403, 404, 405, 409:
			default:
				t.Fatalf("unexpected DAV response %d", missing.Code)
			}
		})
	}
	if m.orgBState(t) != before {
		t.Fatal("DAV changed org B")
	}
	again := request("b1", "GET", "BTeam"+path, "")
	requireStatus(t, again, 200)
	if !bytes.Equal(again.Body.Bytes(), control.Body.Bytes()) {
		t.Fatal("DAV changed file contents")
	}
}

func TestCrossOrgSearch(t *testing.T) {
	t.Setenv("KYDRIVE_CONTENT_SEARCH", "true")
	m := setupMultiServer(t)
	upload := "/api/drive/workspaces/" + m.ids["org-b"]["workspace"] + "/uploads?name=ledgercanary.txt&revision=0"
	out := send(t, m.srv, "POST", upload, "quartzcanary", m.session["b1"], "")
	requireStatus(t, out, 201)
	var f drive.File
	if err := json.Unmarshal(out.Body.Bytes(), &f); err != nil || f.ID == "" {
		t.Fatalf("upload: %s (%v)", out.Body, err)
	}
	type query struct{ term, options string }
	queries := []query{{"ledgercanary", "&contents=0"}}
	if !testdb.Postgres() {
		if err := api.IndexSearchForTest(m.srv); err != nil {
			t.Fatal(err)
		}
		queries = append(queries, query{term: "quartzcanary"})
	}
	before := m.orgBState(t)
	for _, q := range queries {
		search := func(user, query string) *httptest.ResponseRecorder {
			return m.call(user, "", "GET", "/api/drive/search?q="+query+q.options)
		}
		control := search("b1", q.term)
		requireStatus(t, control, 200)
		if !strings.Contains(control.Body.String(), f.ID) {
			t.Fatalf("owner search %s: %s", q.term, control.Body)
		}
		missing := search("a1", "absentcanary")
		requireStatus(t, missing, 200)
		var empty searchResponse
		if err := json.Unmarshal(missing.Body.Bytes(), &empty); err != nil || len(empty.Results) != 0 {
			t.Fatalf("expected empty search: %s", missing.Body)
		}
		sameOrgResponse(t, search("a1", q.term), missing)
	}
	if m.orgBState(t) != before {
		t.Fatal("search changed org B")
	}
}

func TestCrossOrgKyPostView(t *testing.T) {
	signer, origin := editorFixture(t)
	m := setupMultiServer(t)
	d := m.st.Drive()
	peer := drive.WithPeer(t.Context(), drive.Peer{SwitchDefault: true, Live: func() bool { return true }, Pairing: "test-pairing"})
	token, copy, err := d.IssueViewTicket(peer, "b1", "report.docx", int64(len(viewDocx)), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	root := objects.Dir(filepath.Join(m.cfg.Database.DataDir, "view"))
	if err := d.StoreViewCopy(peer, root, m.cfg.Security.EncryptionKey, copy, token, strings.NewReader(viewDocx)); err != nil {
		t.Fatal(err)
	}
	const base = "/api/drive/view-copies/"
	before := m.orgBState(t)
	for _, tc := range []struct {
		action string
		status int
	}{{"editor", 404}, {"close", 204}} {
		compareMissing(t, copy.ID, tc.status, func(id string) *httptest.ResponseRecorder { return m.call("a1", "", "POST", base+id+"/"+tc.action) })
	}
	compareMissing(t, copy.ID, 401, func(id string) *httptest.ResponseRecorder { return m.call("a1", "", "GET", "/api/editor/view/"+id) })
	if m.orgBState(t) != before {
		t.Fatal("viewing copy changed")
	}
	requireStatus(t, m.call("b1", "", "POST", base+copy.ID+"/editor"), 200)
	path := "/api/editor/view/" + copy.ID
	signature, err := signer.Sign(map[string]any{"payload": map[string]string{"url": origin + path}, "exp": time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	content := send(t, m.srv, "GET", path, "", nil, signature)
	requireStatus(t, content, 200)
	if content.Body.String() != viewDocx {
		t.Fatal("viewing copy contents changed")
	}
}

func TestCrossOrgGroupMembership(t *testing.T) {
	m := setupMultiServer(t)
	before := m.orgBState(t)
	for _, method := range []string{"GET", "PATCH", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			body := ""
			if method == "PATCH" {
				body = `{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"add","path":"members","value":[{"value":"a1"}]}]}`
			}
			compareMissing(t, m.ids["org-b"]["group"], 404, func(id string) *httptest.ResponseRecorder {
				return m.callBody("a1", "", method, "/scim/v2/Groups/"+id, body)
			})
		})
	}
	ws := m.call("a1", "", "GET", "/api/drive/workspaces")
	requireStatus(t, ws, 200)
	if !strings.Contains(ws.Body.String(), m.ids["org-a"]["workspace"]) || strings.Contains(ws.Body.String(), m.ids["org-b"]["workspace"]) {
		t.Fatal("group grants returned the wrong workspaces")
	}
	if m.orgBState(t) != before {
		t.Fatal("group membership changed")
	}
}

func TestCrossOrgAdminRequests(t *testing.T) {
	m := setupMultiServer(t)
	b := m.ids["org-b"]
	before := m.orgBState(t)
	for route, class := range adminClass {
		t.Run(route, func(t *testing.T) {
			method, path, ok := strings.Cut(route, " ")
			if !ok {
				method, path = "POST", route
			}
			resource := b["workspace"]
			body := "{}"
			if strings.HasSuffix(path, "/handover") {
				resource, body = b["personal"], `{"user":"a2"}`
			} else if strings.HasSuffix(path, "/restore") {
				resource = b["deleted"]
			}
			call := func(user, org, id string) *httptest.ResponseRecorder {
				return m.callBody(user, org, method, pathParam.ReplaceAllString(path, id), body)
			}
			memberStatus := 403
			if class == "singleTenantAdmin" {
				memberStatus = 404
			}
			// A customer cannot select B even by naming it explicitly.
			foreign := call("a1", "org-b", resource)
			sameOrgResponse(t, foreign, call("a1", "org-b", uuid.NewString()))
			sameOrgResponse(t, foreign, call("a1", "missing-org", resource))
			requireStatus(t, foreign, memberStatus)
			if class != "orgAdmin" {
				return
			}
			noOrg := call("staff", "", resource)
			sameOrgResponse(t, noOrg, call("staff", "", uuid.NewString()))
			sameOrgResponse(t, noOrg, call("staff", "missing-org", resource))
			requireStatus(t, noOrg, 400)
			if !strings.Contains(noOrg.Body.String(), `"org_required"`) {
				t.Fatal("missing org selection error")
			}
			if method == "POST" {
				want := 404
				if strings.HasSuffix(path, "/handover") {
					want = 400
				}
				compareMissing(t, resource, want, func(id string) *httptest.ResponseRecorder { return call("staff", "org-a", id) })
			}
		})
	}
	if m.orgBState(t) != before {
		t.Fatal("administration changed org B")
	}
}
