package drive

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
)

type rowser interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func column(ctx context.Context, q rowser, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// subtree returns root and every descendant folder matching cond, a constant SQL condition
// over alias d. UNION stops on a cycle left by older data.
func subtree(ctx context.Context, tx *sql.Tx, root, cond string, args ...any) ([]string, error) {
	return column(ctx, tx, `WITH RECURSIVE tree(id) AS (SELECT ? UNION SELECT d.id FROM drive_folders d JOIN tree t ON d.parent=t.id WHERE `+cond+`) SELECT id FROM tree`, append([]any{root}, args...)...)
}

// SetFolderTrash trashes a folder and everything live beneath it as one batch, or restores
// exactly that batch. Items trashed earlier stay in the trash. A folder whose parent is
// still trashed comes back at the workspace root.
func (s *Store) SetFolderTrash(ctx context.Context, user, id string, trash bool) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace, parent, batch string
		if err := tx.QueryRowContext(ctx, `SELECT workspace,parent,trash_batch FROM drive_folders WHERE id=? AND trashed=?`, id, !trash).Scan(&workspace, &parent, &batch); err != nil {
			if err == sql.ErrNoRows {
				var n int
				if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_folders WHERE id=?`, id).Scan(&n); err == nil && n == 1 {
					return ErrConflict
				}
				return ErrDenied
			}
			return err
		}
		if err := authorize(ctx, tx, user, workspace, 2); err != nil {
			return err
		}
		if trash {
			tree, err := subtree(ctx, tx, id, `d.trashed=false`)
			if err != nil {
				return err
			}
			batch, stamp := uuid.NewString(), now()
			for _, folder := range tree {
				if _, err = tx.ExecContext(ctx, `UPDATE drive_folders SET trashed=true,trashed_at=?,trash_batch=? WHERE id=?`, stamp, batch, folder); err != nil {
					return err
				}
				if _, err = tx.ExecContext(ctx, `UPDATE drive_files SET trashed=true,trashed_at=?,trash_batch=? WHERE parent=? AND trashed=false`, stamp, batch, folder); err != nil {
					return err
				}
			}
			return event(ctx, tx, user, "folder.trashed", id)
		}
		tree, err := subtree(ctx, tx, id, `d.trash_batch=?`, batch)
		if err != nil {
			return err
		}
		if parent != "" {
			var live int
			if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_folders WHERE id=? AND trashed=false`, parent).Scan(&live); err != nil {
				return err
			}
			if live == 0 {
				if _, err = tx.ExecContext(ctx, `UPDATE drive_folders SET parent='' WHERE id=?`, id); err != nil {
					return err
				}
			}
		}
		for _, folder := range tree {
			if _, err = tx.ExecContext(ctx, `UPDATE drive_files SET trashed=false,trashed_at='',trash_batch='' WHERE parent=? AND trash_batch=?`, folder, batch); err != nil {
				return ErrConflict
			}
			if _, err = tx.ExecContext(ctx, `UPDATE drive_folders SET trashed=false,trashed_at='',trash_batch='' WHERE id=?`, folder); err != nil {
				return ErrConflict
			}
		}
		return event(ctx, tx, user, "folder.restored", id)
	})
}
