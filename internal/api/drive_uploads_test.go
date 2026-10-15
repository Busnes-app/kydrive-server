package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func putChunk(t *testing.T, srv *api.Server, cookie *http.Cookie, id, contentRange, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("PUT", "/api/drive/upload-sessions/"+id, strings.NewReader(body))
	req.AddCookie(cookie)
	api.SignCSRFForTest(srv, req)
	if contentRange != "" {
		req.Header.Set("Content-Range", contentRange)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestResumableUploadRoutes(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "upadmin", "admin")
	user := loginAs(t, srv, st, "uploader", "user")
	other := loginAs(t, srv, st, "bystander", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "uploaders", DisplayName: "Uploaders", Members: []string{"usr_uploader", "usr_bystander"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_upadmin", "Uploads", 200<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_upadmin", w.ID, "uploaders", "editor"); err != nil {
		t.Fatal(err)
	}
	out := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/upload-sessions", `{"name":"big.bin","size":15}`, user, "")
	var session struct {
		ID       string
		Received int64
	}
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &session) != nil || session.ID == "" {
		t.Fatalf("start %d %s", out.Code, out.Body.String())
	}
	for _, c := range []struct {
		rng, body string
		code      int
	}{
		{"", "abcde", 400},
		{"bytes 0-4/16", "abcde", 400},   // total differs from the session
		{"bytes 4-0/15", "abcde", 400},   // backwards
		{"bytes=0-4/15", "abcde", 400},   // not the Content-Range syntax
		{"bytes 0-5/15", "abcde", 400},   // body shorter than the range
		{"bytes 5-9/15", "fghij", 409},   // gap
		{"bytes 0-4/15", "abcde", 204},   // first chunk
		{"bytes 0-4/15", "abcde", 409},   // replayed chunk
		{"bytes 5-9/15", "fghij", 204},   // second chunk
		{"bytes 10-14/15", "klmno", 204}, // last chunk
		{"bytes 15-15/15", "p", 400},     // past the end
	} {
		got := putChunk(t, srv, user, session.ID, c.rng, c.body)
		if got.Code != c.code {
			t.Fatalf("%q: %d %s", c.rng, got.Code, got.Body.String())
		}
		if c.code == 409 && got.Header().Get("Upload-Offset") == "" {
			t.Fatalf("%q: conflict without the current offset", c.rng)
		}
	}
	if got := send(t, srv, "GET", "/api/drive/upload-sessions/"+session.ID, "", other, ""); got.Code != 403 {
		t.Fatalf("other user read %d", got.Code)
	}
	if got := send(t, srv, "POST", "/api/drive/upload-sessions/"+session.ID+"/complete", "", other, ""); got.Code != 403 {
		t.Fatalf("other user completed %d", got.Code)
	}
	out = send(t, srv, "GET", "/api/drive/upload-sessions/"+session.ID, "", user, "")
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &session) != nil || session.Received != 15 {
		t.Fatalf("status %d %s", out.Code, out.Body.String())
	}
	out = send(t, srv, "POST", "/api/drive/upload-sessions/"+session.ID+"/complete", "", user, "")
	var file struct{ ID string }
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &file) != nil {
		t.Fatalf("complete %d %s", out.Code, out.Body.String())
	}
	if got := send(t, srv, "GET", "/api/drive/files/"+file.ID+"/download", "", user, ""); got.Body.String() != "abcdefghijklmno" {
		t.Fatalf("download %d %q", got.Code, got.Body.String())
	}
	if got := send(t, srv, "GET", "/api/drive/search?q=BIG", "", other, ""); got.Code != 200 || !strings.Contains(got.Body.String(), `"name":"big.bin"`) || !strings.Contains(got.Body.String(), `"truncated":false`) {
		t.Fatalf("search %d %s", got.Code, got.Body.String())
	}
	if got := send(t, srv, "GET", "/api/drive/search?q=b", "", other, ""); got.Code != 400 {
		t.Fatalf("one-letter search %d", got.Code)
	}

	// Cancel, then an oversized chunk and an oversized session.
	out = send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/upload-sessions", `{"name":"huge.bin","size":104857600}`, user, "")
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &session) != nil {
		t.Fatalf("start %d %s", out.Code, out.Body.String())
	}
	chunk := strings.Repeat("x", 64<<20+1)
	if got := putChunk(t, srv, user, session.ID, fmt.Sprintf("bytes 0-%d/104857600", len(chunk)-1), chunk); got.Code != 413 {
		t.Fatalf("oversized chunk %d", got.Code)
	}
	if got := send(t, srv, "DELETE", "/api/drive/upload-sessions/"+session.ID, "", user, ""); got.Code != 204 {
		t.Fatalf("cancel %d %s", got.Code, got.Body.String())
	}
	if got := send(t, srv, "GET", "/api/drive/upload-sessions/"+session.ID, "", user, ""); got.Code != 403 {
		t.Fatalf("cancelled session %d", got.Code)
	}
	if got := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/upload-sessions", `{"name":"x","size":10737418241}`, user, ""); got.Code != 413 {
		t.Fatalf("over max file size %d", got.Code)
	}
	if got := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/upload-sessions", `{"name":"x","size":1,"extra":1}`, user, ""); got.Code != 400 {
		t.Fatalf("unknown field %d", got.Code)
	}

	// A service token works only inside its own workspace.
	o, err := st.Drive().CreateWorkspace(ctx, "usr_upadmin", "Automation", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_upadmin", o.ID, "uploaders", "manager"); err != nil {
		t.Fatal(err)
	}
	const token = "kyd_upload_service_token_for_tests_0123456789"
	if _, err = st.Drive().CreateServiceToken(ctx, "usr_uploader", o.ID, "Robot", "editor", token); err != nil {
		t.Fatal(err)
	}
	if got := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/upload-sessions", `{"name":"x","size":1}`, nil, token); got.Code != 403 {
		t.Fatalf("service token outside its workspace %d", got.Code)
	}
	if got := send(t, srv, "POST", "/api/drive/workspaces/"+o.ID+"/upload-sessions", `{"name":"x","size":1}`, nil, token); got.Code != 201 {
		t.Fatalf("service token in its workspace %d %s", got.Code, got.Body.String())
	}
}
