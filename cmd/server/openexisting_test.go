package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// CLI backups seal what the server runs with: the installer-supplied SSO secret and backup
// directory, not just the environment.
func TestOpenExistingAppliesInstallerConfiguration(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	t.Setenv("KY_DATA_DIR", data)
	t.Setenv("KY_APP_URL", "https://drive.example.test")
	t.Setenv("KY_SCIM_TOKEN", "private-scim-token")
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, cfg.Database, cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	secret, token := filepath.Join(data, "client.secret"), filepath.Join(data, "scim.secret")
	for path, v := range map[string]string{secret: "private-oidc-secret", token: "private-scim-token"} {
		if err := os.WriteFile(path, []byte(v), 0600); err != nil {
			t.Fatal(err)
		}
	}
	backups := filepath.Join(t.TempDir(), "backups")
	raw, _ := json.Marshal(applysetup.Common{Schema: applysetup.Schema, Service: "kydrive", SetupID: "271333e2-c221-4601-b249-563b3d828530",
		SSO:    &applysetup.SSO{IssuerURL: "https://identity.example.test", ClientID: "drive", ClientSecretFile: secret, RedirectURI: "https://drive.example.test/api/sso/kysignon/callback", SCIMTokenFile: token},
		Backup: &applysetup.Backup{Directory: backups}})
	if err := st.Settings().SetSetting(ctx, store.InstallerConfigKey, string(raw)); err != nil {
		t.Fatal(err)
	}
	st.Close()

	cfg, st, err = openExisting(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if cfg.SSO.KySignOnSecret != "private-oidc-secret" || cfg.SSO.KySignOnClientID != "drive" || cfg.Backup.Dir != backups {
		t.Fatalf("installer configuration not applied: client %q, backups %q", cfg.SSO.KySignOnClientID, cfg.Backup.Dir)
	}
}
