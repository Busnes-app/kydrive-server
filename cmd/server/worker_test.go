package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/recoveryclient"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// replicas opens two multi-mode stores, each with its own pool, on one PostgreSQL database.
func replicas(t *testing.T) (config.DatabaseConfig, [2]store.Store) {
	t.Helper()
	if !testdb.Postgres() {
		t.Skip("replica election needs PostgreSQL")
	}
	cfg := testdb.Config(t)
	cfg.Tenancy, cfg.StaffOrg = "multi", "00000000-0000-0000-0000-000000000099"
	var sts [2]store.Store
	for i, app := range []string{"kydrive_worker_a", "kydrive_worker_b"} {
		c := cfg
		c.DSN += "&application_name=" + app
		st, err := store.Open(t.Context(), c, testdb.Key)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		sts[i] = st
	}
	return cfg, sts
}

// Two replicas on one database: one runs every job, the other none; when the worker's session
// dies the other takes all the jobs, and the two never run at once.
func TestOneReplicaRunsTheJobs(t *testing.T) {
	cfg, sts := replicas(t)
	electRetry = 100 * time.Millisecond // no running job reads it yet; left short for this package

	var mu sync.Mutex
	running := [2]int{}
	overlap, ran := false, [2]bool{}
	job := func(r int) func(context.Context) {
		return func(ctx context.Context) {
			mu.Lock()
			running[r]++
			ran[r] = true
			overlap = overlap || running[1-r] > 0
			mu.Unlock()
			<-ctx.Done()
			mu.Lock()
			running[r]--
			mu.Unlock()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	var done [][]chan struct{}
	for r, st := range sts {
		done = append(done, startJobs(ctx, st, job(r), job(r)))
	}
	worker := func() int {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			a, b := running[0], running[1]
			mu.Unlock()
			switch {
			case a == 2 && b == 0:
				return 0
			case b == 2 && a == 0:
				return 1
			}
			time.Sleep(50 * time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("no single worker: running %v", running)
		return -1
	}
	first := worker()
	time.Sleep(time.Second) // the standby keeps retrying and must keep losing
	if w := worker(); w != first {
		t.Fatalf("worker moved without a failure: %d then %d", first, w)
	}

	db := testdb.Open(t, cfg)
	app := []string{"kydrive_worker_a", "kydrive_worker_b"}[first]
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(l.pid) FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE a.application_name=? AND l.locktype='advisory' AND l.classid=3 AND l.granted`, app); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		mu.Lock()
		stopped := running[first] == 0
		mu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker whose session died kept running its jobs")
		}
	}
	if w := worker(); w != 1-first {
		t.Fatalf("standby did not take over: worker %d", w)
	}
	cancel()
	for _, d := range done {
		for _, c := range d {
			select {
			case <-c:
			case <-time.After(10 * time.Second):
				t.Fatal("a job did not stop at shutdown")
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if overlap || !ran[0] || !ran[1] {
		t.Fatalf("overlap=%v ran=%v", overlap, ran)
	}
}

// A CLI deposit is refused while another replica holds backup-run, and runs once it is free.
func TestDepositWaitsForOtherReplicasBackup(t *testing.T) {
	_, sts := replicas(t)
	cfg := &config.Config{}
	cfg.Security.EncryptionKey = testdb.Key
	cfg.Database.DataDir = t.TempDir()

	held, release, released := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(released)
		_, _ = sts[0].WithReplicaLock(context.Background(), "backup-run", func(context.Context) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	_, err := runBackup(t.Context(), cfg, sts[1])
	close(release)
	if !errors.Is(err, recoveryclient.ErrInProgress) {
		t.Fatalf("deposit during another replica's backup: %v", err)
	}
	<-released
	// Free again, it reaches the run: this install has nowhere to put a capsule.
	if _, err := runBackup(t.Context(), cfg, sts[1]); !errors.Is(err, recoveryclient.ErrNoDestination) && !errors.Is(err, recoveryclient.ErrNotPaired) {
		t.Fatalf("deposit after release: %v", err)
	}
}
