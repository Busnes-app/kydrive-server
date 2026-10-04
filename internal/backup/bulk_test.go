package backup_test

import (
	"context"
	"encoding/hex"
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
	v, err := drive.WriteBlob(filepath.Join(dir, "blobs"), strings.NewReader("important bytes"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.Drive().Publish(ctx, "admin", drive.File{Workspace: w.ID, Name: "important.txt"}, v, 0); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Database: dbCfg, Security: config.SecurityConfig{EncryptionKey: key}, Server: config.ServerConfig{AppName: "KyDrive"}}
	if err = backup.InitBulkRepository(ctx, dir, key); err != nil {
		t.Fatal(err)
	}
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
	target := filepath.Join(t.TempDir(), "restored")
	if err = backup.RestoreBulk(ctx, opened, repo, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, v.Blob))
	if err != nil || string(got) != "important bytes" {
		t.Fatalf("restore: %q %v", got, err)
	}
	// Wrong restored key and missing repository must each fail before publishing files.
	os.WriteFile(filepath.Join(opened, "data/encryption.key"), []byte(hex.EncodeToString(make([]byte, 32))), 0600)
	if err = backup.RestoreBulk(ctx, opened, repo, filepath.Join(t.TempDir(), "bad")); err == nil {
		t.Fatal("wrong key accepted")
	}
	os.Remove(filepath.Join(dir, "blobs", v.Blob))
	if _, err = backup.Collect(ctx, cfg, "test"); err == nil {
		t.Fatal("missing live blob accepted as backup")
	}
}
