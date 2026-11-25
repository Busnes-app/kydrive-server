package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Shared is the short-lived security state every replica of a multi-tenant PostgreSQL install
// sees (tenancy design R4): rate-limit windows, spent one-time keys (CAPTCHA salts, replayed
// event IDs, device nonces) and conversion tickets. Database time sets every expiry, so the
// replicas' clocks never disagree about a window. Each item is one atomic statement on one
// unique key. The elected worker's Sweep removes what has expired.
type Shared struct{ db *sql.DB }

// Shared is nil outside multi-tenant PostgreSQL, where the server keeps this state in memory.
func (s *SQLStore) Shared() *Shared {
	if s.driver != "postgres" || s.drive == nil || !s.drive.Multi() {
		return nil
	}
	return &Shared{s.db}
}

// readCommitted runs one statement at READ COMMITTED. The pool's default is serializable,
// which fails concurrent upserts of one key with 40001; at READ COMMITTED an INSERT ... ON
// CONFLICT waits for the other row and then applies to it, which is all one key needs.
func (s *Shared) readCommitted(ctx context.Context, work func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := work(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Shared) exec(ctx context.Context, q string, args ...any) (n int64, err error) {
	err = s.readCommitted(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, q, args...)
		if err == nil {
			n, err = res.RowsAffected()
		}
		return err
	})
	return n, err
}

func (s *Shared) row(ctx context.Context, q string, args []any, dest ...any) error {
	return s.readCommitted(ctx, func(tx *sql.Tx) error { return tx.QueryRowContext(ctx, q, args...).Scan(dest...) })
}

const dbMillis = `CAST(EXTRACT(EPOCH FROM statement_timestamp()) * 1000 AS BIGINT)`

// Take counts one attempt on key's window, opening a new window of length window when none
// is live, and reports whether the count is within limit. Refusals count too, as in memory.
func (s *Shared) Take(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if key == "" || limit < 1 || window < time.Millisecond {
		return false, errors.New("invalid rate-limit window")
	}
	var hits int64
	err := s.row(ctx, `INSERT INTO shared_windows (key, hits, expires_at) VALUES ($1, 1, `+dbMillis+` + $2)
ON CONFLICT (key) DO UPDATE SET
hits = CASE WHEN shared_windows.expires_at <= `+dbMillis+` THEN 1 ELSE shared_windows.hits + 1 END,
expires_at = CASE WHEN shared_windows.expires_at <= `+dbMillis+` THEN `+dbMillis+` + $2 ELSE shared_windows.expires_at END
RETURNING hits`, []any{key, window.Milliseconds()}, &hits)
	return err == nil && hits <= int64(limit), err
}

// Exceeded reports, without counting, whether key's live window already holds limit attempts.
func (s *Shared) Exceeded(ctx context.Context, key string, limit int) (bool, error) {
	var over bool
	err := s.row(ctx, `SELECT EXISTS(SELECT 1 FROM shared_windows WHERE key = $1 AND expires_at > `+dbMillis+` AND hits >= $2)`, []any{key, limit}, &over)
	return over, err
}

// Refund returns one attempt to key's live window.
func (s *Shared) Refund(ctx context.Context, key string) error {
	_, err := s.exec(ctx, `UPDATE shared_windows SET hits = hits - 1 WHERE key = $1 AND hits > 0 AND expires_at > `+dbMillis, key)
	return err
}

// Reset ends key's window.
func (s *Shared) Reset(ctx context.Context, key string) error {
	_, err := s.exec(ctx, `DELETE FROM shared_windows WHERE key = $1`, key)
	return err
}

// Spend records id of kind as used until until, and reports whether this call was the first
// to use it: of two replicas spending one key at once, exactly one gets true.
func (s *Shared) Spend(ctx context.Context, kind, id string, until time.Time) (bool, error) {
	if kind == "" || id == "" {
		return false, errors.New("invalid one-time key")
	}
	n, err := s.exec(ctx, `INSERT INTO shared_spent (kind, id, expires_at) VALUES ($1, $2, $3)
ON CONFLICT (kind, id) DO UPDATE SET expires_at = EXCLUDED.expires_at WHERE shared_spent.expires_at <= `+dbMillis, kind, id, until.UnixMilli())
	return err == nil && n == 1, err
}

// Unspend releases a key Spend recorded, so a retry of work that failed is accepted.
func (s *Shared) Unspend(ctx context.Context, kind, id string) error {
	_, err := s.exec(ctx, `DELETE FROM shared_spent WHERE kind = $1 AND id = $2`, kind, id)
	return err
}

// Ticket is a conversion ticket's file: what serving its bytes needs, and no file name.
type Ticket struct {
	Blob, Digest, Name string
	Sealed             bool
	Size               int64
}

// PutTicket stores ticket t under id for life.
func (s *Shared) PutTicket(ctx context.Context, id string, t Ticket, life time.Duration) error {
	_, err := s.exec(ctx, `INSERT INTO shared_tickets (id, blob, sealed, size, digest, name, expires_at) VALUES ($1, $2, $3, $4, $5, $6, `+dbMillis+` + $7)`,
		id, t.Blob, t.Sealed, t.Size, t.Digest, t.Name, life.Milliseconds())
	return err
}

// GetTicket returns id's ticket while it is live.
func (s *Shared) GetTicket(ctx context.Context, id string) (Ticket, bool, error) {
	var t Ticket
	err := s.row(ctx, `SELECT blob, sealed, size, digest, name FROM shared_tickets WHERE id = $1 AND expires_at > `+dbMillis, []any{id},
		&t.Blob, &t.Sealed, &t.Size, &t.Digest, &t.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Ticket{}, false, nil
	}
	return t, err == nil, err
}

// DropTicket ends id's ticket.
func (s *Shared) DropTicket(ctx context.Context, id string) error {
	_, err := s.exec(ctx, `DELETE FROM shared_tickets WHERE id = $1`, id)
	return err
}

// Sweep deletes every expired row. The elected worker runs it.
func (s *Shared) Sweep(ctx context.Context) error {
	for _, table := range sharedTables {
		if _, err := s.exec(ctx, `DELETE FROM `+table+` WHERE expires_at <= `+dbMillis); err != nil {
			return err
		}
	}
	return nil
}
