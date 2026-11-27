package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
	"github.com/google/uuid"
)

func TestReplicaConfigSingleModeNoop(t *testing.T) {
	// No store call or editor parsing in single mode, even with an invalid unused editor URL.
	t.Setenv("KYDRIVE_EDITOR_URL", "invalid")
	for _, mode := range []string{"", "single"} {
		cfg := &config.Config{Database: config.DatabaseConfig{Tenancy: mode}}
		if _, err := replicaConfig(t.Context(), cfg, nil, false); err != nil {
			t.Fatal(err)
		}
		if _, err := replicaConfig(t.Context(), cfg, nil, true); err == nil {
			t.Fatal("single-mode acceptance allowed")
		}
	}
}

func TestReplicaConfigCommandRejectsArguments(t *testing.T) {
	data := filepath.Join(t.TempDir(), "absent")
	t.Setenv("KY_DATA_DIR", data)
	for _, args := range [][]string{{"--help"}, {"--check"}, {"unexpected"}} {
		if err := acceptReplicaConfig(t.Context(), args, io.Discard); err == nil || !strings.Contains(err.Error(), "takes no arguments") {
			t.Fatalf("arguments %v reached deployment loading: %v", args, err)
		}
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatal("invalid command touched data directory", err)
	}
}

func TestRejectedReplicaCellsLeaveServingStateUntouched(t *testing.T) {
	if !testdb.Postgres() {
		t.Skip("requires PostgreSQL replica configuration")
	}
	ctx := t.Context()
	t.Setenv("KYDRIVE_EDITOR_URL", "")
	cfg := &config.Config{Database: testdb.Config(t)}
	cfg.Database.Tenancy = "multi"
	cfg.Database.StaffOrg = uuid.NewString()
	cfg.Database.Cells = []string{"e2e-1"}
	cfg.Security.EncryptionKey = testdb.Key
	cfg.Security.SessionSecret = strings.Repeat("s", 32)
	cfg.Server.AppURL = "https://drive.e2e.test"
	st, err := store.Open(ctx, cfg.Database, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := replicaConfig(ctx, cfg, st, false); err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	if _, err := st.Drive().ApplyOrgs(ctx, []suitedir.Org{{ID: org, HomeRegion: "e2e-1", Status: suitedir.OrgActive, Enabled: true, Revision: 9}}, 9); err != nil {
		t.Fatal(err)
	}
	changed := *cfg
	changed.Database = cfg.Database
	changed.Database.Cells = []string{"e2e-2"}
	other, err := store.Open(ctx, changed.Database, testdb.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err := replicaConfig(ctx, &changed, other, false); err == nil {
		t.Fatal("mismatched cells accepted")
	}
	if since, err := st.Drive().OrgsSince(ctx); err != nil || since != 9 {
		t.Fatal("rejected startup reset cursor", since, err)
	}
	if _, active, err := st.Drive().OrgActive(ctx, org); err != nil || !active {
		t.Fatal("rejected startup changed serving state", active, err)
	}
	if _, err := replicaConfig(ctx, &changed, other, true); err != nil {
		t.Fatal(err)
	}
	if _, active, err := st.Drive().OrgActive(ctx, org); err != nil || !active {
		t.Fatal("acceptance activated cells before restart", active, err)
	}
	if _, err := replicaConfig(ctx, &changed, other, false); err != nil {
		t.Fatal(err)
	}
	if since, err := st.Drive().OrgsSince(ctx); err != nil || since != 0 {
		t.Fatal("validated start did not reset cursor", since, err)
	}
	if _, active, err := st.Drive().OrgActive(ctx, org); err != nil || active {
		t.Fatal("dropped cell kept serving", active, err)
	}
}
