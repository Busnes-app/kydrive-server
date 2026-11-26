//go:build kye2e

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/applysetup"
)

const taggedBuild = true

func gateRefusalWant() string { return ".test" }

func TestGateMatchesBuildTag(t *testing.T) {
	if multiTenantBuild != taggedBuild {
		t.Fatalf("multiTenantBuild = %v, want %v in kye2e build", multiTenantBuild, taggedBuild)
	}
}

func TestE2EBuildOpensMultiTenantAndRequiresTestIssuer(t *testing.T) {
	setupMulti := func(t *testing.T, issuer string) {
		t.Helper()
		root := t.TempDir()
		data := filepath.Join(root, "data")
		identity := filepath.Join(root, "keys", "identity.key")
		writeKey(t, identity, strings.Repeat("34", 32))

		t.Setenv("KY_ENV", "production")
		t.Setenv("KY_DATA_DIR", data)
		t.Setenv("KY_DB_DRIVER", "postgres")
		t.Setenv("KY_DB_DSN", "postgres://drive:pw@db/drive")
		t.Setenv("KY_SESSION_SECRET", strings.Repeat("s", 32))
		t.Setenv("KY_SCIM_ENABLED", "false")
		t.Setenv("KYDRIVE_TENANCY", "multi")
		t.Setenv("KYDRIVE_STAFF_ORG", "00000000-0000-0000-0000-000000000099")
		t.Setenv("KY_ENCRYPTION_KEY", strings.Repeat("12", 32))
		t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", identity)
		t.Setenv("KY_KYSIGNON_ISSUER", issuer)
	}

	for _, good := range []string{"https://id.e2e.test", "https://ID.E2E.TEST:8443"} {
		t.Run("accepts_"+good, func(t *testing.T) {
			setupMulti(t, good)
			cfg, err := LoadFromEnv()
			if err != nil {
				t.Fatalf("LoadFromEnv: %v", err)
			}
			if cfg.Database.Tenancy != "multi" {
				t.Fatalf("Database.Tenancy = %q, want multi", cfg.Database.Tenancy)
			}
			if cfg.Database.StaffOrg != "00000000-0000-0000-0000-000000000099" {
				t.Fatalf("Database.StaffOrg = %q", cfg.Database.StaffOrg)
			}
			if cfg.SSO.KySignOnIssuer != good {
				t.Fatalf("KySignOnIssuer = %q, want %q", cfg.SSO.KySignOnIssuer, good)
			}
		})
	}

	for _, bad := range []string{
		"",
		"https://id.example",
		"http://id.e2e.test",
		"https://id.test.example",
		"https://test",
		"https://.test",
	} {
		t.Run("refuses_"+bad, func(t *testing.T) {
			setupMulti(t, bad)
			if cfg, err := LoadFromEnv(); err == nil || cfg != nil || !strings.Contains(err.Error(), ".test") {
				t.Fatalf("want .test refusal for %q, got cfg=%v err=%v", bad, cfg != nil, err)
			}
		})
	}

	t.Run("single_mode_refuses_real_issuer", func(t *testing.T) {
		t.Setenv("KY_DATA_DIR", t.TempDir())
		t.Setenv("KYDRIVE_TENANCY", "single")
		t.Setenv("KY_KYSIGNON_ISSUER", "https://id.example")
		if cfg, err := LoadFromEnv(); err == nil || cfg != nil || !strings.Contains(err.Error(), ".test") {
			t.Fatalf("want .test refusal in single mode, got cfg=%v err=%v", cfg != nil, err)
		}
	})

	t.Run("single_mode_allows_empty_or_test_issuer", func(t *testing.T) {
		for _, issuer := range []string{"", "https://id.e2e.test"} {
			t.Setenv("KY_DATA_DIR", t.TempDir())
			t.Setenv("KYDRIVE_TENANCY", "single")
			t.Setenv("KY_KYSIGNON_ISSUER", issuer)
			if _, err := LoadFromEnv(); err != nil {
				t.Fatalf("issuer %q: %v", issuer, err)
			}
		}
	})

	t.Run("installer_refuses_real_issuer", func(t *testing.T) {
		dir := t.TempDir()
		secret, token := filepath.Join(dir, "client"), filepath.Join(dir, "scim")
		for _, f := range []string{secret, token} {
			if err := os.WriteFile(f, []byte("private-"+filepath.Base(f)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		b := applysetup.Common{
			Schema:  applysetup.Schema,
			Service: "kydrive",
			SetupID: "271333e2-c221-4601-b249-563b3d828530",
			SSO: &applysetup.SSO{
				IssuerURL:        "https://identity.example.com",
				ClientID:         "drive",
				ClientSecretFile: secret,
				RedirectURI:      "https://drive.example.test/api/sso/kysignon/callback",
				SCIMTokenFile:    token,
			},
		}
		cfg := &Config{}
		cfg.Server.AppURL = "https://drive.example.test"
		cfg.SCIM.Enabled, cfg.SCIM.BearerToken = true, "private-scim"
		cfg.SSO.Enabled = true
		if err := ApplyInstallerConfiguration(cfg, b); err == nil || !strings.Contains(err.Error(), ".test") {
			t.Fatalf("want .test refusal from ApplyInstallerConfiguration, got %v", err)
		}
	})
}
