package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// nameFree refuses a shared workspace name already used by another shared workspace, ignoring
// case, deleted ones included until purged so a restore cannot collide; except is the workspace
// being renamed. Every My files shares one name and is exempt, and no shared workspace may take it:
// WebDAV, MCP and agent folder limits name each user's own as /My files.
func nameFree(ctx context.Context, tx *sql.Tx, name, except string) error {
	if strings.EqualFold(strings.TrimSpace(name), "My files") {
		return fmt.Errorf("%w: My files is the name of each person's own files", ErrConflict)
	}
	var taken, deleted int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(w.deleted_at!=''),0) FROM drive_workspaces w LEFT JOIN drive_personal_workspaces p ON p.workspace=w.id WHERE p.workspace IS NULL AND lower(w.name)=lower(?) AND w.id!=?`, name, except).Scan(&taken, &deleted); err != nil {
		return err
	}
	if taken > deleted {
		return fmt.Errorf("%w: workspace name exists", ErrConflict)
	}
	if deleted > 0 {
		return fmt.Errorf("%w: workspace name exists in Deleted workspaces", ErrConflict)
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

// DeletedWorkspace is a shared workspace in its recovery period, for administrators: its size
// and item count, never file names.
type DeletedWorkspace struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	DeletedAt string `json:"deleted_at"`
	DeletedBy string `json:"deleted_by"`
	Used      int64  `json:"used"`
	Items     int64  `json:"items"`
}

// DeleteWorkspace deletes a shared workspace. Admin only; My files is ErrDenied. An unknown or
// already deleted workspace is nil with no event. recoverable reports which kind ran:
//
//   - Empty (no live or trashed file or folder): it is removed at once with every access to it.
//     An upload in progress refuses it (ErrConflict).
//   - Holding items: confirm must be its exact name, else ErrConflict. It is deleted
//     recoverably: every grant and share lapses (they come back on restore), and its links,
//     service tokens, AI agent tokens limited to it and editor sessions are revoked for good.
//     Its files stay until RestoreWorkspace or PurgeDeletedWorkspaces.
func (s *Store) DeleteWorkspace(ctx context.Context, user, workspace, confirm string) (recoverable bool, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, user); err != nil {
			return err
		}
		var name, deletedAt string
		var personal bool
		err := tx.QueryRowContext(ctx, `SELECT w.name,w.deleted_at,p.workspace IS NOT NULL FROM drive_workspaces w LEFT JOIN drive_personal_workspaces p ON p.workspace=w.id WHERE w.id=?`, workspace).Scan(&name, &deletedAt, &personal)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if personal {
			return ErrDenied
		}
		if deletedAt != "" {
			recoverable = true
			return nil
		}
		now := time.Now()
		var files, folders, uploads int
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM drive_files WHERE workspace=?),(SELECT COUNT(*) FROM drive_folders WHERE workspace=?),(SELECT COUNT(*) FROM drive_upload_sessions WHERE workspace=? AND (expires>? OR completing>?))`,
			workspace, workspace, workspace, now.Unix(), now.Unix()-int64(completionClaim/time.Second)).Scan(&files, &folders, &uploads); err != nil {
			return err
		}
		resource := fmt.Sprintf("%s name=%q", workspace, name)
		if files+folders > 0 {
			if confirm != name {
				return fmt.Errorf("%w: workspace is not empty; confirm with its name", ErrConflict)
			}
			recoverable = true
			resource += fmt.Sprintf(" recoverable=true files=%d folders=%d", files, folders)
			counts, err := run(ctx, tx, workspace, []step{
				{"links", `UPDATE drive_links SET revoked=1 WHERE workspace=? AND revoked=0`},
				{"service_tokens", `UPDATE drive_service_tokens SET revoked=true WHERE workspace=? AND revoked=false`},
				{"agent_tokens", `DELETE FROM drive_app_passwords WHERE root_workspace=?`},
				{"editor_sessions", `UPDATE drive_editor_sessions SET revoked=true WHERE revoked=false AND file_id IN (SELECT id FROM drive_files WHERE workspace=?)`},
				{"", `UPDATE drive_editor_documents SET closed=true WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?)`},
				{"", `UPDATE drive_files SET editor_epoch=editor_epoch+1 WHERE workspace=?`},
			})
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE drive_workspaces SET deleted_at=?,deleted_by=? WHERE id=?`, now.UTC().Format(time.RFC3339Nano), user, workspace); err != nil {
				return err
			}
			return event(ctx, tx, user, "workspace.deleted", resource+counts)
		}
		if uploads > 0 {
			return fmt.Errorf("%w: workspace has uploads in progress", ErrConflict)
		}
		counts, err := removeWorkspace(ctx, tx, workspace)
		if err != nil {
			return err
		}
		return event(ctx, tx, user, "workspace.deleted", resource+counts)
	})
	return recoverable, err
}

// RestoreWorkspace brings back a deleted shared workspace with its grants and shares. Admin
// only; a live, personal or unknown workspace is ErrNotFound. Revoked links and tokens stay
// revoked. Its name was kept reserved (nameFree), so it cannot collide.
func (s *Store) RestoreWorkspace(ctx context.Context, user, workspace string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, user); err != nil {
			return err
		}
		var name string
		err := tx.QueryRowContext(ctx, `SELECT w.name FROM drive_workspaces w WHERE w.id=? AND w.deleted_at!='' AND NOT EXISTS(SELECT 1 FROM drive_personal_workspaces p WHERE p.workspace=w.id)`, workspace).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE drive_workspaces SET deleted_at='',deleted_by='' WHERE id=?`, workspace); err != nil {
			return err
		}
		return event(ctx, tx, user, "workspace.restored", fmt.Sprintf("%s name=%q", workspace, name))
	})
}

