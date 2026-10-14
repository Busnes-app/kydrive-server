package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
)

func TestConfigLoadDefaults(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Database.Driver != "sqlite" {
		t.Errorf("expected default driver sqlite, got %s", cfg.Database.Driver)
	}
	if cfg.Captcha.Provider != "pow" {
		t.Errorf("expected default captcha provider pow, got %s", cfg.Captcha.Provider)
	}
	if cfg.Backup.Dir != "" {
		t.Errorf("expected empty default backup dir (sealed local copies off), got %q", cfg.Backup.Dir)
	}
}

func TestConfigLoadFromEnvOverrides(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_PORT", "9090")
	t.Setenv("KY_DB_DRIVER", "postgres")
	t.Setenv("KY_DB_DSN", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("KY_APP_NAME", "CustomBusnesApp")
	t.Setenv("KY_CAPTCHA_PROVIDER", "turnstile")

	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Database.Driver != "postgres" {
		t.Errorf("expected driver postgres, got %s", cfg.Database.Driver)
	}
	if cfg.Database.DSN != "postgres://user:pass@localhost:5432/testdb" {
		t.Errorf("expected custom DSN, got %s", cfg.Database.DSN)
	}
	if cfg.Server.AppName != "CustomBusnesApp" {
		t.Errorf("expected custom app name, got %s", cfg.Server.AppName)
	}
	if cfg.Captcha.Provider != "turnstile" {
		t.Errorf("expected captcha provider turnstile, got %s", cfg.Captcha.Provider)
	}
}

func TestEncryptionKeyPersistsAcrossLoads(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_ENCRYPTION_KEY", "")
	a, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	b, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Security.EncryptionKey) != 32 || !bytes.Equal(a.Security.EncryptionKey, b.Security.EncryptionKey) {
		t.Fatal("encryption key was not persisted between loads")
	}
	if !a.Security.KeyInDataDir {
		t.Fatal("keyfile in the data directory not reported")
	}
	t.Setenv("KY_ENCRYPTION_KEY", strings.Repeat("ab", 32))
	if c, err := config.LoadFromEnv(); err != nil || !c.Security.KeyInDataDir {
		t.Fatalf("leftover keyfile not reported with an environment key: %v", err)
	}
	if err = os.Remove(filepath.Join(os.Getenv("KY_DATA_DIR"), "encryption.key")); err != nil {
		t.Fatal(err)
	}
	if c, err := config.LoadFromEnv(); err != nil || c.Security.KeyInDataDir {
		t.Fatalf("environment key reported as in the data directory: %v", err)
	}
	// An interrupted drill leaves an opened capsule, deployment key included, under drill/.
	residue := filepath.Join(os.Getenv("KY_DATA_DIR"), "drill", "drill-interrupted")
	if err = os.MkdirAll(residue, 0o700); err != nil {
		t.Fatal(err)
	}
	if c, err := config.LoadFromEnv(); err != nil || !c.Security.KeyInDataDir {
		t.Fatalf("drill residue not reported: %v", err)
	}
	if err = os.RemoveAll(residue); err != nil {
		t.Fatal(err)
	}
	if c, err := config.LoadFromEnv(); err != nil || c.Security.KeyInDataDir {
		t.Fatalf("empty drill directory reported: %v", err)
	}
}

func TestEncryptionKeyFromEnvMustBe32Bytes(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_ENCRYPTION_KEY", "deadbeef")
	if _, err := config.LoadFromEnv(); err == nil {
		t.Fatal("8-byte key accepted")
	}
}

func TestDepositIntervalFromEnv(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 24 * time.Hour, true},
		{"90m", 90 * time.Minute, true},
		{"15m", 15 * time.Minute, true},
		{"0", 0, true},
		{"1s", 0, false},
		{"14m", 0, false},
		{"-1h", 0, false},
		{"daily", 0, false},
	} {
		t.Setenv("KY_BACKUP_DEPOSIT_INTERVAL", tc.in)
		cfg, err := config.LoadFromEnv()
		if (err == nil) != tc.ok {
			t.Errorf("%q: err=%v, want ok=%v", tc.in, err, tc.ok)
			continue
		}
		if tc.ok && cfg.Backup.DepositInterval != tc.want {
			t.Errorf("%q: got %v, want %v", tc.in, cfg.Backup.DepositInterval, tc.want)
		}
	}
}

func TestBackupConfigFromEnv(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_BACKUP_DIR", "/tmp/x")
	t.Setenv("KY_BACKUP_KEEP", "3")
	t.Setenv("KY_BACKUP_ALLOW_PRIVATE_RECOVERY", "true")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backup.Dir != "/tmp/x" || cfg.Backup.Keep != 3 || !cfg.Backup.AllowPrivateRecovery {
		t.Fatalf("%+v", cfg.Backup)
	}
}

func TestBackupKeepBelowOneIsRefused(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_BACKUP_KEEP", "0")
	if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KY_BACKUP_KEEP") {
		t.Fatalf("want KY_BACKUP_KEEP error, got %v", err)
	}
}

