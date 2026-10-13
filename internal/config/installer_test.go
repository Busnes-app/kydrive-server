package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/ky-primitives/applysetup"
)

func TestInstallerRefusesToEnableOperatorDisabledSSO(t *testing.T) {
	dir := t.TempDir()
	secret, token := filepath.Join(dir, "client"), filepath.Join(dir, "scim")
	for _, f := range []string{secret, token} {
		if err := os.WriteFile(f, []byte("private-"+filepath.Base(f)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := applysetup.Common{Schema: applysetup.Schema, Service: "kydrive", SetupID: "271333e2-c221-4601-b249-563b3d828530",
		SSO: &applysetup.SSO{IssuerURL: "https://identity.example.test", ClientID: "drive", ClientSecretFile: secret, RedirectURI: "https://drive.example.test/api/sso/kysignon/callback", SCIMTokenFile: token}}
	for _, enabled := range []bool{true, false} {
		cfg := &Config{}
		cfg.Server.AppURL = "https://drive.example.test"
		cfg.SCIM.Enabled, cfg.SCIM.BearerToken = true, "private-scim"
		cfg.SSO.Enabled = enabled
		err := ApplyInstallerConfiguration(cfg, b)
		if enabled && (err != nil || cfg.SSO.KySignOnIssuer == "") {
			t.Fatal("default-enabled SSO not configured", err)
		}
		if !enabled && (err == nil || cfg.SSO.Enabled || cfg.SSO.KySignOnIssuer != "") {
			t.Fatal("installer enabled SSO the operator disabled", err)
		}
	}
}
