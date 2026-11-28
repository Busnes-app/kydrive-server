package api_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"filippo.io/age"
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

type vaultDocFixture struct {
	srv      *api.Server
	owner    *signer
	key      *hybridsig.PrivateKey
	identity *age.HybridIdentity
	doc      vault.Document
	cp       vault.Checkpoint
	binding  string
}

func (f vaultDocFixture) path() string {
	return "/api/vaults/" + f.doc.Vault + "/files/" + f.doc.File
}

func vaultJSON(in any) []byte {
	data, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	return data
}

func (f vaultDocFixture) encrypt(plain []byte) ([]byte, error) {
	var sealed bytes.Buffer
	w, err := age.Encrypt(&sealed, f.identity.Recipient())
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plain); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return sealed.Bytes(), nil
}

func (f vaultDocFixture) publish(payload []byte, revision int64) (*httptest.ResponseRecorder, error) {
	r, err := (vault.Revision{
		Document: f.doc, Number: revision, Size: int64(len(payload)), Digest: vault.Digest(payload), Binding: f.binding,
	}).Sign(f.key)
	if err != nil {
		return nil, err
	}
	enc := base64.RawURLEncoding
	req := f.owner.request("PUT", f.path()+"/versions/"+strconv.FormatInt(revision, 10), payload,
		"KyDrive-Vault-Revision", enc.EncodeToString(vaultJSON(r)), "KyDrive-Vault-Checkpoint", enc.EncodeToString(vaultJSON(f.cp)))
	return serve(f.srv, req), nil
}

func (f vaultDocFixture) bind(source int64, rooms []string, grants []vault.EditorGrant, blobs ...string) (vault.Binding, *httptest.ResponseRecorder, error) {
	b, err := (vault.Binding{Document: f.doc, Source: source, Rooms: rooms, Grants: grants, Blobs: blobs}).Sign(f.key)
	if err != nil {
		return b, nil, err
	}
	r := f.owner.request("PUT", f.path()+"/editor", vaultJSON(map[string]any{"binding": b, "checkpoint": f.cp}))
	return b, serve(f.srv, r), nil
}

func (f vaultDocFixture) lease(d *signer, edit bool) (string, int) {
	w := serve(f.srv, d.request("POST", f.path()+"/editor/session", vaultJSON(map[string]any{"checkpoint": f.cp, "edit": edit})))
	var out struct{ Token string }
	if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
		return "", 500
	}
	return out.Token, w.Code
}

func vaultRelayFixtureCheck(s *api.Server, token, room string, edit bool) int {
	r := httptest.NewRequest("POST", "/api/vault-relay/check", bytes.NewReader(vaultJSON(map[string]any{"room": room, "edit": edit})))
	r.Header.Set("Authorization", "Bearer "+token)
	return serve(s, r).Code
}

func (f vaultDocFixture) fetch(who *signer, rev string, expected int) ([]byte, error) {
	w := serve(f.srv, who.request("GET", f.path()+"/versions/"+rev, nil))
	if w.Code != expected {
		return nil, fmt.Errorf("download returned %d, want %d: %s", w.Code, expected, w.Body)
	}
	if w.Code != http.StatusOK {
		return nil, nil
	}
	encrypted := w.Body.Bytes()
	var signed vault.Revision
	raw, err := base64.RawURLEncoding.DecodeString(w.Header().Get("KyDrive-Vault-Revision"))
	if err != nil || json.Unmarshal(raw, &signed) != nil || signed.Verify(f.key.Public()) != nil ||
		signed.Org != f.doc.Org || signed.Vault != f.doc.Vault || signed.File != f.doc.File ||
		(rev != "latest" && strconv.FormatInt(signed.Number, 10) != rev) ||
		signed.Digest != vault.Digest(encrypted) || signed.Size != int64(len(encrypted)) {
		return nil, fmt.Errorf("invalid downloaded revision")
	}
	r, err := age.Decrypt(bytes.NewReader(encrypted), f.identity)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func (f vaultDocFixture) expectPublish(t *testing.T, payload []byte, revision int64, expected int) {
	t.Helper()
	w, err := f.publish(payload, revision)
	if err != nil {
		t.Fatal(err)
	}
	if w.Code != expected {
		t.Fatalf("publish revision %d returned %d, want %d: %s", revision, w.Code, expected, w.Body)
	}
}
