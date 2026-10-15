package drive

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type Location struct{ Workspace, Parent, Name string }

// MoveFile renames or relocates a file without creating a version. Moving to another
// workspace needs manager rights on the source, since the file leaves it, plus editor rights
// and room for every retained version on the target. It revokes every open editor session
// on the file, since each was authorized for the source workspace.
func (s *Store) MoveFile(ctx context.Context, user, id string, expected int64, to Location) (File, error) {
	if !ValidName(to.Name) {
		return File{}, ErrInvalid
	}
	f, err := s.File(ctx, user, id, 2)
	if err != nil {
		return f, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		need := 2
		if to.Workspace != f.Workspace {
			need = 3 // taking a file out of its workspace is a manager's call
		}
		if err := authorizeItem(ctx, tx, user, f.Workspace, f.Parent, f.ID, need); err != nil {
			return err
		}
		if err := authorizeItem(ctx, tx, user, to.Workspace, to.Parent, "", 2); err != nil {
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
			// Shares and links were granted within the source; they would revive if the file came back.
			if _, err := tx.ExecContext(ctx, `DELETE FROM drive_shares WHERE resource_kind='file' AND resource_id=?`, id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE drive_links SET revoked=1 WHERE resource_kind='file' AND resource_id=?`, id); err != nil {
				return err
			}
		}
		stored, mac, err := s.names.seal("drive_files", id, to.Name)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE drive_files SET workspace=?,parent=?,name=?,name_hmac=? WHERE id=? AND workspace=? AND revision=? AND trashed=false`, to.Workspace, to.Parent, stored, mac, id, f.Workspace, expected)
		if uniqueViolation(err) {
			return fmt.Errorf("%w: name exists", ErrConflict)
		}
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return cmp.Or(err, ErrConflict)
		}
		if to.Workspace != f.Workspace {
			// Sessions were authorized for the source, possibly through a credential scoped to it.
			// Closing their documents fences callbacks already in flight; the new epoch gives later
			// sessions fresh document keys.
			for _, q := range []string{
				`UPDATE drive_editor_sessions SET revoked=true WHERE file_id=? AND revoked=false`,
				`UPDATE drive_editor_documents SET closed=true WHERE file_id=?`,
				`UPDATE drive_files SET editor_epoch=editor_epoch+1 WHERE id=?`,
			} {
				if _, err := tx.ExecContext(ctx, q, id); err != nil {
					return err
				}
			}
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
		src, err := s.scanFile(tx.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE f.id=? AND f.trashed=false`, id))
		if err != nil {
			return err
		}
		if err = authorizeItem(ctx, tx, user, src.Workspace, src.Parent, src.ID, 1); err != nil {
			return err
		}
		out.Size, out.Digest, out.Blob, out.Sealed = src.Size, src.Digest, src.Blob, src.Sealed
		return s.publishTX(ctx, tx, user, out, Version{FileID: out.ID, Revision: 1, Blob: src.Blob, Digest: src.Digest, Size: src.Size, Created: now(), Sealed: src.Sealed}, 0)
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
		if err := authorizeItem(ctx, tx, user, f.Workspace, id, "", 2); err != nil {
			return err
		}
		if err := authorizeItem(ctx, tx, user, f.Workspace, parent, "", 2); err != nil {
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
		stored, mac, err := s.names.seal("drive_folders", id, name)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE drive_folders SET parent=?,name=?,name_hmac=? WHERE id=?`, parent, stored, mac, id)
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
