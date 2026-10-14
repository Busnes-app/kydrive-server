package backup_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/backup"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/google/uuid"
)

func TestBulkBackupRestoreAndMissingBytes(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic CLI required")
	}
	ctx := context.Background()
	dir := t.TempDir()
	repo := filepath.Join(t.TempDir(), "independent-repo")
	t.Setenv("KYDRIVE_BULK_BACKUP_REPOSITORY", repo)
	key := make([]byte, 32)
	key[0] = 12
	dbCfg := testdb.Config(t)
	if dbCfg.Driver != "sqlite" {
		t.Skip("drive SQLite-only")
	}
	dbCfg.DataDir = dir
	st, err := store.Open(ctx, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.Users().CreateUser(ctx, &store.User{ID: "admin", Username: "admin", Role: "admin", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	g := &store.Group{ID: "team", DisplayName: "Team", Members: []string{"admin"}}
	if err = st.Groups().ReplaceGroup(ctx, g, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "admin", "Team", 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "admin", w.ID, g.ID, "editor"); err != nil {
		t.Fatal(err)
	}
	// A plaintext blob from before sealing, as the release that wrote it left it.
	legacyBody := "legacy bytes"
	sum := sha256.Sum256([]byte(legacyBody))
	legacy := drive.Version{Blob: uuid.NewString(), Digest: hex.EncodeToString(sum[:]), Size: int64(len(legacyBody))}
	os.MkdirAll(filepath.Join(dir, "blobs"), 0700)
	if err = os.WriteFile(filepath.Join(dir, "blobs", legacy.Blob), []byte(legacyBody), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Drive().Publish(ctx, "admin", drive.File{Workspace: w.ID, Name: "legacy.txt"}, legacy, 0); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Database: dbCfg, Security: config.SecurityConfig{EncryptionKey: key}, Server: config.ServerConfig{AppName: "KyDrive"}}
	if err = backup.InitBulkRepository(ctx, dir, key); err != nil {
		t.Fatal(err)
	}
	open := func() (string, backup.BulkManifest) {
		t.Helper()
		payload, err := backup.Collect(ctx, cfg, "test")
		if err != nil {
			t.Fatal(err)
		}
		opened := t.TempDir()
		for _, f := range payload.Files {
			p := filepath.Join(opened, f.Path)
			os.MkdirAll(filepath.Dir(p), 0700)
			if err = os.WriteFile(p, f.Data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		var m backup.BulkManifest
		raw, _ := os.ReadFile(filepath.Join(opened, backup.BulkManifestPath))
		if err = json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return opened, m
	}
	setVersion := func(opened string, m backup.BulkManifest, version int) {
		m.Version = version
		raw, _ := json.Marshal(m)
		os.WriteFile(filepath.Join(opened, backup.BulkManifestPath), raw, 0600)
	}
	// A version 1 capsule (plaintext blobs only) still restores.
	opened, m := open()
	if m.Version != 2 || len(m.Files) != 1 || m.Files[0].Sealed {
		t.Fatalf("legacy manifest %+v", m)
	}
	setVersion(opened, m, 1)
	target := filepath.Join(t.TempDir(), "restored-v1")
	if err = backup.RestoreBulk(ctx, opened, repo, target); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(target, legacy.Blob)); err != nil || string(got) != legacyBody {
		t.Fatalf("v1 restore: %q %v", got, err)
	}

	v, err := drive.WriteBlob(filepath.Join(dir, "blobs"), key, strings.NewReader("important bytes"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Drive().Publish(ctx, "admin", drive.File{Workspace: w.ID, Name: "important.txt"}, v, 0); err != nil {
		t.Fatal(err)
	}
	opened, m = open()
	if m.Version != 2 || len(m.Files) != 2 {
		t.Fatalf("mixed manifest %+v", m)
	}
	target = filepath.Join(t.TempDir(), "restored")
	if err = backup.RestoreBulk(ctx, opened, repo, target); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(target, v.Blob))
	if err != nil || strings.Contains(string(raw), "important") || int64(len(raw)) != drive.SealedSize(v.Size) {
		t.Fatalf("sealed blob restored as %q %v", raw, err)
	}
	r, err := drive.OpenBlob(filepath.Join(target, v.Blob), key, true, v.Size)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(got) != "important bytes" {
		t.Fatalf("restore: %q %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(target, legacy.Blob)); err != nil || string(got) != legacyBody {
		t.Fatalf("legacy restore: %q %v", got, err)
	}
	// A version 1 manifest cannot carry sealed blobs.
	setVersion(opened, m, 1)
	if err = backup.RestoreBulk(ctx, opened, repo, filepath.Join(t.TempDir(), "v1-sealed")); err == nil {
		t.Fatal("version 1 manifest with sealed blobs accepted")
	}
	setVersion(opened, m, 2)
	// Wrong restored key and missing repository must each fail before publishing files.
	os.WriteFile(filepath.Join(opened, "data/encryption.key"), []byte(hex.EncodeToString(make([]byte, 32))), 0600)
	if err = backup.RestoreBulk(ctx, opened, repo, filepath.Join(t.TempDir(), "bad")); err == nil {
		t.Fatal("wrong key accepted")
	}
	// A sealed blob that fails authentication is never backed up.
	live := filepath.Join(dir, "blobs", v.Blob)
	raw, _ = os.ReadFile(live)
	raw[len(raw)-1] ^= 1
	os.WriteFile(live, raw, 0600)
	if _, err = backup.Collect(ctx, cfg, "test"); err == nil {
		t.Fatal("tampered sealed blob accepted as backup")
	}
	os.Remove(live)
	if _, err = backup.Collect(ctx, cfg, "test"); err == nil {
		t.Fatal("missing live blob accepted as backup")
	}
}
