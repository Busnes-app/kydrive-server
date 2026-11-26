package config

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both server and operator startup must reject multi mode without creating or changing
// any key, directory or database. The last case still refuses the unfinished product.
func TestMultiKeyRefusalNeverCreatesFiles(t *testing.T) {
	for _, load := range []struct {
		name string
		fn   func() (*Config, error)
	}{{"server", LoadFromEnv}, {"operator", LoadExisting}} {
		for _, tc := range []struct {
			name    string
			prepare func(*testing.T, string, string)
			want    string
		}{
			{"default", func(t *testing.T, _, _ string) { t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", "") }, "KYDRIVE_IDENTITY_KEY_FILE"},
			{"no deployment key", func(t *testing.T, _, _ string) { t.Setenv("KY_ENCRYPTION_KEY", "") }, "KY_ENCRYPTION_KEY"},
			{"bad deployment key", func(t *testing.T, _, _ string) { t.Setenv("KY_ENCRYPTION_KEY", "secret-invalid-hex") }, "KY_ENCRYPTION_KEY"},
			{"missing seed", func(t *testing.T, _, identity string) { mustRemove(t, identity) }, "KYDRIVE_IDENTITY_KEY_FILE"},
			{"bad seed", func(t *testing.T, _, identity string) { writeKey(t, identity, "secret-invalid-hex") }, "KYDRIVE_IDENTITY_KEY_FILE"},
			{"symlink seed", func(t *testing.T, _, identity string) {
				target := identity + ".retained"
				if err := os.Rename(identity, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, identity); err != nil {
					t.Fatal(err)
				}
			}, "KYDRIVE_IDENTITY_KEY_FILE"},
			{"short seed", func(t *testing.T, _, identity string) { writeKey(t, identity, "abcd") }, "KYDRIVE_IDENTITY_KEY_FILE"},
			{"public seed", func(t *testing.T, _, identity string) {
				if err := os.Chmod(identity, 0644); err != nil {
					t.Fatal(err)
				}
			}, "KYDRIVE_IDENTITY_KEY_FILE"},
			{"local encryption copy", func(t *testing.T, data, _ string) {
				writeKey(t, filepath.Join(data, "encryption.key"), strings.Repeat("12", 32))
			}, "encryption.key"},
			{"local identity copy", func(t *testing.T, data, _ string) {
				writeKey(t, filepath.Join(data, "identity.key"), strings.Repeat("34", 32))
			}, "identity.key"},
			{"seed inside data", func(t *testing.T, data, _ string) {
				identity := filepath.Join(data, "keys", "device.key")
				writeKey(t, identity, strings.Repeat("34", 32))
				t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", identity)
			}, "outside KY_DATA_DIR"},
			{"external keys with closed startup gate", func(*testing.T, string, string) {}, gateRefusalWant()},
		} {
			t.Run(load.name+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				data, identity := filepath.Join(root, "new", "data"), filepath.Join(root, "keys", "identity.key")
				writeKey(t, identity, strings.Repeat("34", 32))
				t.Setenv("KY_ENV", "development")
				t.Setenv("KY_DATA_DIR", data)
				t.Setenv("KYDRIVE_TENANCY", "multi")
				t.Setenv("KY_ENCRYPTION_KEY", strings.Repeat("12", 32))
				t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", identity)
				tc.prepare(t, data, identity)
				before := keyTree(t, root)
				cfg, err := load.fn()
				if !maps.Equal(before, keyTree(t, root)) {
					t.Fatal("refused startup modified the key/data tree")
				}
				if err == nil || cfg != nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want refusal naming %s, got cfg=%v err=%v", tc.want, cfg != nil, err)
				}
				if strings.Contains(err.Error(), "secret-invalid-hex") || strings.Contains(err.Error(), strings.Repeat("12", 32)) || strings.Contains(err.Error(), strings.Repeat("34", 32)) {
					t.Fatal("key bytes in error")
				}
			})
		}
	}
}

func TestReplicaIdentityPathSeparation(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	writeKey(t, filepath.Join(data, "keys", "device.key"), strings.Repeat("34", 32))
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(data, alias); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "data-other", "device.key")
	writeKey(t, outside, strings.Repeat("34", 32))
	for _, tc := range []struct {
		name, data, identity string
		ok                   bool
	}{
		{"inside", data, filepath.Join(data, "keys", "device.key"), false},
		{"identity ancestor alias", data, filepath.Join(alias, "keys", "device.key"), false},
		{"data alias", alias, filepath.Join(data, "keys", "device.key"), false},
		{"fresh data under alias", filepath.Join(alias, "fresh"), filepath.Join(data, "fresh", "device.key"), false},
		{"sibling prefix", data, outside, true},
		{"fresh data", filepath.Join(root, "missing", "nested"), outside, true},
		{"relative", data, "device.key", false},
		{"parent traversal", data, alias + "/../data-other/device.key", false},
		{"data traversal", data + "/../data", outside, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", tc.identity)
			if err := replicaKeyPaths(tc.data); (err == nil) != tc.ok {
				t.Fatalf("path check: %v", err)
			}
		})
	}
	// Single mode still creates an absent externally configured seed once, then reads it
	// unchanged. Multi/operator reading uses the same loader with creation disabled.
	identity := filepath.Join(root, "external", "device.key")
	t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", identity)
	first, err := loadIdentityKey(data, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := loadIdentityKey(data, true)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatal("retained external identity changed", err)
	}
}

func TestInvalidTenancyDoesNotCreateKeys(t *testing.T) {
	data := filepath.Join(t.TempDir(), "missing")
	t.Setenv("KY_DATA_DIR", data)
	t.Setenv("KYDRIVE_TENANCY", "typo")
	if _, err := LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KYDRIVE_TENANCY") {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("invalid tenancy created data directory")
	}
}

func writeKey(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func keyTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		out[path] = info.Mode().String()
		if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] += string(raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
