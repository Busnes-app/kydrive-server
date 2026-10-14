package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/Busnes-app/ky-primitives/capsule"
	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"golang.org/x/sys/unix"
)

var ErrDrillBusy = errors.New("backup: a restore drill is already running")

// bulkLock serializes every use of the bulk repository from this data directory, across
// processes, so a prune never overlaps a backup or a drill restore.
const bulkLock = "bulk.lock"

// lockFile takes an exclusive advisory lock on <dataDir>/name; without wait, contention
// returns unix.EWOULDBLOCK. Lock files are never unlinked: every process must lock the same
// inode. Closing the file (including process exit) releases the lock.
func lockFile(dataDir, name string, wait bool) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dataDir, name), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	how := unix.LOCK_EX
	if !wait {
		how |= unix.LOCK_NB
	}
	if err := unix.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// RunDrill serializes HTTP and CLI drills sharing a data directory, and holds the bulk lock
// across the drill so its restore never meets a prune.
func RunDrill(ctx context.Context, cfg *config.Config, payload recoveryclient.Payload) (*recoveryclient.DrillResult, error) {
	lock, err := lockFile(cfg.Database.DataDir, "drill.lock", false)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return nil, ErrDrillBusy
	}
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	root := DrillRoot(cfg)
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	if err := os.Chmod(root, 0700); err != nil {
		return nil, err
	}
	bulk, err := lockFile(cfg.Database.DataDir, bulkLock, true)
	if err != nil {
		return nil, err
	}
	defer bulk.Close()
	if cfg.Backup.DrillTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Backup.DrillTimeout)
		defer cancel()
	}
	return recoveryclient.Drill(ctx, root, payload, func(dir string, opened capsule.Manifest) []recoveryclient.Check {
		return checks(ctx, dir, opened)
	})
}
