package drive

import (
	"context"
	"database/sql"
	"fmt"
)

type Location struct{ Workspace, Parent, Name string }

// MoveFile renames or relocates a file without creating a version. Another workspace needs
// editor rights there and room for every retained version; open editors that lose access
// are revoked by the next PendingRevocations pass.
func (s *Store) MoveFile(ctx context.Context, user, id string, expected int64, to Location) (File, error) {
	if !ValidName(to.Name) {
		return File{}, ErrInvalid
	}
	f, err := s.File(ctx, user, id, 2)
	if err != nil {
		return f, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := authorize(ctx, tx, user, f.Workspace, 2); err != nil {
			return err
		}
		if err := authorize(ctx, tx, user, to.Workspace, 2); err != nil {
			return err
		}
		if err := parentOK(ctx, tx, to.Workspace, to.Parent); err != nil {
			return err
		}
		if to.Workspace != f.Workspace {
			var size int64
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(size),0) FROM drive_versions WHERE file_id=?`, id).Scan(&size); err != nil {
				return err
			}
			if err := fits(ctx, tx, to.Workspace, size); err != nil {
				return err
			}
		}
		res, err := tx.ExecContext(ctx, `UPDATE drive_files SET workspace=?,parent=?,name=? WHERE id=? AND workspace=? AND revision=? AND trashed=false`, to.Workspace, to.Parent, to.Name, id, f.Workspace, expected)
		if uniqueViolation(err) {
			return fmt.Errorf("%w: name exists", ErrConflict)
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrConflict
		}
		return event(ctx, tx, user, "file.moved", fmt.Sprintf("%s:%s->%s", id, f.Workspace, to.Workspace))
	})
	if err != nil {
		return f, err
	}
	f.Workspace, f.Parent, f.Name = to.Workspace, to.Parent, to.Name
	return f, nil
}
