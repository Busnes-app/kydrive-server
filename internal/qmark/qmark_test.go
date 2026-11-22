package qmark

import "testing"

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
