package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"
)

// SetRetention configures automatic purging for one workspace; 0 turns a rule off.
func (s *Store) SetRetention(ctx context.Context, user, workspace string, trashDays, keepVersions int) error {
	if trashDays < 0 || trashDays > 3650 || keepVersions < 0 || keepVersions > 1000 {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, user); err != nil {
			return err
		}
		if err := sharedWorkspace(ctx, tx, workspace); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE drive_workspaces SET trash_days=?,keep_versions=? WHERE id=?`, trashDays, keepVersions, workspace)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrInvalid
		}
		return event(ctx, tx, user, "workspace.retention_changed", fmt.Sprintf("%s:trash_days=%d:keep_versions=%d", workspace, trashDays, keepVersions))
	})
}

const expiredFolders = `SELECT d.id FROM drive_folders d JOIN drive_workspaces w ON w.id=d.workspace WHERE d.trashed=true AND w.trash_days>0 AND julianday(?)-julianday(d.trashed_at)>w.trash_days AND NOT EXISTS(SELECT 1 FROM drive_files f WHERE f.parent=d.id) AND NOT EXISTS(SELECT 1 FROM drive_folders c WHERE c.parent=d.id)`

const (
	expiredFile    = `FROM drive_files f JOIN drive_workspaces w ON w.id=f.workspace WHERE f.trashed=true AND w.trash_days>0 AND julianday(?)-julianday(f.trashed_at)>w.trash_days`
	expiredVersion = `FROM drive_versions v JOIN drive_files f ON f.id=v.file_id JOIN drive_workspaces w ON w.id=f.workspace WHERE w.keep_versions>0 AND v.revision<=f.revision-w.keep_versions`
	page           = 500
)

// retainFile purges one file if the workspace policy still expires it; no match is ErrInvalid.
func retainFile(ctx context.Context, tx *sql.Tx, id, stamp string) ([]string, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 `+expiredFile+` AND f.id=?`, stamp, id).Scan(&one)
	if err == sql.ErrNoRows {
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	return purgeFile(ctx, tx, id)
}

// retainVersion purges one version if the policy still expires it, judged against the file's current revision.
func retainVersion(ctx context.Context, tx *sql.Tx, id string, revision int64) ([]string, error) {
	var current int64
	err := tx.QueryRowContext(ctx, `SELECT f.revision `+expiredVersion+` AND v.file_id=? AND v.revision=?`, id, revision).Scan(&current)
	if err == sql.ErrNoRows {
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	return purgeVersion(ctx, tx, id, revision, current)
}

// ApplyRetention purges what workspace policies have expired as of at. Each item commits on
// its own and is re-checked against the policy in force inside that transaction, so one
// document still open in an editor does not hold back the rest. Selection pages by cursor,
// so permanently skipped items cannot starve later ones.
func (s *Store) ApplyRetention(ctx context.Context, at time.Time) ([]string, error) {
	stamp := at.UTC().Format(time.RFC3339Nano)
	var blobs []string
	skipped := 0
	defer func() {
		if skipped > 0 {
			log.Printf("retention: skipped %d items", skipped)
		}
	}()
	each := func(action, resource string, purge func(*sql.Tx) ([]string, error)) error {
		var b []string
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			var err error
			if b, err = purge(tx); err != nil {
				return err
			}
			return event(ctx, tx, "system", action, resource)
		})
		if errors.Is(err, ErrConflict) || errors.Is(err, ErrInvalid) || errors.Is(err, ErrDenied) {
			skipped++
			return nil
		}
		if err == nil {
			blobs = append(blobs, b...)
		}
		return err
	}
	// pages walks a (key, id) cursor over query, which selects two text columns ordered by them.
	pages := func(query string, args []any, visit func(key, id string) error) error {
		key, id := "", ""
		for {
			rows, err := s.db.QueryContext(ctx, query, append(args, key, key, id)...)
			if err != nil {
				return err
			}
			var keys, ids []string
			for rows.Next() {
				var k, i string
				if err = rows.Scan(&k, &i); err != nil {
					rows.Close()
					return err
				}
				keys, ids = append(keys, k), append(ids, i)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for n := range ids {
				if err = visit(keys[n], ids[n]); err != nil {
					return err
				}
			}
			if len(ids) < page {
				return nil
			}
			key, id = keys[len(keys)-1], ids[len(ids)-1]
		}
	}
	err := pages(`SELECT f.trashed_at,f.id `+expiredFile+` AND (f.trashed_at>? OR (f.trashed_at=? AND f.id>?)) ORDER BY f.trashed_at,f.id LIMIT 500`, []any{stamp}, func(_, id string) error {
		return each("file.retention_purged", id, func(tx *sql.Tx) ([]string, error) { return retainFile(ctx, tx, id, stamp) })
	})
	if err != nil {
		return blobs, err
	}
	// Leaf folders first; a deeper tree empties over successive passes.
	for deleted := 1; deleted > 0; {
		deleted = 0
		err = pages(`SELECT d.trashed_at,d.id FROM (`+expiredFolders+`) d2 JOIN drive_folders d ON d.id=d2.id WHERE (d.trashed_at>? OR (d.trashed_at=? AND d.id>?)) ORDER BY d.trashed_at,d.id LIMIT 500`, []any{stamp}, func(_, id string) error {
			return s.transaction(ctx, func(tx *sql.Tx) error {
				res, err := tx.ExecContext(ctx, `DELETE FROM drive_folders WHERE id=? AND id IN (`+expiredFolders+`)`, id, stamp)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					skipped++
					return nil
				}
				deleted++
				return event(ctx, tx, "system", "folder.retention_purged", id)
			})
		})
		if err != nil {
			return blobs, err
		}
	}
	// Versions page by (file id, revision); revision is cast to text with padding to sort as a key.
	err = pages(`SELECT v.file_id||'', printf('%020d',v.revision) `+expiredVersion+` AND (v.file_id>? OR (v.file_id=? AND printf('%020d',v.revision)>?)) ORDER BY v.file_id,v.revision LIMIT 500`, nil, func(file, rev string) error {
		revision, _ := strconv.ParseInt(rev, 10, 64)
		return each("version.retention_purged", fmt.Sprintf("%s:%d", file, revision), func(tx *sql.Tx) ([]string, error) {
			return retainVersion(ctx, tx, file, revision)
		})
	})
	return blobs, err
}
