package store_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/store"
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
