//go:build !kye2e

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

const taggedBuild = false

func gateRefusalWant() string { return "multi is not available" }

func TestGateMatchesBuildTag(t *testing.T) {
	if multiTenantBuild != taggedBuild {
		t.Fatalf("multiTenantBuild = %v, want %v in release build", multiTenantBuild, taggedBuild)
	}
}

func TestReleaseBuildRefusesMultiTenant(t *testing.T) {
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
	t.Setenv("KY_KYSIGNON_ISSUER", "https://id.e2e.test")

	for _, load := range []struct {
		name string
		fn   func() (*Config, error)
	}{
		{"LoadFromEnv", LoadFromEnv},
		{"LoadExisting", LoadExisting},
	} {
		t.Run(load.name, func(t *testing.T) {
			cfg, err := load.fn()
			if err == nil || cfg != nil {
				t.Fatalf("want multi-tenant refusal, got cfg=%v err=%v", cfg != nil, err)
			}
			const want = "KYDRIVE_TENANCY=multi is not available in this release"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("got %q, want %q", err.Error(), want)
			}
		})
	}
}
