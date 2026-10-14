package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/editor"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// editorFixture configures Euro-Office for the next server and returns a client holding its secret.
func editorFixture(t *testing.T) (*editor.Client, string) {
	t.Helper()
	t.Setenv("KYDRIVE_EDITOR_URL", "http://editor.test")
	t.Setenv("KYDRIVE_EDITOR_DRIVE_URL", "http://drive.test")
	t.Setenv("KYDRIVE_EDITOR_SECRET", strings.Repeat("s", 32))
	c, err := editor.FromEnv("http://drive.test", "development")
	if err != nil {
		t.Fatal(err)
	}
	return c, "http://drive.test"
}

func TestEditorDownloadNeedsEuroOfficeSignature(t *testing.T) {
	signer, origin := editorFixture(t)
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "boss", "admin")
	writer := loginAs(t, srv, st, "writer", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "writers", DisplayName: "Writers", Members: []string{"usr_writer"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_boss", "Team", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_boss", w.ID, "writers", "editor"); err != nil {
		t.Fatal(err)
	}
	var file struct{ ID string }
	out := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=a.docx&revision=0", "hello", writer, "")
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &file) != nil {
		t.Fatalf("upload %d %s", out.Code, out.Body.String())
	}
	var opened struct {
		Config struct {
			Document struct{ URL string } `json:"document"`
			Exp      int64                `json:"exp"`
		} `json:"config"`
	}
	out = send(t, srv, "POST", "/api/drive/files/"+file.ID+"/editor", "", writer, "")
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &opened) != nil {
		t.Fatalf("open %d %s", out.Code, out.Body.String())
	}
	url := opened.Config.Document.URL
	if !strings.HasPrefix(url, origin+"/api/editor/download/") || strings.Contains(url, "?") {
		t.Fatalf("download URL carries a credential: %s", url)
	}
	if exp := time.Until(time.Unix(opened.Config.Exp, 0)); exp <= 0 || exp > 10*time.Minute+time.Second {
		t.Fatalf("editor config lives %v", exp)
	}
	download := func(token string) int {
		req := httptest.NewRequest("GET", strings.TrimPrefix(url, origin), nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code == 200 && rec.Body.String() != "hello" {
			t.Fatalf("served %q", rec.Body.String())
		}
		return rec.Code
	}
	signed, err := signer.Sign(map[string]any{"payload": map[string]string{"url": url}, "exp": time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := signer.Sign(map[string]any{"payload": map[string]string{"url": url + "x"}})
	if got := download(""); got != 401 {
		t.Fatalf("unsigned download: %d", got)
	}
	if got := download(other); got != 401 {
		t.Fatalf("download signed for another URL: %d", got)
	}
	if got := download(signed); got != 200 {
		t.Fatalf("signed download: %d", got)
	}
	if out = send(t, srv, "POST", "/api/auth/logout", "", writer, ""); out.Code != 200 {
		t.Fatalf("logout %d", out.Code)
	}
	if got := download(signed); got != 403 {
		t.Fatalf("download after logout: %d", got)
	}
}

func TestTrashRetentionSetting(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	admin := loginAs(t, srv, st, "boss", "admin")
	member := loginAs(t, srv, st, "bob", "user")
	if out := send(t, srv, "GET", "/api/drive/trash-retention", "", admin, ""); out.Code != 200 || !strings.Contains(out.Body.String(), `"trash_days":90`) {
		t.Fatalf("default %d %s", out.Code, out.Body.String())
	}
	for _, body := range []string{`{"trash_days":-1}`, `{"trash_days":3651}`, `{}`, `{"trash_days":"5"}`} {
		if out := send(t, srv, "PUT", "/api/drive/trash-retention", body, admin, ""); out.Code != 400 {
			t.Fatalf("%s accepted: %d", body, out.Code)
		}
	}
	if out := send(t, srv, "PUT", "/api/drive/trash-retention", `{"trash_days":0}`, member, ""); out.Code != 403 {
		t.Fatalf("member changed the setting: %d", out.Code)
	}
	if out := send(t, srv, "PUT", "/api/drive/trash-retention", `{"trash_days":0}`, admin, ""); out.Code != 200 || !strings.Contains(out.Body.String(), `"trash_days":0`) {
		t.Fatalf("off %d %s", out.Code, out.Body.String())
	}
	if out := send(t, srv, "GET", "/api/drive/trash-retention", "", admin, ""); !strings.Contains(out.Body.String(), `"trash_days":0`) {
		t.Fatalf("not stored: %s", out.Body.String())
	}
}
