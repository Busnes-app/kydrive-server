package drive

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

// OrgKeys is for org-owned background work; every call rechecks serving state before
// consulting the short-lived key cache, so a purge/suspension fences a loaded snapshot.
func (s *Store) OrgKeys(ctx context.Context, org string) (Keys, error) {
	_, active, err := s.OrgActive(ctx, org)
	if err != nil {
		return Keys{}, err
	}
	if !active {
		return Keys{}, ErrDenied
	}
	k, err := s.unwrap(ctx, s.db, org)
	return k.keys, err
}
func (k Keys) SearchKey() ([]byte, error) {
	return crypto.DeriveKey(k.key, "kydrive:org-search:"+k.org)
}
func (k Keys) Org() string { return k.org }

// SearchPointer is the committed immutable search snapshot. Cursor and queue are shared
// database state, independent of a replica's memory or the success of an S3 write.
type SearchPointer struct {
	Object          string
	Version, Cursor int64
}
type SearchWork struct {
	File       string
	Generation int64
}

func (s *Store) SearchPointer(ctx context.Context, org string) (SearchPointer, error) {
	var p SearchPointer
	err := s.db.QueryRowContext(ctx, `SELECT object,version,cursor FROM drive_search_index WHERE org_id=?`, org).Scan(&p.Object, &p.Version, &p.Cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	return p, err
}
func (s *Store) SearchEnqueue(ctx context.Context, org string, files []string, at time.Time, delay time.Duration, cursor int64) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var active bool
		if err := tx.QueryRowContext(ctx, `SELECT `+orgServing("o")+` FROM drive_orgs o WHERE o.id=?`, org).Scan(&active); err != nil {
			return err
		}
		if !active {
			return ErrDenied
		}
		for _, file := range files {
			if _, err := tx.ExecContext(ctx, `INSERT INTO drive_search_queue(org_id,file,first,not_before,generation) VALUES(?,?,?,?,1) ON CONFLICT(org_id,file) DO UPDATE SET not_before=min(max(drive_search_queue.not_before,excluded.not_before),drive_search_queue.first+60),generation=drive_search_queue.generation+1`, org, file, at.Unix(), at.Add(delay).Unix()); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO drive_search_index(org_id,cursor) VALUES(?,?) ON CONFLICT(org_id) DO UPDATE SET cursor=max(drive_search_index.cursor,excluded.cursor)`, org, max(0, cursor))
		return err
	})
}
func (s *Store) SearchDue(ctx context.Context, org string, at time.Time, force bool, limit int) ([]SearchWork, error) {
	due := at.Unix()
	if force {
		due = 1 << 62
	}
	rows, err := s.db.QueryContext(ctx, `SELECT file,generation FROM drive_search_queue WHERE org_id=? AND not_before<=? ORDER BY first,file LIMIT ?`, org, due, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchWork
	for rows.Next() {
		var work SearchWork
		if err := rows.Scan(&work.File, &work.Generation); err != nil {
			return nil, err
		}
		out = append(out, work)
	}
	return out, rows.Err()
}

// PublishSearch moves the pointer and acknowledges exactly the queue generations processed
// into it, in one transaction. A losing publisher cleans up only its own new S3 object.
func (s *Store) PublishSearch(ctx context.Context, org string, old SearchPointer, object string, done []SearchWork) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_search_index(org_id) SELECT ? WHERE EXISTS(SELECT 1 FROM drive_orgs o WHERE o.id=? AND `+orgServing("o")+`) ON CONFLICT(org_id) DO NOTHING`, org, org); err != nil {
			return err
		}
		n, err := rowsAffected(tx.ExecContext(ctx, `UPDATE drive_search_index SET object=?,version=version+1 WHERE org_id=? AND version=? AND EXISTS(SELECT 1 FROM drive_orgs o WHERE o.id=? AND `+orgServing("o")+`)`, object, org, old.Version, org))
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		for _, work := range done {
			if _, err := tx.ExecContext(ctx, `DELETE FROM drive_search_queue WHERE org_id=? AND file=? AND generation=?`, org, work.File, work.Generation); err != nil {
				return err
			}
		}
		return nil
	})
}
