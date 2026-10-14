package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/backup"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// A deployment key that cannot seal is a configuration fault, not a run that might succeed
// next minute: the scheduler says so once and stops. Retrying would write a log line and an
// audit row every tick forever, because a RunConfig failure never reaches the point where
// recoveryclient.Run stamps the attempt that moves NextRun along.
func TestBackupLoopStopsWhenTheSealerCannotBeBuilt(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.EncryptionKey = []byte("too short")

	done := make(chan struct{})
	// A nil store is safe only because the loop must return before it reads one.
	go backupLoop(context.Background(), cfg, nil, done)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("backupLoop did not give up on an unbuildable RunConfig")
	}
}

// Shutdown waits on done before the store closes, so the loop must close it: a loop that
// never signalled would hang the process, and one that signalled early would put us back to
// killing a deposit mid-flight. done is closed only where the loop returns, which is between
// runs.
func TestBackupLoopClosesDoneOnCancel(t *testing.T) {
	cfg := &config.Config{}
	cfg.Security.EncryptionKey = make([]byte, 32)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go backupLoop(ctx, cfg, nil, done)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("backupLoop did not close done after its context was cancelled")
	}
}

// The shutdown guarantee is only as good as the grace period the deployment grants: the HTTP
// drain and the backup wait both have to finish inside it, or the supervisor's SIGKILL lands on
// a capsule mid-upload -- the exact outcome the wait exists to prevent. This holds the compose
// file and the two constants in step.
func TestComposeGracePeriodCoversTheShutdownBudget(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^\s*stop_grace_period:\s*(\S+)\s*$`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("docker-compose.yml sets no stop_grace_period, so Docker's default 10s kills an in-flight deposit")
	}
	grace, err := time.ParseDuration(string(m[1]))
	if err != nil {
		t.Fatalf("stop_grace_period %q: %v", m[1], err)
	}
	if budget := shutdownTimeout + backupWaitTimeout; grace <= budget {
		t.Errorf("stop_grace_period %s does not cover shutdownTimeout+backupWaitTimeout (%s)", grace, budget)
	}
}

// Both wait phases share one context, not one timer channel: a timer channel delivers its value
// once, so a scheduler wait that consumed it would leave the handler wait unbounded -- the stuck
// deposit this whole path exists to bound. Neither channel here ever closes, so the only way out
// is the deadline, twice.
func TestWaitForBackupWorkIsBoundedInBothPhases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	returned := make(chan struct{})
	go func() {
		defer close(returned)
		waitForBackupWork(ctx, make(chan struct{}), func() { select {} })
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForBackupWork did not return; the second phase outlived the shared deadline")
	}
}

// The two waits run concurrently, not one after the other. A scheduled deposit that hangs eats
// the whole shared budget, and a sequential handler wait would then start already past the
// deadline and give a live detached handler zero time -- the store closes under the pin or the
// deposit it exists to protect. Starting both before either blocks costs nothing and means the
// handler wait has run for the whole budget by the time it is read.
func TestWaitForBackupWorkWaitsBothAtOnce(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var finished atomic.Bool
	// backupDone never closes: the scheduled run is the one that hangs.
	waitForBackupWork(ctx, make(chan struct{}), func() {
		time.Sleep(10 * time.Millisecond)
		finished.Store(true)
	})
	if !finished.Load() {
		t.Fatal("the detached-handler wait had not run when waitForBackupWork returned; a hung scheduled deposit consumed its whole budget")
	}
}

// A scheduled prune is audited as system with its outcome, including restic never starting.
func TestPruneBulkAuditsAsSystem(t *testing.T) {
	ctx := context.Background()
	dbCfg := testdb.Config(t)
	dbCfg.DataDir = t.TempDir()
	st, err := store.Open(ctx, dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	t.Setenv("KYDRIVE_BULK_BACKUP_REPOSITORY", "")
	cfg := &config.Config{Database: dbCfg, Security: config.SecurityConfig{EncryptionKey: make([]byte, 32)}, Backup: config.BackupConfig{Keep: 1}}
	if err := backup.SetBulkPolicy(backup.Settings(ctx, st.Settings()), backup.BulkPolicy{Daily: 7}); err != nil {
		t.Fatal(err)
	}
	pruneBulk(ctx, ctx, cfg, st)
	pruneBulk(ctx, ctx, cfg, st) // within the day: no second row
	records, _, err := st.Audit().ListAuditRecords(ctx, 0, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("audit rows: %v %v", records, err)
	}
	if r := records[0]; r.UserID != "system" || r.Action != "backup.bulk_prune" || !strings.HasPrefix(r.Details, `outcome="failure" forgotten="0" error=`) || !strings.Contains(r.Details, "KYDRIVE_BULK_BACKUP_REPOSITORY") {
		t.Fatalf("audit row: %+v", r)
	}
}
