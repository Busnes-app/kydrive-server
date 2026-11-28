package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

// The fixture process acts as the installed client. Only the fixture's Office
// bytes and editor capabilities enter this loopback controller. Its KyDrive
// requests carry signed ciphertext or a recipient-encrypted capability.
func runVaultBrowser(t *testing.T, dir string, first vaultDocFixture, collaborator *signer,
	grants func(string, bool) []vault.EditorGrant, rotate func(vaultDocFixture) vaultDocFixture) {
	t.Helper()
	relayAuthority := httptest.NewServer(first.srv)
	defer relayAuthority.Close()

	initial, err := os.ReadFile(filepath.Join(dir, "fixtures", "edit.docx"))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := first.encrypt(initial)
	if err != nil {
		t.Fatal(err)
	}
	first.expectPublish(t, ciphertext, 1, 204)
	original, err := first.fetch(first.owner, "1", 200)
	if err != nil || !bytes.Equal(initial, original) {
		t.Fatalf("vault import changed the source: %v", err)
	}
	f := first
	revision := int64(1)
	var mu sync.Mutex
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /source":
			w.Write(original)
		case "POST /bind":
			var in struct {
				URL   string   `json:"url"`
				Rooms []string `json:"rooms"`
			}
			if vault.Decode(http.MaxBytesReader(w, r.Body, 16<<10), &in) != nil {
				http.Error(w, "invalid fixture binding", 400)
				return
			}
			b, resp, err := f.bind(revision, in.Rooms, grants(in.URL, f.doc.Generation > 1))
			if err != nil || resp.Code != 204 {
				http.Error(w, fmt.Sprintf("fixture could not bind: %v %v", err, resp), 500)
				return
			}
			f.binding = b.ID()
			device := f.owner
			if f.doc.Generation == 1 {
				device = collaborator
			}
			token, status := f.lease(device, true)
			if status != 200 {
				http.Error(w, fmt.Sprintf("fixture could not open edit session: %d", status), 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
		case "POST /save":
			src, err := io.ReadAll(http.MaxBytesReader(w, r.Body, vault.MaxDocumentBytes-8192))
			if err != nil {
				http.Error(w, "invalid fixture export", 400)
				return
			}
			ciphertext, err := f.encrypt(src)
			if err != nil {
				http.Error(w, "fixture encryption failed", 500)
				return
			}
			if f.doc.Generation > 1 {
				if _, err := age.Decrypt(bytes.NewReader(ciphertext), first.identity); err == nil {
					http.Error(w, "old vault key opened new export", 500)
					return
				}
			}
			resp, err := f.publish(ciphertext, revision+1)
			if err != nil || resp.Code != 204 {
				http.Error(w, fmt.Sprintf("fixture could not publish: %v %v", err, resp), 500)
				return
			}
			revision++
			if resp, err := f.publish(ciphertext, revision-1); err != nil || resp.Code != 409 {
				http.Error(w, fmt.Sprintf("fixture allowed a stale revision: %v %v", err, resp), 500)
				return
			}
			got, err := f.fetch(f.owner, "latest", 200)
			if err != nil || !bytes.Equal(src, got) {
				http.Error(w, "fixture export failed verification", 500)
				return
			}
			w.Write(got)
		case "POST /renew":
			token, status := f.lease(f.owner, true)
			if status != 200 {
				http.Error(w, fmt.Sprintf("fixture could not renew session: %d", status), 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
		case "POST /rotate":
			f = rotate(f)
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	}))
	defer controller.Close()

	script, err := filepath.Abs("../../scripts/vault-docx-proof.mjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "KY_VAULT_FIXTURE_CONTROLLER="+controller.URL,
		"KY_VAULT_RELAY_AUTHORITY="+relayAuthority.URL+"/api/vault-relay/check")
	output, err := cmd.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatal(err)
	}
}
