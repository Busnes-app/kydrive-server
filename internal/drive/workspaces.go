package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// nameFree refuses a shared workspace name already used by another shared workspace, ignoring
// case; except is the workspace being renamed. Every My files shares one name and is exempt.
func nameFree(ctx context.Context, tx *sql.Tx, name, except string) error {
	var taken int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_workspaces w LEFT JOIN drive_personal_workspaces p ON p.workspace=w.id WHERE p.workspace IS NULL AND lower(w.name)=lower(?) AND w.id!=?`, name, except).Scan(&taken); err != nil {
		return err
	}
	if taken > 0 {
		return fmt.Errorf("%w: workspace name exists", ErrConflict)
	}
	return nil
}

// RenameWorkspace renames a shared workspace. Admin only; My files cannot be renamed.
func (s *Store) RenameWorkspace(ctx context.Context, user, workspace, name string) error {
	if !ValidName(name) {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, user); err != nil {
			return err
		}
		if err := sharedWorkspace(ctx, tx, workspace); err != nil {
			return err
		}
		if err := nameFree(ctx, tx, name, workspace); err != nil {
			return err
		}
		var old string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM drive_workspaces WHERE id=?`, workspace).Scan(&old); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE drive_workspaces SET name=? WHERE id=?`, name, workspace); err != nil {
			return err
		}
		return event(ctx, tx, user, "workspace.renamed", fmt.Sprintf("%s from=%q to=%q", workspace, old, name))
	})
}

// DeleteWorkspace removes an empty shared workspace and every access to it. Admin only; My
// files is refused. The workspace must hold no live or trashed file or folder and no upload in
// progress, checked in the deleting transaction so nothing can land between check and delete.
// An unknown workspace is already gone: nil, and no event.
func (s *Store) DeleteWorkspace(ctx context.Context, user, workspace string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, user); err != nil {
			return err
		}
		var name string
		var personal bool
		err := tx.QueryRowContext(ctx, `SELECT w.name,p.workspace IS NOT NULL FROM drive_workspaces w LEFT JOIN drive_personal_workspaces p ON p.workspace=w.id WHERE w.id=?`, workspace).Scan(&name, &personal)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if personal {
			return ErrDenied
		}
		now := time.Now().Unix()
		var items, uploads int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM drive_files WHERE workspace=?)+(SELECT COUNT(*) FROM drive_folders WHERE workspace=?),(SELECT COUNT(*) FROM drive_upload_sessions WHERE workspace=? AND (expires>? OR completing>?))`,
			workspace, workspace, workspace, now, now-int64(completionClaim/time.Second)).Scan(&items, &uploads); err != nil {
			return err
		}
		if items > 0 {
			return fmt.Errorf("%w: workspace is not empty", ErrConflict)
		}
		if uploads > 0 {
			return fmt.Errorf("%w: workspace has uploads in progress", ErrConflict)
		}
		// Expired upload sessions go too; ExpireUploads sweeps their staging directories. A
		// labelled step's row count goes into the event.
		steps := []struct {
			label, query string
		}{
			{"", `DELETE FROM drive_upload_segments WHERE session IN (SELECT id FROM drive_upload_sessions WHERE workspace=?)`},
			{"", `DELETE FROM drive_upload_sessions WHERE workspace=?`},
			{"", `DELETE FROM drive_link_users WHERE link IN (SELECT id FROM drive_links WHERE workspace=?)`},
			{"links", `DELETE FROM drive_links WHERE workspace=?`},
			{"shares", `DELETE FROM drive_shares WHERE workspace=?`},
			{"grants", `DELETE FROM drive_grants WHERE workspace=?`},
			{"service_tokens", `UPDATE drive_service_tokens SET revoked=true WHERE workspace=? AND revoked=false`},
			{"agent_tokens", `DELETE FROM drive_app_passwords WHERE root_workspace=?`},
			{"", `DELETE FROM drive_changes WHERE workspace=?`},
			{"", `DELETE FROM drive_change_floor WHERE workspace=?`},
			{"", `DELETE FROM drive_workspaces WHERE id=?`},
		}
		resource := fmt.Sprintf("%s name=%q", workspace, name)
		for _, c := range steps {
			res, err := tx.ExecContext(ctx, c.query, workspace)
			if err != nil {
				return err
			}
			if c.label == "" {
				continue
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			resource += fmt.Sprintf(" %s=%d", c.label, n)
		}
		return event(ctx, tx, user, "workspace.deleted", resource)
	})
}
