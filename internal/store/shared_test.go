package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/store"
	"golang.org/x/net/webdav"
)

func TestSharedOnlyInMultiTenantPostgres(t *testing.T) {
	if st := newTestStore(t); st.Shared() != nil {
		t.Fatalf("%s single mode has shared state", st.Driver())
	}
}

// Two replicas racing on one key: the window admits exactly its limit and a key is spent
// exactly once, however the attempts interleave.
func TestSharedStateIsAtomicAcrossReplicas(t *testing.T) {
	a, b, _ := replicaLockStores(t)
	ctx := t.Context()
	var admitted, fresh atomic.Int32
	var wg sync.WaitGroup
	for i := range 40 {
		sh := []store.Store{a, b}[i%2].Shared()
		wg.Go(func() {
			if ok, err := sh.Take(ctx, "race", 7, time.Minute); err != nil {
				t.Error(err)
			} else if ok {
				admitted.Add(1)
			}
			if ok, err := sh.Spend(ctx, "pow", "salt", time.Now().Add(time.Minute)); err != nil {
				t.Error(err)
			} else if ok {
				fresh.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 7 || fresh.Load() != 1 {
		t.Fatalf("admitted %d of limit 7, spent %d times", admitted.Load(), fresh.Load())
	}
}

// Windows and spent keys end at their expiry in database time; refunds, resets and the sweep
// act on the rows the other replica wrote.
func TestSharedStateExpiresAndSweeps(t *testing.T) {
	a, b, db := replicaLockStores(t)
	ctx := t.Context()
	sa, sb := a.Shared(), b.Shared()
	take := func(sh *store.Shared) bool {
		t.Helper()
		ok, err := sh.Take(ctx, "k", 2, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	exceeded := func() bool {
		t.Helper()
		over, err := sb.Exceeded(ctx, "k", 2)
		if err != nil {
			t.Fatal(err)
		}
		return over
	}
	if !take(sa) || exceeded() || !take(sb) || !exceeded() || take(sa) {
		t.Fatal("window of 2 across replicas")
	}
	if err := sb.Refund(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	// The refusal counted, so one refund still leaves the window shut.
	if take(sa) {
		t.Fatal("a refund after a refusal opened the window")
	}
	if err := sb.Reset(ctx, "k"); err != nil || !take(sa) {
		t.Fatal("reset", err)
	}
	if ok, err := sa.Spend(ctx, "kypost", "e", time.Now().Add(time.Minute)); !ok || err != nil {
		t.Fatal("spend", ok, err)
	}
	if err := sb.Unspend(ctx, "kypost", "e"); err != nil {
		t.Fatal(err)
	}
	if ok, err := sa.Spend(ctx, "kypost", "e", time.Now().Add(time.Minute)); !ok || err != nil {
		t.Fatal("spend after unspend", ok, err)
	}
	if err := sa.PutTicket(ctx, "t", store.Ticket{Blob: "b"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	// Age everything past its expiry.
	for _, table := range []string{"shared_windows", "shared_spent", "shared_tickets"} {
		if _, err := db.Exec(`UPDATE ` + table + ` SET expires_at = 1`); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, err := sb.GetTicket(ctx, "t"); ok || err != nil {
		t.Fatal("an expired ticket serves", err)
	}
	if ok, err := sb.Spend(ctx, "kypost", "e", time.Now().Add(time.Minute)); !ok || err != nil {
		t.Fatal("an expired key is still spent", ok, err)
	}
	if exceeded() || !take(sb) {
		t.Fatal("an expired window still counts")
	}
	if _, err := db.Exec(`UPDATE shared_windows SET expires_at = 1`); err != nil {
		t.Fatal(err)
	}
	if err := sa.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM shared_windows) + (SELECT COUNT(*) FROM shared_tickets) + (SELECT COUNT(*) FROM shared_spent WHERE expires_at <= 1)`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("sweep left %d expired rows: %v", left, err)
	}
}

// Two replicas locking one item at once: exactly one gets the lock. A held lock survives its
// timeout until the request holding it ends, and cannot be released meanwhile.
func TestSharedDAVLocksAcrossReplicas(t *testing.T) {
	a, b, db := replicaLockStores(t)
	ctx := t.Context()
	la, lb := a.Shared().DavLocks(), b.Shared().DavLocks()
	var won atomic.Int32
	var wg sync.WaitGroup
	for i := range 10 {
		l := []*store.DavLocks{la, lb}[i%2]
		wg.Go(func() {
			if _, err := l.Create(ctx, webdav.LockDetails{Root: "/ws/doc.odt", Duration: time.Minute, ZeroDepth: true}); err == nil {
				won.Add(1)
			} else if !errors.Is(err, webdav.ErrLocked) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("%d of 10 concurrent locks won", won.Load())
	}
	// An infinite-depth folder lock covers what is below it, and blocks an infinite lock above.
	token, err := la.Create(ctx, webdav.LockDetails{Root: "/ws/folder", Duration: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lb.Create(ctx, webdav.LockDetails{Root: "/ws/folder/inner.txt", Duration: time.Minute, ZeroDepth: true}); !errors.Is(err, webdav.ErrLocked) {
		t.Fatalf("lock below an infinite lock: %v", err)
	}
	if _, err := lb.Create(ctx, webdav.LockDetails{Root: "/ws", Duration: time.Minute}); !errors.Is(err, webdav.ErrLocked) {
		t.Fatalf("infinite lock above a lock: %v", err)
	}
	if _, err := lb.Create(ctx, webdav.LockDetails{Root: "/ws/folder-2", Duration: time.Minute, ZeroDepth: true}); err != nil {
		t.Fatalf("a sibling sharing the prefix: %v", err)
	}
	// One infinite lock covers both ends of a MOVE inside it.
	release, err := lb.Confirm(ctx, "/ws/folder/a.txt", "/ws/folder/b.txt", []string{token})
	if err != nil {
		t.Fatalf("a move inside a locked folder with its token: %v", err)
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
	release, err = lb.Confirm(ctx, "/ws/folder/inner.txt", "", []string{"nope", token})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := la.Confirm(ctx, "/ws/folder", "", []string{token}); !errors.Is(err, webdav.ErrConfirmationFailed) {
		t.Fatalf("a held lock confirmed twice: %v", err)
	}
	if err := la.Unlock(ctx, token); !errors.Is(err, webdav.ErrLocked) {
		t.Fatalf("unlock of a held lock: %v", err)
	}
	if _, err := db.Exec(`UPDATE shared_dav_locks SET expires_at = 1 WHERE token = $1`, token); err != nil {
		t.Fatal(err)
	}
	if err := a.Shared().Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := la.Refresh(ctx, token, time.Minute); !errors.Is(err, webdav.ErrLocked) {
		t.Fatalf("a held lock past its timeout: %v, want still held", err)
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := la.Unlock(ctx, token); !errors.Is(err, webdav.ErrNoSuchLock) {
		t.Fatalf("a lock whose timeout passed while held, once released: %v", err)
	}
}

// Confirm considers only the first davTokens distinct presented tokens, in one query per name,
// so a request with a huge If header cannot hold the table's lock for long.
func TestSharedDAVConfirmIsBounded(t *testing.T) {
	a, _, _ := replicaLockStores(t)
	ctx := t.Context()
	l := a.Shared().DavLocks()
	token, err := l.Create(ctx, webdav.LockDetails{Root: "/ws/f.txt", Duration: time.Minute, ZeroDepth: true})
	if err != nil {
		t.Fatal(err)
	}
	many := make([]string, 0, 100001)
	for i := range 100000 {
		many = append(many, fmt.Sprintf("bogus-%d", i))
	}
	start := time.Now()
	if _, err := l.Confirm(ctx, "/ws/f.txt", "", append(many, token)); !errors.Is(err, webdav.ErrConfirmationFailed) {
		t.Fatalf("a token past the first 32 distinct ones: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("100 001 tokens took %v", d)
	}
	repeated := append(slices.Repeat([]string{"bogus"}, 100000), token)
	release, err := l.Confirm(ctx, "/ws/f.txt", "", repeated)
	if err != nil {
		t.Fatalf("a repeated token used up the cap: %v", err)
	}
	if err := release(ctx); err != nil {
		t.Fatal(err)
	}
}

// The sweep removes expired WebDAV locks only under the lock table's lock, so it never deletes
// a lock between a confirmation choosing it and holding it.
func TestSharedSweepWaitsForTheDAVLockTable(t *testing.T) {
	a, _, db := replicaLockStores(t)
	ctx := t.Context()
	token, err := a.Shared().DavLocks().Create(ctx, webdav.LockDetails{Root: "/ws/s.txt", Duration: time.Minute, ZeroDepth: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE shared_dav_locks SET expires_at = 1 WHERE token = $1`, token); err != nil {
		t.Fatal(err)
	}
	busy, err := db.BeginTx(ctx, nil) // a confirmation in progress
	if err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Exec(`SELECT pg_advisory_xact_lock(hashtext('kydrive dav locks'))`); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	_ = a.Shared().Sweep(short)
	var left int
	if err := db.QueryRow(`SELECT COUNT(*) FROM shared_dav_locks WHERE token = $1`, token).Scan(&left); err != nil || left != 1 {
		t.Fatalf("the sweep deleted a lock while the table was locked: %d %v", left, err)
	}
	if err := busy.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := a.Shared().Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM shared_dav_locks WHERE token = $1`, token).Scan(&left); err != nil || left != 0 {
		t.Fatalf("the sweep left an expired lock: %d %v", left, err)
	}
}
