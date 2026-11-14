package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/config"
)

func TestKyPostPairingFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "kypost.secret")
	if err := os.WriteFile(secret, []byte(strings.Repeat("s", 32)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	short := filepath.Join(dir, "short.secret")
	if err := os.WriteFile(short, []byte("too short"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ url, file, refused string }{
		{"https://post.example/", secret, ""},
		{"https://post.example", "", "needs KYDRIVE_KYPOST_SECRET_FILE"},
		{"", secret, "KYDRIVE_KYPOST_URL"},
		{"http://post.example", secret, "KYDRIVE_KYPOST_URL"},
		{"https://post.example/mail", secret, "KYDRIVE_KYPOST_URL"},
		{"https://user@post.example", secret, "KYDRIVE_KYPOST_URL"},
		{"https://post.example", short, "at least 32 bytes"},
		{"https://post.example", filepath.Join(dir, "missing"), "KYDRIVE_KYPOST_SECRET_FILE"},
	}
	for _, c := range cases {
		t.Setenv("KY_DATA_DIR", t.TempDir())
		t.Setenv("KYDRIVE_KYPOST_URL", c.url)
		t.Setenv("KYDRIVE_KYPOST_SECRET_FILE", c.file)
		cfg, err := config.LoadFromEnv()
		if c.refused != "" {
			if err == nil || !strings.Contains(err.Error(), c.refused) {
				t.Errorf("%q %q: %v, want %q", c.url, c.file, err, c.refused)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Drive.KyPostURL != "https://post.example" || string(cfg.Drive.KyPostSecret) != strings.Repeat("s", 32) {
			t.Errorf("loaded %q %q", cfg.Drive.KyPostURL, cfg.Drive.KyPostSecret)
		}
	}
}
