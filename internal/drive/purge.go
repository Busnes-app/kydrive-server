package drive

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// unreferenced returns the candidates no version row still names. Called inside the purging
// transaction, so a copy or restore committed after it sees the blob gone and fails.
func unreferenced(ctx context.Context, tx *sql.Tx, blobs []string) ([]string, error) {
	out := []string{}
	for _, b := range blobs {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_versions WHERE blob=?`, b).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			out = append(out, b)
		}
	}
	return out, nil
}

// purgeFile deletes a trashed file and its history once no editor can still save to it.
func purgeFile(ctx context.Context, tx *sql.Tx, id string, at int64) ([]string, error) {
	var open int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_editor_sessions WHERE file_id=? AND (revoked=false OR drop_done=false) AND expires>?`, id, at).Scan(&open); err != nil {
		return nil, err
	}
	if open > 0 {
		return nil, fmt.Errorf("%w: editor session still open", ErrConflict)
	}
	blobs, err := column(ctx, tx, `SELECT DISTINCT blob FROM drive_versions WHERE file_id=?`, id)
	if err != nil {
		return nil, err
	}
	for _, q := range []string{
		`DELETE FROM drive_editor_saves WHERE key IN (SELECT key FROM drive_editor_documents WHERE file_id=?)`,
		`DELETE FROM drive_editor_documents WHERE file_id=?`,
		`DELETE FROM drive_editor_sessions WHERE file_id=?`,
		`DELETE FROM drive_versions WHERE file_id=?`,
		`DELETE FROM drive_files WHERE id=? AND trashed=true`,
	} {
		if _, err = tx.ExecContext(ctx, q, id); err != nil {
			return nil, err
		}
	}
	return unreferenced(ctx, tx, blobs)
}

func purgeVersion(ctx context.Context, tx *sql.Tx, id string, revision, current, at int64) ([]string, error) {
	if revision >= current {
		return nil, ErrInvalid
	}
	var open int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_editor_sessions WHERE file_id=? AND revision=? AND revoked=false AND expires>?`, id, revision, at).Scan(&open); err != nil {
		return nil, err
	}
	if open > 0 {
		return nil, fmt.Errorf("%w: version open in editor", ErrConflict)
	}
	var blob string
	if err := tx.QueryRowContext(ctx, `SELECT blob FROM drive_versions WHERE file_id=? AND revision=?`, id, revision).Scan(&blob); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrInvalid
		}
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM drive_versions WHERE file_id=? AND revision=?`, id, revision); err != nil {
		return nil, err
	}
	return unreferenced(ctx, tx, []string{blob})
}

func (s *Store) PurgeFile(ctx context.Context, user, id string) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace string
		var trashed bool
		if err := tx.QueryRowContext(ctx, `SELECT workspace,trashed FROM drive_files WHERE id=?`, id).Scan(&workspace, &trashed); err != nil {
			if err == sql.ErrNoRows {
				return ErrDenied
			}
			return err
		}
		if err := authorize(ctx, tx, user, workspace, 3); err != nil {
			return err
		}
		if !trashed {
			return fmt.Errorf("%w: move to trash first", ErrConflict)
		}
		var err error
		if blobs, err = purgeFile(ctx, tx, id, time.Now().Unix()); err != nil {
			return err
		}
		return event(ctx, tx, user, "file.purged", id)
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}

func (s *Store) PurgeFolder(ctx context.Context, user, id string) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace string
		var trashed bool
		if err := tx.QueryRowContext(ctx, `SELECT workspace,trashed FROM drive_folders WHERE id=?`, id).Scan(&workspace, &trashed); err != nil {
			if err == sql.ErrNoRows {
				return ErrDenied
			}
			return err
		}
		if err := authorize(ctx, tx, user, workspace, 3); err != nil {
			return err
		}
		if !trashed {
			return fmt.Errorf("%w: move to trash first", ErrConflict)
		}
		tree, err := subtree(ctx, tx, id, `d.trashed=true`)
		if err != nil {
			return err
		}
		at := time.Now().Unix()
		for _, folder := range tree {
			var live int
			if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM drive_files WHERE parent=? AND trashed=false)+(SELECT COUNT(*) FROM drive_folders WHERE parent=? AND trashed=false)`, folder, folder).Scan(&live); err != nil {
				return err
			}
			if live > 0 {
				return fmt.Errorf("%w: folder has live items", ErrConflict)
			}
			files, err := column(ctx, tx, `SELECT id FROM drive_files WHERE parent=?`, folder)
			if err != nil {
				return err
			}
			for _, f := range files {
				b, err := purgeFile(ctx, tx, f, at)
				if err != nil {
					return err
				}
				blobs = append(blobs, b...)
			}
			if _, err = tx.ExecContext(ctx, `DELETE FROM drive_folders WHERE id=?`, folder); err != nil {
				return err
			}
		}
		return event(ctx, tx, user, "folder.purged", id)
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}

func (s *Store) PurgeVersion(ctx context.Context, user, id string, revision int64) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace string
		var current int64
		if err := tx.QueryRowContext(ctx, `SELECT workspace,revision FROM drive_files WHERE id=?`, id).Scan(&workspace, &current); err != nil {
			if err == sql.ErrNoRows {
				return ErrDenied
			}
			return err
		}
		if err := authorize(ctx, tx, user, workspace, 3); err != nil {
			return err
		}
		var err error
		if blobs, err = purgeVersion(ctx, tx, id, revision, current, time.Now().Unix()); err != nil {
			return err
		}
		return event(ctx, tx, user, "version.purged", fmt.Sprintf("%s:%d", id, revision))
	})
	if err != nil {
		return nil, err
	}
	return blobs, nil
}
