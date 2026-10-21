package search

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
	_ "modernc.org/sqlite"
)

// Document states. A document with no row is unindexed.
const (
	StateDone   = 1
	StateFailed = 2
	// MaxAttempts is how often a failing document is tried before it is left alone.
	MaxAttempts = 3
)

// File is the index database's name inside its directory (<KY_DATA_DIR>/search).
const File = "index.db"

const schema = `CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS docs(id INTEGER PRIMARY KEY, ref BLOB NOT NULL UNIQUE, state INTEGER NOT NULL, len INTEGER NOT NULL DEFAULT 0, err TEXT NOT NULL DEFAULT '', attempts INTEGER NOT NULL DEFAULT 0, updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS postings(token INTEGER NOT NULL, doc INTEGER NOT NULL, tf INTEGER NOT NULL, PRIMARY KEY(token, doc)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS queue(file TEXT PRIMARY KEY, first INTEGER NOT NULL, not_before INTEGER NOT NULL);`

// Index is the content search index. Postings name a document by ref, a keyed hash of its blob
// ID and extractor kind, and a term by a keyed 64-bit hash; the database holds no plaintext, no
// blob ID and no file type. Queue rows hold random file IDs until processed.
type Index struct {
	db       *sql.DB
	tok, ref []byte
}

// Open opens or creates the index under dir. An index built under another deployment key or
// tokenizer version is deleted and started again, empty.
func Open(ctx context.Context, dir string, key []byte) (*Index, error) {
	if len(key) != 32 {
		return nil, errors.New("search: index requires a 32-byte deployment key")
	}
	tok, err := crypto.DeriveKey(key, "kydrive:search-token")
	if err != nil {
		return nil, err
	}
	ref, err := crypto.DeriveKey(key, "kydrive:search-doc")
	if err != nil {
		return nil, err
	}
	check, err := crypto.DeriveKey(key, "kydrive:search-check")
	if err != nil {
		return nil, err
	}
	want := hex.EncodeToString(check) + ":" + strconv.Itoa(TokenizerVersion)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, File)
	for attempt := 0; ; attempt++ {
		db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=secure_delete(ON)")
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		got, err := prepare(ctx, db, want)
		if err == nil && got == want {
			return &Index{db: db, tok: tok, ref: ref}, nil
		}
		db.Close()
		if err != nil {
			return nil, fmt.Errorf("search index: %w", err)
		}
		if attempt > 0 {
			return nil, errors.New("search index: could not start a fresh index")
		}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
	}
}

// prepare creates the schema and returns the stored key/tokenizer check, recording want when
// the index is new.
func prepare(ctx context.Context, db *sql.DB, want string) (string, error) {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return "", err
	}
	if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO meta VALUES('check',?)`, want); err != nil {
		return "", err
	}
	var got string
	err := db.QueryRowContext(ctx, `SELECT v FROM meta WHERE k='check'`).Scan(&got)
	return got, err
}

// Close closes the index database.
func (x *Index) Close() error { return x.db.Close() }

// Ref identifies the text of blob extracted as kind. Equal content shares one ref.
func (x *Index) Ref(blob, kind string) string {
	h := hmac.New(sha256.New, x.ref)
	h.Write([]byte(blob + ":" + kind))
	return string(h.Sum(nil)[:16])
}

func (x *Index) token(term string) int64 {
	h := hmac.New(sha256.New, x.tok)
	h.Write([]byte(term))
	return int64(binary.BigEndian.Uint64(h.Sum(nil)[:8]))
}

// Cursor is the last drive change sequence number queued.
func (x *Index) Cursor(ctx context.Context) (int64, error) {
	var v string
	err := x.db.QueryRowContext(ctx, `SELECT v FROM meta WHERE k='cursor'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

