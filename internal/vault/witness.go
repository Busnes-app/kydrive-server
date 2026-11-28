//go:build !js

package vault

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Busnes-app/ky-primitives/hybridsig"
	_ "modernc.org/sqlite"
)

const MaxReceipts = 32

// Witness uses its own database and key. Its publisher key authorizes only opaque log receipts.
type Witness struct {
	db        *sql.DB
	key       *hybridsig.PrivateKey
	publisher *hybridsig.PublicKey
	now       func() time.Time
}

// InitializeWitness creates a new database. OpenWitness only opens initialized state.
func InitializeWitness(ctx context.Context, path string, key *hybridsig.PrivateKey, publisher *hybridsig.PublicKey) (*Witness, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	witness, err := openWitness(ctx, path, key, publisher, true)
	if err != nil {
		_ = os.Remove(path)
	}
	return witness, err
}

func OpenWitness(ctx context.Context, path string, key *hybridsig.PrivateKey, publisher *hybridsig.PublicKey) (*Witness, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() == 0 {
		return nil, errors.New("witness database must be a nonempty owner-only regular file")
	}
	return openWitness(ctx, path, key, publisher, false)
}

func openWitness(ctx context.Context, path string, key *hybridsig.PrivateKey, publisher *hybridsig.PublicKey, initialize bool) (*Witness, error) {
	if key == nil || publisher == nil || bytes.Equal(key.Public().Bytes(), publisher.Bytes()) {
		return nil, ErrInvalid
	}
	dsn := url.URL{Scheme: "file", Path: path, RawQuery: "mode=rw&_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=synchronous(FULL)"}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if initialize {
		err = createWitnessSchema(ctx, db, key.Public(), publisher)
	} else {
		err = checkWitnessSchema(ctx, db, key.Public(), publisher)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Witness{db: db, key: key, publisher: publisher, now: time.Now}, nil
}

func createWitnessSchema(ctx context.Context, db *sql.DB, signing, publisher *hybridsig.PublicKey) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE witness_heads (
log TEXT PRIMARY KEY, sequence INTEGER NOT NULL CHECK(sequence>0), hash TEXT NOT NULL);
CREATE TABLE witness_identity (
id INTEGER PRIMARY KEY CHECK(id=1), signing BLOB NOT NULL, publisher BLOB NOT NULL);`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO witness_identity(id,signing,publisher) VALUES(1,?,?)`,
		signing.Bytes(), publisher.Bytes()); err != nil {
		return err
	}
	return tx.Commit()
}

func checkWitnessSchema(ctx context.Context, db *sql.DB, signing, publisher *hybridsig.PublicKey) error {
	var storedSigning, storedPublisher []byte
	var empty int
	err := db.QueryRowContext(ctx, `SELECT signing,publisher,
		(SELECT COUNT(*) FROM witness_heads WHERE 1=0)
		FROM witness_identity WHERE id=1`).Scan(&storedSigning, &storedPublisher, &empty)
	if err != nil {
		return err
	}
	if !bytes.Equal(storedSigning, signing.Bytes()) || !bytes.Equal(storedPublisher, publisher.Bytes()) {
		return errors.New("witness database belongs to a different signing key or publisher")
	}
	return nil
}

func (w *Witness) Close() error { return w.db.Close() }

func (w *Witness) sign(head Head, at time.Time) (Checkpoint, error) {
	cp := Checkpoint{Head: head, IssuedMS: at.UnixMilli()}
	var err error
	cp.Signature, err = w.key.Sign(checkpointDomain, cp.message())
	return cp, err
}

func (w *Witness) Current(ctx context.Context, log string) (Checkpoint, error) {
	if !validDigest(log) {
		return Checkpoint{}, ErrInvalid
	}
	// Date the checkpoint before the read. A delayed signature must never refresh an
	// old head after a concurrent publisher has advanced it.
	at := w.now()
	head := Head{Log: log}
	err := w.db.QueryRowContext(ctx, `SELECT sequence, hash FROM witness_heads WHERE log=?`, log).Scan(&head.Sequence, &head.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{}, ErrMissing
	}
	if err != nil {
		return Checkpoint{}, err
	}
	return w.sign(head, at)
}

// Advance accepts a contiguous extension or an exact retry of the current head.
func (w *Witness) Advance(ctx context.Context, log string, receipts []Receipt) (Checkpoint, error) {
	if !validDigest(log) || len(receipts) == 0 || len(receipts) > MaxReceipts {
		return Checkpoint{}, ErrInvalid
	}
	for i, r := range receipts {
		if r.Log != log || r.verify(w.publisher) != nil {
			return Checkpoint{}, ErrInvalid
		}
		if i > 0 {
			previous := receipts[i-1].Link
			if r.Link.Sequence != previous.Sequence+1 || r.Link.Previous != previous.Hash() {
				return Checkpoint{}, ErrConflict
			}
		}
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return Checkpoint{}, err
	}
	defer tx.Rollback()
	head := Head{Log: log}
	err = tx.QueryRowContext(ctx, `SELECT sequence, hash FROM witness_heads WHERE log=?`, log).Scan(&head.Sequence, &head.Hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Checkpoint{}, err
	}
	changed := false
	for _, r := range receipts {
		switch {
		case r.Link.Sequence < head.Sequence:
			continue
		case r.Link.Sequence == head.Sequence && r.Link.Hash() == head.Hash:
			continue
		case r.Link.Sequence == head.Sequence+1 && r.Link.Previous == head.Hash:
			head.Sequence, head.Hash = r.Link.Sequence, r.Link.Hash()
			changed = true
		default:
			return Checkpoint{}, ErrConflict
		}
	}
	if receipts[len(receipts)-1].Link.Sequence < head.Sequence {
		return Checkpoint{}, ErrConflict
	}
	if changed {
		if _, err = tx.ExecContext(ctx, `INSERT INTO witness_heads(log, sequence, hash) VALUES(?,?,?)
ON CONFLICT(log) DO UPDATE SET sequence=excluded.sequence, hash=excluded.hash`, log, head.Sequence, head.Hash); err != nil {
			return Checkpoint{}, err
		}
	}
	cp, err := w.sign(head, w.now())
	if err != nil {
		return Checkpoint{}, err
	}
	if err = tx.Commit(); err != nil {
		return Checkpoint{}, err
	}
	return cp, nil
}

func (w *Witness) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/logs/{log}", func(resp http.ResponseWriter, r *http.Request) {
		cp, err := w.Current(r.Context(), r.PathValue("log"))
		writeCheckpoint(resp, cp, err)
	})
	mux.HandleFunc("POST /v1/logs/{log}", func(resp http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(resp, r.Body, 256<<10)
		var receipts []Receipt
		if err := Decode(r.Body, &receipts); err != nil {
			writeCheckpoint(resp, Checkpoint{}, ErrInvalid)
			return
		}
		cp, err := w.Advance(r.Context(), r.PathValue("log"), receipts)
		writeCheckpoint(resp, cp, err)
	})
	return mux
}

func Decode(r io.Reader, value any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	if dec.Decode(new(json.RawMessage)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

func writeCheckpoint(w http.ResponseWriter, cp Checkpoint, err error) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	switch {
	case errors.Is(err, ErrInvalid):
		http.Error(w, "invalid receipt", http.StatusBadRequest)
	case errors.Is(err, ErrMissing):
		http.Error(w, "unknown log", http.StatusNotFound)
	case errors.Is(err, ErrConflict):
		http.Error(w, "checkpoint conflict", http.StatusConflict)
	case err != nil:
		http.Error(w, "witness unavailable", http.StatusServiceUnavailable)
	default:
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cp)
	}
}