// DeletedWorkspaces lists deleted shared workspaces, newest first. Admin only.
func (s *Store) DeletedWorkspaces(ctx context.Context, user string) ([]DeletedWorkspace, error) {
	if err := admin(ctx, s.db, user); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT w.id,w.name,w.deleted_at,COALESCE((SELECT COALESCE(NULLIF(display_name,''),username) FROM users WHERE id=w.deleted_by),'a removed account'),
 COALESCE((SELECT SUM(v.size) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=w.id),0),
 (SELECT COUNT(*) FROM drive_files f WHERE f.workspace=w.id)+(SELECT COUNT(*) FROM drive_folders d WHERE d.workspace=w.id)
 FROM drive_workspaces w WHERE w.deleted_at!='' ORDER BY w.deleted_at DESC,w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeletedWorkspace{}
	for rows.Next() {
		var d DeletedWorkspace
		if err := rows.Scan(&d.ID, &d.Name, &d.DeletedAt, &d.DeletedBy, &d.Used, &d.Items); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PurgeDeletedWorkspaces permanently removes workspaces deleted more than days ago (0 = never),
// as actor system, one transaction each, and returns the blobs left unreferenced for the caller
// to remove after commit (the orphan sweep catches any it misses). A workspace with a file
// still open in an editor (an undelivered drop) is skipped until a later run.
func (s *Store) PurgeDeletedWorkspaces(ctx context.Context, at time.Time, days int) ([]string, error) {
	if days <= 0 {
		return nil, nil
	}
	stamp := at.UTC().Format(time.RFC3339Nano)
	const expired = `deleted_at!='' AND julianday(?)-julianday(deleted_at)>?`
	ids, err := column(ctx, s.db, `SELECT id FROM drive_workspaces WHERE `+expired+` ORDER BY deleted_at`, stamp, days)
	if err != nil {
		return nil, err
	}
	var blobs []string
	for _, id := range ids {
		var b []string
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			var name string
			err := tx.QueryRowContext(ctx, `SELECT name FROM drive_workspaces WHERE id=? AND `+expired, id, stamp, days).Scan(&name)
			if errors.Is(err, sql.ErrNoRows) {
				return nil // restored meanwhile
			}
			if err != nil {
				return err
			}
			var open int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_editor_sessions WHERE drop_done=false AND file_id IN (SELECT id FROM drive_files WHERE workspace=?)`, id).Scan(&open); err != nil {
				return err
			}
			if open > 0 {
				log.Printf("workspace purge: %s skipped, %d editor sessions not yet dropped", id, open)
				return nil
			}
			candidates, err := column(ctx, tx, `SELECT DISTINCT v.blob FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=?`, id)
			if err != nil {
				return err
			}
			counts, err := run(ctx, tx, id, []step{
				{"", `DELETE FROM drive_editor_saves WHERE key IN (SELECT key FROM drive_editor_documents WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?))`},
				{"", `DELETE FROM drive_editor_documents WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?)`},
				{"", `DELETE FROM drive_editor_sessions WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?)`},
				{"versions", `DELETE FROM drive_versions WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?)`},
				{"files", `DELETE FROM drive_files WHERE workspace=?`},
				{"folders", `DELETE FROM drive_folders WHERE workspace=?`},
			})
			if err != nil {
				return err
			}
			rest, err := removeWorkspace(ctx, tx, id)
			if err != nil {
				return err
			}
			if b, err = unreferenced(ctx, tx, candidates); err != nil {
				return err
			}
			return event(ctx, tx, "system", "workspace.purged", fmt.Sprintf("%s name=%q", id, name)+counts+rest)
		})
		if err != nil {
			return blobs, err // a rolled-back purge still references b
		}
		blobs = append(blobs, b...)
	}
	return blobs, nil
}

// step is one statement over a workspace ID; a labelled step's row count goes into the event.
type step struct{ label, query string }

func run(ctx context.Context, tx *sql.Tx, workspace string, steps []step) (string, error) {
	out := ""
	for _, c := range steps {
		res, err := tx.ExecContext(ctx, c.query, workspace)
		if err != nil {
			return "", err
		}
		if c.label == "" {
			continue
		}
		n, err := res.RowsAffected()
		if err != nil {
			return "", err
		}
		out += fmt.Sprintf(" %s=%d", c.label, n)
	}
	return out, nil
}

// removeWorkspace deletes a workspace row that holds no items, with every access to it. Upload
// sessions go too; ExpireUploads sweeps their staging directories. It never touches blobs.
func removeWorkspace(ctx context.Context, tx *sql.Tx, workspace string) (string, error) {
	return run(ctx, tx, workspace, []step{
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
	})
}