func TestExcalidrawFontsDirMustExist(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	t.Setenv("KY_EXCALIDRAW_FONTS_DIR", dir)
	cfg, err := config.LoadFromEnv()
	if err != nil || cfg.Server.ExcalidrawFontsDir != dir {
		t.Fatalf("cfg=%+v err=%v", cfg, err)
	}
	t.Setenv("KY_EXCALIDRAW_FONTS_DIR", dir+"/missing")
	if _, err := config.LoadFromEnv(); err == nil {
		t.Fatal("missing fonts directory accepted")
	}
}

func TestUnparseableValuesAreRefusedByName(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"KY_PORT", "80a"},
		{"KY_DB_MAX_OPEN_CONNS", "many"},
		{"KY_CAPTCHA_POW_DIFFICULTY", "1e5"},
		{"KY_BACKUP_KEEP", "seven"},
		{"KY_COOKIE_SECURE", "ture"},
		{"KY_SSO_AUTO_PROVISION", "enabled"},
		{"KY_SCIM_ENABLED", "2"},
	} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv("KY_DATA_DIR", t.TempDir())
			t.Setenv(tc.key, tc.value)
			if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("%s=%q: %v", tc.key, tc.value, err)
			}
		})
	}
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_COOKIE_SECURE", "off")
	if cfg, err := config.LoadFromEnv(); err != nil || cfg.Security.CookieSecure {
		t.Fatalf("explicit off: %v", err)
	}
}

func TestEnvironmentMustBeKnown(t *testing.T) {
	t.Setenv("KY_DATA_DIR", t.TempDir())
	for _, env := range []string{"prod", "Production", "staging"} {
		t.Setenv("KY_ENV", env)
		if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "KY_ENV") {
			t.Fatalf("KY_ENV=%s: %v", env, err)
		}
	}
}

func TestExamplePlaceholdersRefuseToStart(t *testing.T) {
	for _, key := range []string{"KY_SESSION_SECRET", "KY_KYSIGNON_SECRET", "KY_SCIM_TOKEN", "KYDRIVE_EDITOR_SECRET", "KY_ADMIN_PASSWORD"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("KY_DATA_DIR", t.TempDir())
			t.Setenv(key, "replace-with-long-random-value")
			if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("%s placeholder accepted: %v", key, err)
			}
		})
	}
}

func TestUnreadableDrillDirectoryFailsStartup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KY_DATA_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "drill"), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), "drill") {
		t.Fatalf("unreadable drill directory ignored: %v", err)
	}
}

func TestDrillAndCollectSettings(t *testing.T) {
	data := t.TempDir()
	t.Setenv("KY_DATA_DIR", data)
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backup.DrillDir != filepath.Join(data, "drill") || cfg.Backup.DrillTimeout != 2*time.Hour || cfg.Backup.CollectTimeout != 2*time.Hour {
		t.Fatalf("defaults: %+v", cfg.Backup)
	}
	drill := t.TempDir()
	t.Setenv("KY_DRILL_DIR", drill)
	t.Setenv("KY_DRILL_TIMEOUT", "6h")
	t.Setenv("KY_BACKUP_COLLECT_TIMEOUT", "90m")
	if cfg, err = config.LoadFromEnv(); err != nil || cfg.Backup.DrillDir != drill || cfg.Backup.DrillTimeout != 6*time.Hour || cfg.Backup.CollectTimeout != 90*time.Minute || cfg.Security.KeyInDataDir != true {
		t.Fatalf("overrides: %+v %v", cfg, err)
	}
	// Residue of an interrupted drill in KY_DRILL_DIR carries the key too.
	os.Remove(filepath.Join(data, "encryption.key"))
	t.Setenv("KY_ENCRYPTION_KEY", strings.Repeat("ab", 32))
	if cfg, err = config.LoadFromEnv(); err != nil || cfg.Security.KeyInDataDir {
		t.Fatalf("empty drill dir: %v", err)
	}
	if err = os.Mkdir(filepath.Join(drill, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	if cfg, err = config.LoadFromEnv(); err != nil || !cfg.Security.KeyInDataDir {
		t.Fatalf("drill residue in KY_DRILL_DIR not flagged: %v", err)
	}
	for key, value := range map[string]string{"KY_DRILL_DIR": "relative/drill", "KY_DRILL_TIMEOUT": "30s", "KY_BACKUP_COLLECT_TIMEOUT": "soon"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, value)
			if _, err := config.LoadFromEnv(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("%s=%q: %v", key, value, err)
			}
		})
	}
}

// Operator commands on an existing deployment never mint a key or a database.
func TestLoadExistingRefusesAMissingDeployment(t *testing.T) {
	data := t.TempDir()
	t.Setenv("KY_DATA_DIR", data)
	key := filepath.Join(data, "encryption.key")
	if _, err := config.LoadExisting(); err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("missing key: %v", err)
	}
	if _, err := os.Lstat(key); !os.IsNotExist(err) {
		t.Fatal("LoadExisting created a key")
	}
	if _, err := config.LoadFromEnv(); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(data, "ky_server.db")
	if _, err := config.LoadExisting(); err == nil || !strings.Contains(err.Error(), db) {
		t.Fatalf("missing database: %v", err)
	}
	if err := os.WriteFile(db, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadExisting(); err != nil {
		t.Fatal(err)
	}
}
