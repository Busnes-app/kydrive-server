package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// ApplyRetention purges what workspace policies have expired as of at. Each item commits on
// its own, so one document still open in an editor does not hold back the rest.
func (s *Store) ApplyRetention(ctx context.Context, at time.Time) ([]string, error) {
	stamp, unix := at.UTC().Format(time.RFC3339Nano), at.Unix()
	var blobs []string
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
			return nil
		}
		if err == nil {
			blobs = append(blobs, b...)
		}
		return err
	}
	files, err := column(ctx, s.db, `SELECT f.id FROM drive_files f JOIN drive_workspaces w ON w.id=f.workspace WHERE f.trashed=true AND w.trash_days>0 AND julianday(?)-julianday(f.trashed_at)>w.trash_days LIMIT 500`, stamp)
	if err != nil {
		return blobs, err
	}
	for _, id := range files {
		if err = each("file.retention_purged", id, func(tx *sql.Tx) ([]string, error) { return purgeFile(ctx, tx, id, unix) }); err != nil {
			return blobs, err
		}
	}
	// Leaf folders first; a deeper tree empties over successive passes.
	for {
		ids, err := column(ctx, s.db, expiredFolders+` LIMIT 500`, stamp)
		if err != nil {
			return blobs, err
		}
		deleted := 0
		for _, id := range ids {
			err = s.transaction(ctx, func(tx *sql.Tx) error {
				res, err := tx.ExecContext(ctx, `DELETE FROM drive_folders WHERE id=? AND id IN (`+expiredFolders+`)`, id, stamp)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return nil
				}
				deleted++
				return event(ctx, tx, "system", "folder.retention_purged", id)
			})
			if err != nil {
				return blobs, err
			}
		}
		if deleted == 0 {
			break
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT v.file_id,v.revision,f.revision FROM drive_versions v JOIN drive_files f ON f.id=v.file_id JOIN drive_workspaces w ON w.id=f.workspace WHERE w.keep_versions>0 AND v.revision<=f.revision-w.keep_versions LIMIT 500`)
	if err != nil {
		return blobs, err
	}
	type old struct {
		id                string
		revision, current int64
	}
	var versions []old
	for rows.Next() {
		var v old
		if err = rows.Scan(&v.id, &v.revision, &v.current); err != nil {
			rows.Close()
			return blobs, err
		}
		versions = append(versions, v)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return blobs, err
	}
	for _, v := range versions {
		if err = each("version.retention_purged", fmt.Sprintf("%s:%d", v.id, v.revision), func(tx *sql.Tx) ([]string, error) {
			return purgeVersion(ctx, tx, v.id, v.revision, v.current, unix)
		}); err != nil {
			return blobs, err
		}
	}
	return blobs, nil
}
