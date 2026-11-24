package config

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestReplicaFingerprint(t *testing.T) {
	cfg := Config{Security: SecurityConfig{EncryptionKey: bytes.Repeat([]byte{7}, 32), SessionSecret: "secret-marker", IdentityKey: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{8}, 32))}}
	cfg.SSO.KySignOnSecret = "oidc-marker"
	original := ReplicaFingerprint(&cfg)
	raw, _ := json.Marshal(original)
	for _, secret := range []string{"secret-marker", "oidc-marker", string(cfg.Security.IdentityKey)} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("raw secret in fingerprint")
		}
	}
	for name, digest := range original {
		if b, err := hex.DecodeString(digest); err != nil || len(b) != 32 {
			t.Fatalf("bad digest for %s", name)
		}
	}
	// Host-local differences must not obstruct replica replacement.
	cfg.Database.DataDir = "/another/host"
	cfg.Database.DSN = "different host credentials"
	cfg.Database.MaxOpenConns = 99
	cfg.Server.Host = "other"
	cfg.Server.Port = 9999
	cfg.Backup.Dir = "/other/backups"
	cfg.Security.TLSCertFile = "/different/path"
	cfg.SCIM.BearerToken = "per-process-disabled-token"
	if !reflect.DeepEqual(original, ReplicaFingerprint(&cfg)) {
		t.Fatal("host-local config changed shared fingerprint")
	}
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"KY_SESSION_SECRET", func(c *Config) { c.Security.SessionSecret += "changed" }},
		{"KY_KYSIGNON_ISSUER", func(c *Config) { c.SSO.KySignOnIssuer = "https://new.example.test" }},
		{"KY_KYSIGNON_SECRET", func(c *Config) { c.SSO.KySignOnSecret += "changed" }},
		{"KY_KYSIGNON_HMAC_SECRET", func(c *Config) { c.SSO.KySignOnHMACSecret = "changed" }},
		{"KYDRIVE_IDENTITY_KEY_FILE", func(c *Config) { c.Security.IdentityKey = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, 32)) }},
		{"KYDRIVE_KYPOST_SECRET_FILE", func(c *Config) { c.Drive.KyPostSecret = []byte("changed") }},
		{"KYDRIVE_KYPOST_URL", func(c *Config) { c.Drive.KyPostURL = "https://post.example.test" }},
		{"KYDRIVE_TENANCY", func(c *Config) { c.Database.Tenancy = "multi" }},
		{"KYDRIVE_STAFF_ORG", func(c *Config) { c.Database.StaffOrg = "new-org" }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			other := cfg
			tt.change(&other)
			if got := DiffFingerprint(original, ReplicaFingerprint(&other)); !slices.Equal(got, []string{tt.name}) {
				t.Fatalf("changed fields %v", got)
			}
		})
	}
	other := cfg
	other.Security.EncryptionKey = bytes.Repeat([]byte{9}, 32)
	if got := DiffFingerprint(original, ReplicaFingerprint(&other)); !slices.Equal(got, []string{EncryptionKeyField}) {
		t.Fatal(got)
	}
	if ReplicaField(cfg.Security.EncryptionKey, "A", "same") == ReplicaField(cfg.Security.EncryptionKey, "B", "same") {
		t.Fatal("labels not separated")
	}
	if strings.Contains(string(raw), "another/host") {
		t.Fatal("path leaked")
	}
}
