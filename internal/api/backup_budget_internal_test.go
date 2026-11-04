package api

import (
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
)

// A drill or export answers after its whole collection (and drill) budget, not a fixed 16
// minutes: a first bulk snapshot of a large drive takes longer than that.
func TestBackupWriteBudgetCoversCollectAndDrill(t *testing.T) {
	b := config.BackupConfig{CollectTimeout: 2 * time.Hour, DrillTimeout: 90 * time.Minute}
	if got, want := backupWriteBudget(b, false), 2*time.Hour+depositWriteBudget; got != want {
		t.Errorf("export/deposit budget %v, want %v", got, want)
	}
	if got, want := backupWriteBudget(b, true), 3*time.Hour+30*time.Minute+depositWriteBudget; got != want {
		t.Errorf("drill budget %v, want %v", got, want)
	}
}
