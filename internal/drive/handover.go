package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Target is a handover destination: a shared workspace or a colleague's My files.
type Target struct{ Workspace, User string }

// Leaver is a personal workspace whose owner can no longer use it. It carries counts, never names.
type Leaver struct {
	Workspace string `json:"workspace"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	Size      int64  `json:"size"`
	Items     int    `json:"items"`
}

type Colleague struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// OperatorActor records handovers run from the server CLI.
const OperatorActor = "system:operator-cli"

// ownerLabel prefers the live account name, then the name kept for a deleted account.
const ownerLabel = `COALESCE(NULLIF(u.display_name,''),NULLIF(u.username,''),NULLIF(p.owner_name,''),'a removed account')`

// Leavers lists non-empty personal workspaces whose owner is missing or not active.
func (s *Store) Leavers(ctx context.Context, user string) ([]Leaver, error) {
	if err := admin(ctx, s.db, user); err != nil {
		return nil, err
	}
	return s.OperatorLeavers(ctx)
}

// OperatorLeavers is Leavers for the server CLI.
func (s *Store) OperatorLeavers(ctx context.Context) ([]Leaver, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT * FROM (SELECT p.workspace,`+ownerLabel+`,COALESCE(u.status,'deleted'),COALESCE((SELECT SUM(v.size) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=p.workspace),0),(SELECT COUNT(*) FROM drive_files WHERE workspace=p.workspace)+(SELECT COUNT(*) FROM drive_folders WHERE workspace=p.workspace) AS items FROM drive_personal_workspaces p LEFT JOIN users u ON u.id=p.owner WHERE u.id IS NULL OR u.status!='active') WHERE items>0 ORDER BY 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Leaver{}
	for rows.Next() {
		var l Leaver
		if err = rows.Scan(&l.Workspace, &l.Name, &l.Status, &l.Size, &l.Items); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Colleagues lists active accounts that can receive a handover into their My files.
func (s *Store) Colleagues(ctx context.Context, user string) ([]Colleague, error) {
	if err := admin(ctx, s.db, user); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,COALESCE(NULLIF(display_name,''),username) FROM users WHERE status='active' ORDER BY 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Colleague{}
	for rows.Next() {
		var c Colleague
		if err = rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Handover moves a leaver's whole personal tree, trash and versions included, into a new
// folder at the target root and returns that folder. The admin gains no read access.
func (s *Store) Handover(ctx context.Context, user, source string, to Target) (string, error) {
	return s.handover(ctx, user, source, to, false)
}

// OperatorHandover is Handover for the server CLI, whose operator already holds the database.
// It also writes an audit record.
func (s *Store) OperatorHandover(ctx context.Context, source string, to Target) (string, error) {
	return s.handover(ctx, OperatorActor, source, to, true)
}

func (s *Store) handover(ctx context.Context, user, source string, to Target, operator bool) (string, error) {
	if (to.Workspace == "") == (to.User == "") {
		return "", ErrInvalid
	}
	folder := uuid.NewString()
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service {
			return ErrDenied
		}
		if !operator {
			if err := admin(ctx, tx, user); err != nil {
				return err
			}
		}
		var owner, status string
		err := tx.QueryRowContext(ctx, `SELECT `+ownerLabel+`,COALESCE(u.status,'deleted') FROM drive_personal_workspaces p LEFT JOIN users u ON u.id=p.owner WHERE p.workspace=?`, source).Scan(&owner, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		if status == "active" {
			return fmt.Errorf("%w: owner is active", ErrConflict)
		}
		target := to.Workspace
		if to.User != "" {
			if target, err = ensurePersonal(ctx, tx, to.User); err != nil {
				return err
			}
		} else if err = sharedWorkspace(ctx, tx, target); err != nil {
			return err
		}
		if target == source {
			return ErrInvalid
		}
		var files, folders int
		var size int64
		if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM drive_files WHERE workspace=?),(SELECT COUNT(*) FROM drive_folders WHERE workspace=?),COALESCE((SELECT SUM(v.size) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=?),0)`, source, source, source).Scan(&files, &folders, &size); err != nil {
			return err
		}
		if files+folders == 0 {
			return fmt.Errorf("%w: nothing to hand over", ErrConflict)
		}
		if err = fits(ctx, tx, target, size); err != nil {
			return err
		}
		if err = handoverFolder(ctx, tx, folder, target, owner); err != nil {
			return err
		}
		// Sessions and tokens were authorized for the source; as in MoveFile, closed documents
		// fence callbacks in flight and the new epoch gives later sessions fresh keys.
		for _, q := range []string{
			`UPDATE drive_editor_sessions SET revoked=true WHERE revoked=false AND file_id IN (SELECT id FROM drive_files WHERE workspace=?)`,
			`UPDATE drive_editor_documents SET closed=true WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?)`,
			`UPDATE drive_files SET editor_epoch=editor_epoch+1 WHERE workspace=?`,
			`UPDATE drive_service_tokens SET revoked=true WHERE workspace=?`,
		} {
			if _, err = tx.ExecContext(ctx, q, source); err != nil {
				return err
			}
		}
		// Trashed items stay trashed, restarting their clock so the target's retention cannot purge them at once.
		stamp := now()
		for _, table := range []string{"drive_folders", "drive_files"} {
			if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET workspace=?,parent=CASE parent WHEN '' THEN ? ELSE parent END,trashed_at=CASE WHEN trashed THEN ? ELSE trashed_at END WHERE workspace=?`, target, folder, stamp, source); err != nil {
				return err
			}
		}
		resource := fmt.Sprintf("%s->%s:%s files=%d folders=%d bytes=%d", source, target, folder, files, folders, size)
		if operator {
			if _, err = tx.ExecContext(ctx, `INSERT INTO audit_records(user_id,action,resource,details,ip_address,created_at) VALUES(?,?,?,?,?,?)`, user, "drive.handover", source, resource, "operator-cli", time.Now().UTC()); err != nil {
				return err
			}
		}
		return event(ctx, tx, user, "workspace.handed_over", resource)
	})
	if err != nil {
		return "", err
	}
	return folder, nil
}

// handoverFolder creates "Handover from <owner> <date>" at the target root, numbering it when taken.
func handoverFolder(ctx context.Context, tx *sql.Tx, id, workspace, owner string) error {
	date := time.Now().UTC().Format(time.DateOnly)
	base := "Handover from " + owner + " " + date
	if !ValidName(base + " (100)") {
		base = "Handover from a removed account " + date
	}
	for n := 1; n <= 100; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO drive_folders(id,workspace,parent,name) VALUES(?,?,?,?)`, id, workspace, "", name)
		if !uniqueViolation(err) {
			return err
		}
	}
	return fmt.Errorf("%w: name exists", ErrConflict)
}
