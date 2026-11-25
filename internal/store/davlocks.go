package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"slices"
	"time"

	"golang.org/x/net/webdav"
)

// davHold bounds a lock without a timeout (the ones x/net takes around a write without an If
// header) and a request's hold on a lock, so a replica that dies mid-request blocks the item
// no longer than a crashed client's LOCK can (internal/dav maxLock).
const davHold = time.Hour

// davExpired is a lock past its timeout and held by no request: x/net never expires a held one.
const davExpired = `expires_at <= ` + dbMillis + ` AND held_until <= ` + dbMillis

// DavLocks is x/net's in-memory WebDAV lock table (webdav.NewMemLS) on PostgreSQL, so every
// replica sees every lock. It keeps memLS's rules: a lock conflicts with a lock on the same
// name, an infinite-depth lock on an ancestor, or (for an infinite-depth request) any lock
// below; a request that presents a token holds that lock until it ends, and a held lock
// cannot be presented again, refreshed or released. Each call is one transaction under one
// advisory lock: lock traffic is light, and the conflict check must see every other write.
type DavLocks struct{ shared *Shared }

func (s *Shared) DavLocks() *DavLocks { return &DavLocks{s} }

// davTokens is how many presented tokens Confirm considers, in order. A client presents one
// or two; more only lengthen work done under the table's one lock.
const davTokens = 32

func (l *DavLocks) tx(ctx context.Context, work func(*sql.Tx) error) error {
	return l.shared.readCommitted(ctx, func(tx *sql.Tx) error {
		// Bounds every statement here, the wait for the table's lock included, so no request
		// holds or queues for it long.
		if _, err := tx.ExecContext(ctx, `SET LOCAL statement_timeout = 5000`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('kydrive dav locks'))`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM shared_dav_locks WHERE `+davExpired); err != nil {
			return err
		}
		return work(tx)
	})
}

func davMillis(d time.Duration) int64 {
	if d < 0 {
		d = davHold
	}
	return d.Milliseconds()
}

// Create locks d.Root (a clean absolute path) for d.Duration, a negative one meaning davHold.
func (l *DavLocks) Create(ctx context.Context, d webdav.LockDetails) (string, error) {
	raw := make([]byte, 16)
	rand.Read(raw)
	token := hex.EncodeToString(raw)
	below := d.Root + "/"
	if d.Root == "/" {
		below = "/"
	}
	err := l.tx(ctx, func(tx *sql.Tx) error {
		var locked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM shared_dav_locks WHERE root = $1
OR (NOT $2 AND starts_with(root, $3))
OR (NOT zero_depth AND (root = '/' OR starts_with($1, root || '/'))))`, d.Root, d.ZeroDepth, below).Scan(&locked); err != nil {
			return err
		}
		if locked {
			return webdav.ErrLocked
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO shared_dav_locks (token, root, zero_depth, owner_xml, expires_at) VALUES ($1, $2, $3, $4, `+dbMillis+` + $5)`,
			token, d.Root, d.ZeroDepth, d.OwnerXML, davMillis(d.Duration))
		return err
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// Confirm finds, for each of name0 and name1 that is not "", the first of the first davTokens
// distinct presented tokens whose unheld lock covers it, and holds those locks until release.
// Any name left uncovered is webdav.ErrConfirmationFailed.
func (l *DavLocks) Confirm(ctx context.Context, name0, name1 string, tokens []string) (release func(context.Context) error, err error) {
	var candidates []string
	for _, t := range tokens {
		if t != "" && !slices.Contains(candidates, t) {
			candidates = append(candidates, t)
			if len(candidates) == davTokens {
				break
			}
		}
	}
	var held []string
	err = l.tx(ctx, func(tx *sql.Tx) error {
		for _, name := range []string{name0, name1} {
			if name == "" {
				continue
			}
			var found string
			err := tx.QueryRowContext(ctx, `SELECT token FROM shared_dav_locks WHERE token = ANY($1) AND held_until <= `+dbMillis+`
AND (root = $2 OR (NOT zero_depth AND (root = '/' OR starts_with($2, root || '/'))))
ORDER BY array_position($1, token) LIMIT 1`, candidates, name).Scan(&found)
			if errors.Is(err, sql.ErrNoRows) {
				return webdav.ErrConfirmationFailed
			} else if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE shared_dav_locks SET held_until = `+dbMillis+` + $2 WHERE token = $1`, found, davHold.Milliseconds()); err != nil {
				return err
			}
			held = append(held, found)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		return l.tx(ctx, func(tx *sql.Tx) error {
			for _, token := range held {
				// A held lock does not expire, so one whose timeout passed while held goes now.
				if _, err := tx.ExecContext(ctx, `UPDATE shared_dav_locks SET held_until = 0 WHERE token = $1`, token); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, `DELETE FROM shared_dav_locks WHERE `+davExpired)
			return err
		})
	}, nil
}

// Refresh sets token's lock to expire duration from now.
func (l *DavLocks) Refresh(ctx context.Context, token string, duration time.Duration) (webdav.LockDetails, error) {
	var d webdav.LockDetails
	err := l.tx(ctx, func(tx *sql.Tx) error {
		var held bool
		if err := tx.QueryRowContext(ctx, `SELECT root, zero_depth, owner_xml, held_until > `+dbMillis+` FROM shared_dav_locks WHERE token = $1`, token).
			Scan(&d.Root, &d.ZeroDepth, &d.OwnerXML, &held); errors.Is(err, sql.ErrNoRows) {
			return webdav.ErrNoSuchLock
		} else if err != nil {
			return err
		}
		if held {
			return webdav.ErrLocked
		}
		d.Duration = duration
		_, err := tx.ExecContext(ctx, `UPDATE shared_dav_locks SET expires_at = `+dbMillis+` + $2 WHERE token = $1`, token, davMillis(duration))
		return err
	})
	if err != nil {
		return webdav.LockDetails{}, err
	}
	return d, nil
}

// Unlock releases token's lock unless a request holds it.
func (l *DavLocks) Unlock(ctx context.Context, token string) error {
	return l.tx(ctx, func(tx *sql.Tx) error {
		var held bool
		if err := tx.QueryRowContext(ctx, `SELECT held_until > `+dbMillis+` FROM shared_dav_locks WHERE token = $1`, token).Scan(&held); errors.Is(err, sql.ErrNoRows) {
			return webdav.ErrNoSuchLock
		} else if err != nil {
			return err
		}
		if held {
			return webdav.ErrLocked
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM shared_dav_locks WHERE token = $1`, token)
		return err
	})
}
