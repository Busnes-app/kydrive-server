package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// Uploads land sealed on disk; downloads and every Range across chunk boundaries return plaintext.
func TestSealedUploadDownloadRanges(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "sealadmin", "admin")
	user := loginAs(t, srv, st, "sealer", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "sealers", DisplayName: "Sealers", Members: []string{"usr_sealer"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_sealadmin", "Sealed", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_sealadmin", w.ID, "sealers", "editor"); err != nil {
		t.Fatal(err)
	}
	const chunk = 64 << 10
	plain := []byte(strings.Repeat("plaintext-marker-", (3*chunk+100)/17+1))[:3*chunk+100]
	out := send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/uploads?name=big.bin&revision=0", string(plain), user, "")
	var file struct{ ID string }
	if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &file) != nil {
		t.Fatalf("upload %d %s", out.Code, out.Body.String())
	}
	entries, _ := os.ReadDir(filepath.Join(cfg.Database.DataDir, "blobs"))
	if len(entries) != 1 {
		t.Fatalf("blobs on disk: %d", len(entries))
	}
	raw, _ := os.ReadFile(filepath.Join(cfg.Database.DataDir, "blobs", entries[0].Name()))
	if bytes.Contains(raw, []byte("plaintext-marker")) || int64(len(raw)) != drive.SealedSize(int64(len(plain))) {
		t.Fatal("plaintext on disk or unsealed size")
	}
	get := func(rng string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/drive/files/"+file.ID+"/download", nil)
		req.AddCookie(user)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	if out = get(""); out.Code != 200 || !bytes.Equal(out.Body.Bytes(), plain) {
		t.Fatalf("download %d, %d bytes", out.Code, out.Body.Len())
	}
	n := len(plain)
	var ranges [][2]int
	for _, b := range []int{0, chunk, 2 * chunk, 3 * chunk} {
		for _, start := range []int{b - 1, b, b + 1} {
			for _, end := range []int{start, start + 1, start + chunk, n - 1} {
				if start >= 0 && end >= start && end < n {
					ranges = append(ranges, [2]int{start, end})
				}
			}
		}
	}
	for _, r := range ranges {
		out = get(fmt.Sprintf("bytes=%d-%d", r[0], r[1]))
		if out.Code != 206 || !bytes.Equal(out.Body.Bytes(), plain[r[0]:r[1]+1]) {
			t.Fatalf("range %v: %d, %d bytes", r, out.Code, out.Body.Len())
		}
	}
	if out = get("bytes=-10"); out.Code != 206 || !bytes.Equal(out.Body.Bytes(), plain[n-10:]) {
		t.Fatalf("suffix range %d", out.Code)
	}
	if out = get(fmt.Sprintf("bytes=%d-", n)); out.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("unsatisfiable range %d", out.Code)
	}
	// Tampered bytes are never served.
	path := filepath.Join(cfg.Database.DataDir, "blobs", entries[0].Name())
	raw[len(raw)-1] ^= 1
	os.WriteFile(path, raw, 0600)
	if out = get(""); out.Code != 503 || bytes.Contains(out.Body.Bytes(), []byte("plaintext-marker")) {
		t.Fatalf("tampered blob served: %d", out.Code)
	}
	raw[len(raw)-1] ^= 1
	raw[100] ^= 1
	os.WriteFile(path, raw, 0600)
	if out = get(""); bytes.Contains(out.Body.Bytes(), []byte("plaintext-marker")) {
		t.Fatalf("tampered first chunk served: %d", out.Code)
	}
}
