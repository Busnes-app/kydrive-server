package api_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

func TestFolderZipRoute(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "zipadmin", "admin")
	user := loginAs(t, srv, st, "zipper", "user")
	outsider := loginAs(t, srv, st, "zipoutsider", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "zippers", DisplayName: "Zippers", Members: []string{"usr_zipper"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_zipadmin", "Zips", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_zipadmin", w.ID, "zippers", "editor"); err != nil {
		t.Fatal(err)
	}
	top, err := st.Drive().AddFolder(ctx, "usr_zipper", w.ID, "", "Reports")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := st.Drive().AddFolder(ctx, "usr_zipper", w.ID, top.ID, "Q1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Reports/": "", "Reports/Q1/": "", "Reports/summary.txt": "summary", "Reports/Q1/data.csv": "a,b\n1,2\n"}
	for parent, f := range map[string][2]string{top.ID: {"summary.txt", "summary"}, sub.ID: {"data.csv", "a,b\n1,2\n"}} {
		if out := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name="+f[0]+"&parent="+parent+"&revision=0", f[1], user, ""); out.Code != 201 {
			t.Fatalf("upload %d %s", out.Code, out.Body.String())
		}
	}
	out := send(t, srv, "GET", "/api/drive/folders/"+top.ID+"/zip", "", user, "")
	if out.Code != 200 || out.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("zip %d %s", out.Code, out.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Body.Bytes()), int64(out.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		r.Close()
		got[f.Name] = string(b)
	}
	if a, b := mustJSON(got), mustJSON(want); a != b {
		t.Fatalf("zip holds %s, want %s", a, b)
	}
	if out = send(t, srv, "GET", "/api/drive/folders/"+top.ID+"/zip", "", outsider, ""); out.Code != 403 {
		t.Fatalf("outsider zip %d", out.Code)
	}

	cfg.Drive.MaxZipSize = 10
	if out = send(t, srv, "GET", "/api/drive/folders/"+top.ID+"/zip", "", user, ""); out.Code != 413 || out.Header().Get("Content-Type") == "application/zip" {
		t.Fatalf("over cap %d %q", out.Code, out.Header().Get("Content-Type"))
	}
	cfg.Drive.MaxZipSize = 8 << 20

	// A member that cannot be read cuts the connection rather than ending a valid archive.
	blobs, _ := os.ReadDir(filepath.Join(cfg.Database.DataDir, "blobs"))
	for _, b := range blobs {
		os.Remove(filepath.Join(cfg.Database.DataDir, "blobs", b.Name()))
	}
	live := httptest.NewServer(srv)
	defer live.Close()
	req, _ := http.NewRequest("GET", live.URL+"/api/drive/folders/"+top.ID+"/zip", nil)
	req.AddCookie(user)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return // cut before the headers were flushed
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err == nil {
		if _, zerr := zip.NewReader(bytes.NewReader(body), int64(len(body))); zerr == nil {
			t.Fatal("broken folder served as a valid zip")
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
