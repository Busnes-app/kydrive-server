package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDriveServiceAPIScopeAndStreamingUpload(t *testing.T) {
	s, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	if err := st.Users().CreateUser(ctx, &store.User{ID: "drive-service", Username: "drive-service", Role: "admin", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	g := &store.Group{ID: "drive-group", DisplayName: "Drive Group", Members: []string{"drive-service"}}
	if err := st.Groups().ReplaceGroup(ctx, g, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "drive-service", "Files", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "drive-service", w.ID, g.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", 64)
	id, err := st.Drive().CreateServiceToken(ctx, "drive-service", w.ID, "Automation", "editor", token)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=large.bin&revision=0", bytes.NewReader(bytes.Repeat([]byte("x"), 2<<20)))
	r.Header.Set("Authorization", "Bearer "+token)
	out := httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if out.Code != 201 {
		t.Fatalf("streaming upload %d %s", out.Code, out.Body.String())
	}
	var file struct {
		ID   string `json:"id"`
		Size int64  `json:"size"`
	}
	if err = json.Unmarshal(out.Body.Bytes(), &file); err != nil || file.Size != 2<<20 {
		t.Fatal(err, file)
	}
	other, err := st.Drive().CreateWorkspace(ctx, "drive-service", "Other", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "drive-service", other.ID, g.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/drive/workspaces/" + other.ID + "/files", "/api/drive/workspaces/" + w.ID + "/grants", "/api/drive/audit"} {
		r = httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		out = httptest.NewRecorder()
		s.ServeHTTP(out, r)
		if out.Code == 200 {
			t.Fatalf("service scope bypass %s", path)
		}
	}
	if err = st.Drive().RevokeServiceToken(ctx, "drive-service", id); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/api/drive/files/"+file.ID+"/download", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	out = httptest.NewRecorder()
	s.ServeHTTP(out, r)
	if out.Code != 401 {
		t.Fatal(out.Code)
	}
}
