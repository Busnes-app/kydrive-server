package store_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

func replicaLockStores(t *testing.T) (store.Store, store.Store, *sql.DB) {
	t.Helper()
	if !testdb.Postgres() {
		t.Skip("session locks require PostgreSQL")
	}
	cfg := testdb.Config(t)
	cfg.Tenancy, cfg.StaffOrg = "multi", "00000000-0000-0000-0000-000000000099"
	cfg.MaxOpenConns = 4
	cfg.DSN += "&application_name=kydrive_replica_lock_test"
	open := func() store.Store {
		st, err := store.Open(t.Context(), cfg, testdb.Key)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}
	return open(), open(), testdb.Open(t, cfg)
}

type replicaLockResult struct {
	won bool
	err error
}

func lockResult(t *testing.T, result <-chan replicaLockResult) replicaLockResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("lock callback did not stop")
		return replicaLockResult{}
	}
}

func TestReplicaLockSingleModeUnchanged(t *testing.T) {
	st := newTestStore(t)
	called := false
	won, err := st.WithReplicaLock(t.Context(), "scheduled-worker", func(context.Context) error { called = true; return nil })
	if won || err == nil || !strings.Contains(err.Error(), "multi-tenant PostgreSQL") || called {
		t.Fatalf("single mode: won=%v called=%v err=%v", won, called, err)
	}
}

func TestReplicaLockExcludesOtherPoolsAndWaitsForWork(t *testing.T) {
	a, b, _ := replicaLockStores(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	entered, canceled, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(finish) })
	defer release()
	result := make(chan replicaLockResult, 1)
	go func() {
		won, err := a.WithReplicaLock(ctx, "scheduled-worker", func(owned context.Context) error {
			if err := a.Ping(owned); err != nil {
				return err
			} // callback queries use a different connection
			close(entered)
			<-owned.Done()
			close(canceled)
			<-finish
			return owned.Err()
		})
		result <- replicaLockResult{won, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("owner did not start")
	}
	called := false
	if won, err := b.WithReplicaLock(ctx, "scheduled-worker", func(context.Context) error { called = true; return nil }); won || err != nil || called {
		t.Fatal("contender ran", won, called, err)
	}
	// Scheduled ownership and backup serialization are distinct locks.
	if won, err := b.WithReplicaLock(ctx, "backup-run", b.Ping); !won || err != nil {
		t.Fatal("different name blocked", won, err)
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(10 * time.Second):
		t.Fatal("owner was not canceled")
	}
	if won, err := b.WithReplicaLock(t.Context(), "scheduled-worker", func(context.Context) error { return nil }); won || err != nil {
		t.Fatal("released before work finished", won, err)
	}
	release()
	if got := lockResult(t, result); !got.won || !errors.Is(got.err, context.Canceled) {
		t.Fatal(got)
	}
	if won, err := b.WithReplicaLock(t.Context(), "scheduled-worker", b.Ping); !won || err != nil {
		t.Fatal("ownership did not transfer", won, err)
	}
}

func TestReplicaLockReleasesOnErrorAndPanic(t *testing.T) {
	a, b, _ := replicaLockStores(t)
	wanted := errors.New("work failed")
	calls := 0
	if won, err := a.WithReplicaLock(t.Context(), "backup-run", func(context.Context) error { calls++; return wanted }); !won || !errors.Is(err, wanted) || calls != 1 {
		t.Fatal("callback replayed or error lost", won, err, calls)
	}
	if won, err := b.WithReplicaLock(t.Context(), "backup-run", b.Ping); !won || err != nil {
		t.Fatal("failed owner left lock in its pool", won, err)
	}
	func() {
		defer func() {
			if got := recover(); got != "work panic" {
				t.Fatalf("panic changed: %v", got)
			}
		}()
		_, _ = a.WithReplicaLock(t.Context(), "backup-run", func(context.Context) error { panic("work panic") })
	}()
	if won, err := b.WithReplicaLock(t.Context(), "backup-run", b.Ping); !won || err != nil {
		t.Fatal("panic left lock in pool", won, err)
	}
}

func TestReplicaLockCancelsAfterBackendLoss(t *testing.T) {
	a, b, db := replicaLockStores(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	entered := make(chan struct{})
	result := make(chan replicaLockResult, 1)
	go func() {
		won, err := a.WithReplicaLock(ctx, "scheduled-worker", func(owned context.Context) error {
			close(entered)
			<-owned.Done()
			return owned.Err()
		})
		result <- replicaLockResult{won, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("owner did not start")
	}
	var pid int
	// pg_locks exposes the actual owning session, distinct from the ordinary query pool.
	if err := db.QueryRowContext(ctx, `SELECT l.pid FROM pg_locks l JOIN pg_stat_activity a ON a.pid=l.pid WHERE a.application_name='kydrive_replica_lock_test' AND l.database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND l.locktype='advisory' AND l.classid=3 AND l.objsubid=2 AND l.granted AND l.objid::bigint=(hashtext(current_schema() || ':scheduled-worker')::bigint+4294967296)%4294967296`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	var killed bool
	if err := db.QueryRowContext(ctx, `SELECT pg_terminate_backend(?)`, pid).Scan(&killed); err != nil || !killed {
		t.Fatal("could not end lock session", killed, err)
	}
	if got := lockResult(t, result); !got.won || !errors.Is(got.err, context.Canceled) {
		t.Fatal("lost ownership did not cancel work", got)
	}
	if won, err := b.WithReplicaLock(t.Context(), "scheduled-worker", b.Ping); !won || err != nil {
		t.Fatal("standby did not acquire after loss", won, err)
	}
}
