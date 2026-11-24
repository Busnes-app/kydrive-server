package main

import (
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
