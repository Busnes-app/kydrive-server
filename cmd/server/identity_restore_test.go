package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/ky-primitives/recoverykey"
	"github.com/Busnes-app/kydrive-server/internal/backup"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// A restore keeps the TLS pin identity key, so devices re-linked afterwards verify the same
// key and a statement signed by the restored server still verifies with the old public half.
func TestRestoreKeepsTheIdentityKey(t *testing.T) {
	ctx := context.Background()
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", "")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.IdentityKey == nil {
		t.Fatal("the server created no identity key")
	}
	st, err := store.Open(ctx, cfg.Database, cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := backup.Collect(ctx, cfg, "1.0.0", backup.PurposeExport)
	if closeErr := st.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	priv, err := recoverykey.Generate()
	if err != nil {
		t.Fatal(err)
	}
	shares, err := recoverykey.Split(priv, 2, 3)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := recoveryclient.Seal(payload, recoveryclient.RecoveryKey{Public: priv.Public(), Threshold: 2, TotalShares: 3})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "x.kycap")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := restore(path, target, cfg.Server.AppName, []string{shares[0].String(), shares[2].String()}, io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KY_DATA_DIR", filepath.Join(target, "data"))
	restored, err := config.LoadExisting()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored.Security.IdentityKey, cfg.Security.IdentityKey) {
		t.Fatal("the restored server has a different identity key; every device would need to re-link")
	}
}
