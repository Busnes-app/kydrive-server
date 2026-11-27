package search

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemorySnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	key := []byte(strings.Repeat("k", 32))
	x, err := OpenMemory(ctx, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	ref := x.Ref("blob-one", "text")
	counts, n := Count("privateword privately")
	if err := x.Put(ctx, ref, counts, n, "file", time.Now()); err != nil {
		t.Fatal(err)
	}
	data, err := x.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	y, err := OpenMemory(ctx, key, data)
	if err != nil {
		t.Fatal(err)
	}
	defer y.Close()
	got, err := y.Match(ctx, ParseQuery("privat*"), map[string]bool{ref: true})
	if err != nil || len(got.Scores) != 1 {
		t.Fatalf("prefix after restart: %+v %v", got, err)
	}
	if _, err := OpenMemory(ctx, []byte(strings.Repeat("j", 32)), data); err == nil {
		t.Fatal("foreign key accepted")
	}
	// Readers mutate only their own copy; the snapshot and writer stay independent.
	if _, err := y.Keep(ctx, map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	got, err = x.Match(ctx, ParseQuery("privateword"), map[string]bool{ref: true})
	if err != nil || len(got.Scores) != 1 {
		t.Fatal("reader mutated writer", err)
	}
}
