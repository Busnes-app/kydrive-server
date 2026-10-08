package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func send(t *testing.T, srv *api.Server, method, path, body string, cookie *http.Cookie, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "test-csrf"})
		req.Header.Set(auth.HeaderCSRF, "test-csrf")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestFileManagementRoutes(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	boss := loginAs(t, srv, st, "boss", "admin")
	mover := loginAs(t, srv, st, "mover", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "movers", DisplayName: "Movers", Members: []string{"usr_mover"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_boss", "Team", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_boss", w.ID, "movers", "manager"); err != nil {
		t.Fatal(err)
	}
	blobCount := func() int {
		entries, _ := os.ReadDir(filepath.Join(cfg.Database.DataDir, "blobs"))
		n := 0
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), ".") {
				n++
			}
		}
		return n
	}
	var file struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Revision int64  `json:"revision"`
	}
	out := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=a.txt&revision=0", "hello", mover, "")
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &file) != nil {
		t.Fatalf("upload %d %s", out.Code, out.Body.String())
	}
	body, _ := json.Marshal(map[string]any{"revision": 1, "workspace": w.ID, "parent": "", "name": "renamed.txt"})
	if out = send(t, srv, "PUT", "/api/drive/files/"+file.ID+"/location", string(body), mover, ""); out.Code != 200 || !strings.Contains(out.Body.String(), "renamed.txt") {
		t.Fatalf("rename %d %s", out.Code, out.Body.String())
	}
	body, _ = json.Marshal(map[string]any{"workspace": w.ID, "parent": "", "name": "copy.txt"})
	if out = send(t, srv, "POST", "/api/drive/files/"+file.ID+"/copy", string(body), mover, ""); out.Code != 201 {
		t.Fatalf("copy %d %s", out.Code, out.Body.String())
	}
	if out = send(t, srv, "DELETE", "/api/drive/files/"+file.ID, "", mover, ""); out.Code != 409 {
		t.Fatalf("purge of a live file: %d", out.Code)
	}
	if out = send(t, srv, "PUT", "/api/drive/files/"+file.ID+"/trash", `{"revision":1,"trashed":true}`, mover, ""); out.Code != 200 {
		t.Fatalf("trash %d", out.Code)
	}
	token := strings.Repeat("m", 64)
	if _, err = st.Drive().CreateServiceToken(ctx, "usr_mover", w.ID, "Automation", "editor", token); err != nil {
		t.Fatal(err)
	}
	if out = send(t, srv, "DELETE", "/api/drive/files/"+file.ID, "", nil, token); out.Code != 403 {
		t.Fatalf("service token purged: %d", out.Code)
	}
	if out = send(t, srv, "DELETE", "/api/drive/files/"+file.ID, "", mover, ""); out.Code != 200 {
		t.Fatalf("purge %d %s", out.Code, out.Body.String())
	}
	if n := blobCount(); n != 1 {
		t.Fatalf("shared blob removed while the copy still uses it: %d", n)
	}
	var folder struct{ ID string }
	out = send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/folders", `{"parent":"","name":"Old"}`, mover, "")
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &folder) != nil {
		t.Fatalf("folder %d %s", out.Code, out.Body.String())
	}
	if out = send(t, srv, "PUT", "/api/drive/folders/"+folder.ID+"/location", `{"parent":"","name":"Archive"}`, mover, ""); out.Code != 200 {
		t.Fatalf("folder rename %d", out.Code)
	}
	if out = send(t, srv, "PUT", "/api/drive/folders/"+folder.ID+"/trash", `{"trashed":true}`, mover, ""); out.Code != 200 {
		t.Fatalf("folder trash %d", out.Code)
	}
	if out = send(t, srv, "GET", "/api/drive/workspaces/"+w.ID+"/folders?trash=true", "", mover, ""); !strings.Contains(out.Body.String(), "Archive") {
		t.Fatalf("trashed folders %s", out.Body.String())
	}
	if out = send(t, srv, "DELETE", "/api/drive/folders/"+folder.ID, "", mover, ""); out.Code != 200 {
		t.Fatalf("folder purge %d", out.Code)
	}
	if out = send(t, srv, "PUT", "/api/drive/workspaces/"+w.ID+"/retention", `{"trash_days":30,"keep_versions":10}`, mover, ""); out.Code != 403 {
		t.Fatalf("non-admin set retention: %d", out.Code)
	}
	if out = send(t, srv, "PUT", "/api/drive/workspaces/"+w.ID+"/retention", `{"trash_days":30,"keep_versions":10}`, boss, ""); out.Code != 200 {
		t.Fatalf("retention %d %s", out.Code, out.Body.String())
	}
	if out = send(t, srv, "PUT", "/api/drive/workspaces/"+w.ID+"/retention", `{"trash_days":30,"keep_versions":10,"extra":1}`, boss, ""); out.Code != 400 {
		t.Fatalf("unknown field accepted: %d", out.Code)
	}
}
