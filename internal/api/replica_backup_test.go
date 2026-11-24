package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// A manual deposit is refused while another replica holds backup-run, and runs once it ends.
func TestManualDepositWaitsForOtherReplicasBackup(t *testing.T) {
	if !testdb.Postgres() {
		t.Skip("backup-run is a PostgreSQL session lock")
	}
	m := setupMultiServer(t)
	other, err := store.Open(t.Context(), m.cfg.Database, m.cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	held, release := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = other.WithReplicaLock(context.Background(), "backup-run", func(context.Context) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	w := m.call("staff", "", http.MethodPost, "/api/backup/deposit")
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "already in progress") {
		close(release)
		t.Fatalf("deposit during another replica's backup: %d %s", w.Code, w.Body.String())
	}
	close(release)
	// Free again, the deposit reaches the run itself: this install has nowhere to put a capsule.
	if w = m.call("staff", "", http.MethodPost, "/api/backup/deposit"); w.Code == http.StatusConflict {
		t.Fatalf("deposit after release: %d %s", w.Code, w.Body.String())
	}
}
