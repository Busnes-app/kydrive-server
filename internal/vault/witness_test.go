package vault

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/hybridsig"
)

func TestWitnessMonotonicHead(t *testing.T) {
	ctx := t.Context()
	key := func() *hybridsig.PrivateKey {
		t.Helper()
		out, err := hybridsig.Generate()
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	signing, publisher, stranger := key(), key(), key()
	db := filepath.Join(t.TempDir(), "state.db")
	if _, err := OpenWitness(ctx, db, signing, publisher.Public()); err == nil {
		t.Fatal("opened an absent database")
	}
	w, err := InitializeWitness(ctx, db, signing, publisher.Public())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	at := time.UnixMilli(time.Now().UnixMilli())
	w.now = func() time.Time { return at }
	first := Link{Sequence: 1, Record: Digest([]byte("genesis"))}
	log := first.Hash()
	sign := func(p *hybridsig.PrivateKey, link Link) Receipt {
		t.Helper()
		out, err := SignReceipt(p, log, link)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	receipt := sign(publisher, first)
	if _, err := w.Advance(ctx, log, []Receipt{sign(stranger, first)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("untrusted publisher: %v", err)
	}
	genesis, err := w.Advance(ctx, log, []Receipt{receipt})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCheckpoint(signing.Public(), genesis, genesis.Head, at.Add(MaxCheckpointAge-time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []time.Time{at.Add(-time.Millisecond), at.Add(MaxCheckpointAge)} {
		if err := VerifyCheckpoint(signing.Public(), genesis, genesis.Head, bad); err == nil {
			t.Fatalf("invalid checkpoint age %s passed", bad.Sub(at))
		}
	}
	if err := VerifyCheckpoint(stranger.Public(), genesis, genesis.Head, at); err == nil {
		t.Fatal("stranger verified the checkpoint")
	}
	outcomes := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"remove", "rotate"} {
		next := sign(publisher, Link{Sequence: 2, Previous: log, Record: Digest([]byte(name))})
		wg.Go(func() {
			_, err := w.Advance(ctx, log, []Receipt{receipt, next})
			outcomes <- err
		})
	}
	wg.Wait()
	close(outcomes)
	var successes, conflicts int
	for err := range outcomes {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("advances = %d, conflicts = %d", successes, conflicts)
	}
	if _, err := w.Advance(ctx, log, []Receipt{receipt}); !errors.Is(err, ErrConflict) {
		t.Fatalf("rollback: %v", err)
	}
	head, err := w.Current(ctx, log)
	if err != nil || head.Head.Sequence != 2 || head.Head.Hash == genesis.Head.Hash {
		t.Fatalf("current head = %+v, error = %v", head.Head, err)
	}
	if err := VerifyCheckpoint(signing.Public(), genesis, head.Head, at); err == nil {
		t.Fatal("old checkpoint verified for new head")
	}
	if other, err := OpenWitness(ctx, db, stranger, publisher.Public()); err == nil {
		other.Close()
		t.Fatal("database reopened with different signing key")
	}
}
