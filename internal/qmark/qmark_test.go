package qmark

import (
	"context"
	"errors"
	"testing"
)

func TestRebind(t *testing.T) {
	for q, want := range map[string]string{
		`SELECT 1`:                                `SELECT 1`,
		`SELECT ? WHERE a=? AND b=?`:              `SELECT $1 WHERE a=$2 AND b=$3`,
		`SELECT '?', ? FROM t`:                    `SELECT '?', $1 FROM t`,
		`SELECT 'it''s ?', ?`:                     `SELECT 'it''s ?', $1`,
		`SELECT "a?" FROM t WHERE x=?`:            `SELECT "a?" FROM t WHERE x=$1`,
		"SELECT ? -- why?\n, ?":                   "SELECT $1 -- why?\n, $2",
		`SELECT /* ? */ ?`:                        `SELECT /* ? */ $1`,
		`AS $f$BEGIN IF x='?' THEN END IF; $f$ ?`: `AS $f$BEGIN IF x='?' THEN END IF; $f$ $1`,
		`AS $$ ? $$, ?`:                           `AS $$ ? $$, $1`,
		`WHERE a=?1 OR b=?1 AND c=?`:              `WHERE a=$1 OR b=$1 AND c=$2`,
		`WHERE a=?2 AND b=? AND c=?1`:             `WHERE a=$2 AND b=$3 AND c=$1`,
		`SELECT 'open ?`:                          `SELECT 'open ?`,
	} {
		if got := Rebind(q); got != want {
			t.Errorf("Rebind(%q) = %q, want %q", q, got, want)
		}
	}
}

func TestRetryRerunsOnlySerializationFailures(t *testing.T) {
	for msg, want := range map[string]int{
		"ERROR: could not serialize access (SQLSTATE 40001)": MaxAttempts,
		"ERROR: deadlock detected (SQLSTATE 40P01)":          MaxAttempts,
		"ERROR: duplicate key value (SQLSTATE 23505)":        1,
	} {
		runs := 0
		err := Retry(t.Context(), func() error { runs++; return errors.New(msg) })
		if runs != want || err == nil || err.Error() != msg {
			t.Errorf("%s: %d runs, %v", msg, runs, err)
		}
	}
	runs := 0
	if err := Retry(t.Context(), func() error {
		if runs++; runs < 3 {
			return errors.New("(SQLSTATE 40001)")
		}
		return nil
	}); err != nil || runs != 3 {
		t.Fatalf("%d runs, %v", runs, err)
	}
}

func TestRetryCancellationStopsContention(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runs := 0
	err := Retry(ctx, func() error {
		runs++
		if runs == 3 {
			cancel()
		}
		return errors.New("(SQLSTATE 40001)")
	})
	if err == nil || runs != 3 {
		t.Fatalf("cancelled contention kept retrying: runs=%d err=%v", runs, err)
	}
}
