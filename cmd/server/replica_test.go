package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/config"
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
