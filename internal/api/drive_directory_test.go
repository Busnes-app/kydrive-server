package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
)

// The group picker lists every group, not just the first page.
func TestDirectoryListsEveryGroup(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	boss := loginAs(t, srv, st, "boss", "admin")
	for i := range 450 {
		if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: fmt.Sprintf("g%03d", i), DisplayName: fmt.Sprintf("Group %03d", i)}, true); err != nil {
			t.Fatal(err)
		}
	}
	w := send(t, srv, "GET", "/api/drive/directory", "", boss, "")
	var out struct {
		Groups []struct {
			ID string `json:"id"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); w.Code != 200 || err != nil {
		t.Fatalf("directory: %d %v %s", w.Code, err, w.Body.String())
	}
	if len(out.Groups) != 450 || out.Groups[449].ID != "g449" {
		t.Fatalf("directory lists %d groups, want 450", len(out.Groups))
	}
}
