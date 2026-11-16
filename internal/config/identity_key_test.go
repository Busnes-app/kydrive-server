package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityKeyIsCreatedOnceAndKept(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KY_DATA_DIR", dir)
	t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", "")
	first, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if first.Security.IdentityKey == nil {
		t.Fatal("no identity key created")
	}
	fi, err := os.Stat(filepath.Join(dir, "identity.key"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("identity.key mode %o, want 600", fi.Mode().Perm())
	}
	again, err := LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !first.Security.IdentityKey.Equal(again.Security.IdentityKey) {
		t.Fatal("a restart minted a new identity key")
	}
}

func TestIdentityKeyFileOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(t.TempDir(), "id.key")
	t.Setenv("KY_DATA_DIR", dir)
	t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", path)
	if _, err := LoadFromEnv(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the override path was not used", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "identity.key")); !os.IsNotExist(err) {
		t.Fatal("the default path was used despite the override")
	}
	t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", "relative.key")
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("a relative KYDRIVE_IDENTITY_KEY_FILE was accepted")
	}
}

// Operator commands never mint the key: one no device has learned need not exist.
func TestLoadExistingDoesNotCreateTheIdentityKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KY_DATA_DIR", dir)
	t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", "")
	t.Setenv("KY_ENCRYPTION_KEY", "")
	if err := os.WriteFile(filepath.Join(dir, "encryption.key"), []byte("0707070707070707070707070707070707070707070707070707070707070707\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ky_server.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadExisting()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Security.IdentityKey != nil {
		t.Fatal("LoadExisting returned a key")
	}
	if _, err := os.Stat(filepath.Join(dir, "identity.key")); !os.IsNotExist(err) {
		t.Fatal("LoadExisting created identity.key")
	}
}

// A damaged seed fails startup and is left in place: a new key would orphan every device's pin.
func TestIdentityKeyWrongLengthIsRefused(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KY_DATA_DIR", dir)
	t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", "")
	path := filepath.Join(dir, "identity.key")
	if err := os.WriteFile(path, []byte("abcd\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFromEnv(); err == nil {
		t.Fatal("a 2-byte seed was accepted")
	}
	if got, _ := os.ReadFile(path); string(got) != "abcd\n" {
		t.Fatalf("the seed file was rewritten: %q", got)
	}
}
