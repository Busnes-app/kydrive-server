package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type Location struct{ Workspace, Parent, Name string }

// MoveFile renames or relocates a file without creating a version. Another workspace needs
// manager rights on the source (the move removes the file from it) and editor rights there and room for every retained version; open editors that lose access
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
		if to.Workspace != f.Workspace {
			if err := authorize(ctx, tx, user, f.Workspace, 3); err != nil {
				return err
			}
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

// CopyFile publishes the source's current bytes as revision 1 of a new file. The source is
// read inside the same transaction, so a concurrent purge cannot remove the shared blob.
func (s *Store) CopyFile(ctx context.Context, user, id string, to Location) (File, error) {
	out := File{ID: uuid.NewString(), Workspace: to.Workspace, Parent: to.Parent, Name: to.Name, Revision: 1}
	if !ValidName(to.Name) {
		return out, ErrInvalid
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		src, err := scanFile(tx.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE f.id=? AND f.trashed=false`, id))
		if err != nil {
			return err
		}
		if err = authorize(ctx, tx, user, src.Workspace, 1); err != nil {
			return err
		}
		out.Size, out.Digest, out.Blob = src.Size, src.Digest, src.Blob
		return publishTX(ctx, tx, user, out, Version{FileID: out.ID, Revision: 1, Blob: src.Blob, Digest: src.Digest, Size: src.Size, Created: now()}, 0)
	})
	return out, err
}

// MoveFolder renames or re-parents a live folder within its workspace.
func (s *Store) MoveFolder(ctx context.Context, user, id, parent, name string) (Folder, error) {
	f := Folder{ID: id, Parent: parent, Name: name}
	if !ValidName(name) || parent == id {
		return f, ErrInvalid
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT workspace FROM drive_folders WHERE id=? AND trashed=false`, id).Scan(&f.Workspace)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		if err := authorize(ctx, tx, user, f.Workspace, 2); err != nil {
			return err
		}
		if err := parentOK(ctx, tx, f.Workspace, parent); err != nil {
			return err
		}
		var cycle int
		if err := tx.QueryRowContext(ctx, `WITH RECURSIVE up(id) AS (SELECT ? UNION SELECT d.parent FROM drive_folders d JOIN up ON d.id=up.id WHERE d.parent!='') SELECT COUNT(*) FROM up WHERE id=?`, parent, id).Scan(&cycle); err != nil {
			return err
		}
		if cycle > 0 {
			return ErrInvalid
		}
		_, err = tx.ExecContext(ctx, `UPDATE drive_folders SET parent=?,name=? WHERE id=?`, parent, name, id)
		if uniqueViolation(err) {
			return fmt.Errorf("%w: name exists", ErrConflict)
		}
		if err != nil {
			return err
		}
		return event(ctx, tx, user, "folder.moved", id)
	})
	return f, err
}
