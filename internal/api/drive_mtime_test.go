package api_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestUploadMtime(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "mtadmin", "admin")
	user := loginAs(t, srv, st, "mtuser", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "mt", DisplayName: "Mt", Members: []string{"usr_mtuser"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_mtadmin", "Dated", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_mtadmin", w.ID, "mt", "editor"); err != nil {
		t.Fatal(err)
	}
	var f struct{ ID, Mtime, Modified string }
	decode := func(body []byte) {
		t.Helper()
		f.ID, f.Mtime, f.Modified = "", "", ""
		if err := json.Unmarshal(body, &f); err != nil {
			t.Fatal(err)
		}
	}
	uploads := "/api/drive/workspaces/" + w.ID + "/uploads?revision=0&name="

	out := send(t, srv, "POST", uploads+"a.txt&mtime="+url.QueryEscape("2020-01-02T04:04:05+01:00"), "hello", user, "")
	if out.Code != 201 {
		t.Fatalf("upload %d %s", out.Code, out.Body.String())
	}
	decode(out.Body.Bytes())
	if f.Mtime != "2020-01-02T03:04:05Z" {
		t.Fatalf("upload mtime %q", f.Mtime)
	}
	decode(send(t, srv, "GET", "/api/drive/files/"+f.ID, "", user, "").Body.Bytes())
	if f.Mtime != "2020-01-02T03:04:05Z" {
		t.Fatalf("read mtime %q", f.Mtime)
	}

	decode(send(t, srv, "POST", uploads+"b.txt", "hello", user, "").Body.Bytes())
	if f.Mtime == "" || f.Mtime != f.Modified {
		t.Fatalf("fallback mtime %q, modified %q", f.Mtime, f.Modified)
	}

	blobs := func() int {
		entries, _ := os.ReadDir(filepath.Join(cfg.Database.DataDir, "blobs"))
		return len(entries)
	}
	before := blobs()
	for _, m := range []string{"1700000000", "1969-12-31T23:59:59Z", "3000-01-01T00:00:00Z"} {
		if out = send(t, srv, "POST", uploads+"c.txt&mtime="+url.QueryEscape(m), "hello", user, ""); out.Code != 400 {
			t.Fatalf("mtime %q: %d %s", m, out.Code, out.Body.String())
		}
		if out = send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/upload-sessions", `{"name":"c.txt","size":5,"mtime":"`+m+`"}`, user, ""); out.Code != 400 {
			t.Fatalf("session mtime %q: %d %s", m, out.Code, out.Body.String())
		}
	}
	if blobs() != before {
		t.Fatal("a refused mtime left a blob")
	}

	out = send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/upload-sessions", `{"name":"big.bin","size":5,"mtime":"2021-06-07T08:09:10Z"}`, user, "")
	var session struct{ ID, Mtime string }
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &session) != nil || session.Mtime != "2021-06-07T08:09:10Z" {
		t.Fatalf("start %d %s", out.Code, out.Body.String())
	}
	if got := putChunk(t, srv, user, session.ID, "bytes 0-4/5", "abcde"); got.Code != 204 {
		t.Fatalf("chunk %d %s", got.Code, got.Body.String())
	}
	out = send(t, srv, "POST", "/api/drive/upload-sessions/"+session.ID+"/complete", "", user, "")
	if out.Code != 201 || !strings.Contains(out.Body.String(), `"mtime":"2021-06-07T08:09:10Z"`) {
		t.Fatalf("complete %d %s", out.Code, out.Body.String())
	}
}
