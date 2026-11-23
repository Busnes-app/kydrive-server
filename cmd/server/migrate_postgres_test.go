package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// migrate-postgres reads the SQLite deployment from its own settings and the target only
// from the environment.
func TestMigratePostgresCommand(t *testing.T) {
	ctx := context.Background()
	t.Setenv("KY_DATA_DIR", t.TempDir())
	t.Setenv("KY_APP_URL", "https://drive.example.test")
	t.Setenv("KY_SCIM_TOKEN", "private-scim-token")
	t.Setenv("KY_MIGRATE_POSTGRES_DSN", "")
	if err := migratePostgres(ctx, nil, io.Discard); err == nil || !strings.Contains(err.Error(), "KY_MIGRATE_POSTGRES_DSN") {
		t.Fatalf("no target: %v", err)
	}
	if !testdb.Postgres() {
		return
	}
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, cfg.Database, cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Users().CreateUser(ctx, &store.User{ID: "owner", Username: "owner", Role: "admin", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	t.Setenv("KY_MIGRATE_POSTGRES_DSN", testdb.Config(t).DSN)
	var out bytes.Buffer
	if err := migratePostgres(ctx, []string{"-check"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"users":1`) || !strings.Contains(out.String(), "no copied rows") {
		t.Fatalf("output: %s", out.String())
	}
}