// Enqueue queues files to be (re)considered after delay, and, when cursor >= 0, records it in
// the same transaction. A file queued again waits for the later time, but never more than a
// minute after it was first queued, so an autosaving file is still indexed.
func (x *Index) Enqueue(ctx context.Context, files []string, now time.Time, delay time.Duration, cursor int64) error {
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := now.Unix()
	for _, f := range files {
		if _, err = tx.ExecContext(ctx, `INSERT INTO queue(file,first,not_before) VALUES(?,?,?) ON CONFLICT(file) DO UPDATE SET not_before=MIN(MAX(queue.not_before,excluded.not_before),queue.first+60)`, f, at, at+int64(delay/time.Second)); err != nil {
			return err
		}
	}
	if cursor >= 0 {
		if _, err = tx.ExecContext(ctx, `INSERT INTO meta VALUES('cursor',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, strconv.FormatInt(cursor, 10)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Due returns up to limit queued files whose wait is over (all of them when force is set).
func (x *Index) Due(ctx context.Context, now time.Time, force bool, limit int) ([]string, error) {
	q, args := `SELECT file FROM queue WHERE not_before<=? ORDER BY not_before,file LIMIT ?`, []any{now.Unix(), limit}
	if force {
		q, args = `SELECT file FROM queue ORDER BY not_before,file LIMIT ?`, []any{limit}
	}
	rows, err := x.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var f string
		if err = rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Queued is how many files wait in the queue.
func (x *Index) Queued(ctx context.Context) (int, error) {
	var n int
	err := x.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM queue`).Scan(&n)
	return n, err
}

// Dequeue drops file from the queue.
func (x *Index) Dequeue(ctx context.Context, file string) error {
	_, err := x.db.ExecContext(ctx, `DELETE FROM queue WHERE file=?`, file)
	return err
}

// Status is a document's state (0 when unindexed), failed attempts and last update (Unix).
func (x *Index) Status(ctx context.Context, ref string) (state, attempts int, updated int64, err error) {
	err = x.db.QueryRowContext(ctx, `SELECT state,attempts,updated FROM docs WHERE ref=?`, []byte(ref)).Scan(&state, &attempts, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, 0, nil
	}
	return state, attempts, updated, err
}

// DocStatus is one document's state, failed attempts and last update (Unix).
type DocStatus struct {
	State, Attempts int
	Updated         int64
}

// Docs returns every document's status by ref.
func (x *Index) Docs(ctx context.Context) (map[string]DocStatus, error) {
	rows, err := x.db.QueryContext(ctx, `SELECT ref,state,attempts,updated FROM docs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]DocStatus{}
	for rows.Next() {
		var ref []byte
		var d DocStatus
		if err = rows.Scan(&ref, &d.State, &d.Attempts, &d.Updated); err != nil {
			return nil, err
		}
		out[string(ref)] = d
	}
	return out, rows.Err()
}

// Retry reports whether a document in that status should be extracted again at now: unindexed,
// or failed fewer than MaxAttempts times and past its backoff (2, 4 minutes).
func Retry(state, attempts int, updated int64, now time.Time) bool {
	switch state {
	case 0:
		return true
	case StateFailed:
		return attempts < MaxAttempts && now.Unix() >= updated+int64(60<<attempts)
	}
	return false
}

// Put records ref's term counts and token count, and drops file from the queue, in one
// transaction. A failed document has no postings, so none need removing.
func (x *Index) Put(ctx context.Context, ref string, counts map[string]int, length int, file string, now time.Time) error {
	tokens := make([]int64, 0, len(counts))
	tf := make(map[int64]int, len(counts))
	for term, n := range counts {
		t := x.token(term)
		if _, dup := tf[t]; !dup {
			tokens = append(tokens, t)
		}
		tf[t] += n
	}
	slices.Sort(tokens)
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var doc int64
	if err = tx.QueryRowContext(ctx, `INSERT INTO docs(ref,state,len,updated) VALUES(?,?,?,?) ON CONFLICT(ref) DO UPDATE SET state=excluded.state,len=excluded.len,err='',updated=excluded.updated RETURNING id`, []byte(ref), StateDone, length, now.Unix()).Scan(&doc); err != nil {
		return err
	}
	st, err := tx.PrepareContext(ctx, `INSERT OR REPLACE INTO postings(token,doc,tf) VALUES(?,?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, t := range tokens {
		if _, err = st.ExecContext(ctx, t, doc, min(tf[t], maxTF)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM queue WHERE file=?`, file); err != nil {
		return err
	}
	return tx.Commit()
}

// Fail records a failed extraction of ref (code is a short reason, never content) and drops
// file from the queue.
func (x *Index) Fail(ctx context.Context, ref, code, file string, now time.Time) error {
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO docs(ref,state,err,attempts,updated) VALUES(?,?,?,1,?) ON CONFLICT(ref) DO UPDATE SET state=excluded.state,err=excluded.err,attempts=docs.attempts+1,updated=excluded.updated`, []byte(ref), StateFailed, code, now.Unix()); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM queue WHERE file=?`, file); err != nil {
		return err
	}
	return tx.Commit()
}

// Keep deletes every document whose ref is not in keep, with its postings, and returns how many
// went. Postings have no index by document, so they are deleted in batched scans.
func (x *Index) Keep(ctx context.Context, keep map[string]bool) (int, error) {
	rows, err := x.db.QueryContext(ctx, `SELECT id,ref FROM docs`)
	if err != nil {
		return 0, err
	}
	var stale []any
	for rows.Next() {
		var id int64
		var ref []byte
		if err = rows.Scan(&id, &ref); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[string(ref)] {
			stale = append(stale, id)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(stale) == 0 {
		return 0, err
	}
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for batch := range slices.Chunk(stale, 10000) {
		in := "(" + strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",") + ")"
		if _, err = tx.ExecContext(ctx, `DELETE FROM postings WHERE doc IN `+in, batch...); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM docs WHERE id IN `+in, batch...); err != nil {
			return 0, err
		}
	}
	return len(stale), tx.Commit()
}

// Settled reports whether a document in that status needs no more work.
func settled(state, attempts int) bool {
	return state == StateDone || (state == StateFailed && attempts >= MaxAttempts)
}

// BM25 parameters.
const (
	k1 = 1.2
	b  = 0.75
)

// Match scores the documents among refs that hold every term (BM25), with document counts,
// frequencies and lengths taken from refs only, so what the caller cannot read never shapes
// the result. pending counts refs not yet settled.
func (x *Index) Match(ctx context.Context, terms []string, refs map[string]bool) (scores map[string]float64, pending int, err error) {
	type doc struct {
		ref    string
		length float64
	}
	cand := map[int64]doc{}
	settledRefs := map[string]bool{}
	rows, err := x.db.QueryContext(ctx, `SELECT id,ref,state,attempts,len FROM docs`)
	if err != nil {
		return nil, 0, err
	}
	total := 0.0
	for rows.Next() {
		var id int64
		var ref []byte
		var state, attempts, length int
		if err = rows.Scan(&id, &ref, &state, &attempts, &length); err != nil {
			rows.Close()
			return nil, 0, err
		}
		if !refs[string(ref)] {
			continue
		}
		if settled(state, attempts) {
			settledRefs[string(ref)] = true
		}
		if state == StateDone {
			cand[id] = doc{string(ref), float64(length)}
			total += float64(length)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	pending = len(refs) - len(settledRefs)
	scores = map[string]float64{}
	if len(cand) == 0 || len(terms) == 0 {
		return scores, pending, nil
	}
	n := float64(len(cand))
	avg := max(total/n, 1)
	score := map[int64]float64{}
	for i, term := range terms {
		tfs := map[int64]float64{}
		rows, err := x.db.QueryContext(ctx, `SELECT doc,tf FROM postings WHERE token=?`, x.token(term))
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var d int64
			var tf float64
			if err = rows.Scan(&d, &tf); err != nil {
				rows.Close()
				return nil, 0, err
			}
			if _, ok := cand[d]; ok && (i == 0 || score[d] > 0) {
				tfs[d] = tf
			}
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return nil, 0, err
		}
		df := float64(len(tfs))
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		next := map[int64]float64{}
		for d, tf := range tfs {
			next[d] = score[d] + idf*tf*(k1+1)/(tf+k1*(1-b+b*cand[d].length/avg))
		}
		if score = next; len(score) == 0 {
			return scores, pending, nil
		}
	}
	for d, s := range score {
		scores[cand[d].ref] = s
	}
	return scores, pending, nil
}
