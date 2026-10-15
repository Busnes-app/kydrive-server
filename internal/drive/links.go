package drive

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
)

// An organisation link gives any active signed-in user who opens it its role (reader or
// editor) on one folder or file, until it is revoked or expires. The token is 256 random bits
// shown once; only its SHA-256 is stored. Opening records the user, so access follows the link.
const maxLinkLife = 366 * 24 * time.Hour

type Link struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Expires   int64  `json:"expires"` // unix seconds, 0 for never
	Created   string `json:"created"`
	CreatedBy string `json:"created_by"`
	Opened    int    `json:"opened"`
}

// CreateLink returns the new link and its token, which is not stored.
func (s *Store) CreateLink(ctx context.Context, user, kind, id, role string, expires int64) (Link, string, error) {
	l := Link{ID: uuid.NewString(), Role: role, Expires: expires, Created: now(), CreatedBy: user}
	if role != "reader" && role != "editor" {
		return l, "", ErrInvalid
	}
	if expires != 0 && (expires <= time.Now().Unix() || expires > time.Now().Add(maxLinkLife).Unix()) {
		return l, "", ErrInvalid
	}
	raw := make([]byte, 32)
	rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		workspace, folder, file, trashed, err := resource(ctx, tx, kind, id)
		if err != nil {
			return err
		}
		if err = mayShare(ctx, tx, user, workspace, folder, file, role); err != nil {
			return err
		}
		if trashed {
			return ErrConflict
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO drive_links(id,workspace,resource_kind,resource_id,role,scope,token_hash,expires,created_by,created) VALUES(?,?,?,?,?,'org',?,?,?,?)`, l.ID, workspace, kind, id, role, tokenHash(token), expires, user, l.Created); err != nil {
			return err
		}
		return event(ctx, tx, user, "link.created", l.ID)
	})
	return l, token, err
}

// Links lists an item's live organisation links for those who may share it; never their tokens.
func (s *Store) Links(ctx context.Context, user, kind, id string) ([]Link, error) {
	workspace, folder, file, _, err := resource(ctx, s.db, kind, id)
	if err != nil {
		return nil, err
	}
	if err = mayShare(ctx, s.db, user, workspace, folder, file, "reader"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT l.id,l.role,l.expires,l.created,COALESCE((SELECT COALESCE(NULLIF(display_name,''),username) FROM users WHERE id=l.created_by),'a removed account'),(SELECT COUNT(*) FROM drive_link_users WHERE link=l.id) FROM drive_links l WHERE l.resource_kind=? AND l.resource_id=? AND l.scope='org' AND l.revoked=0 ORDER BY l.created`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err = rows.Scan(&l.ID, &l.Role, &l.Expires, &l.Created, &l.CreatedBy, &l.Opened); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RevokeLink ends a link and every access that came through it.
func (s *Store) RevokeLink(ctx context.Context, user, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var kind, rid, role string
		err := tx.QueryRowContext(ctx, `SELECT resource_kind,resource_id,role FROM drive_links WHERE id=? AND scope='org' AND revoked=0`, id).Scan(&kind, &rid, &role)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		workspace, folder, file, _, err := resource(ctx, tx, kind, rid)
		if err != nil {
			return err
		}
		if err = mayShare(ctx, tx, user, workspace, folder, file, role); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE drive_links SET revoked=1 WHERE id=?`, id); err != nil {
			return err
		}
		return event(ctx, tx, user, "link.revoked", id)
	})
}

// OpenLink gives the user the link's access and returns its item. Every way a link can be
// unusable (unknown, revoked, expired, trashed item, lapsed owner) is ErrNotFound alike.
func (s *Store) OpenLink(ctx context.Context, user, token string) (SharedItem, error) {
	var item SharedItem
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service {
		return item, ErrDenied
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var link string
		err := tx.QueryRowContext(ctx, `SELECT id,resource_kind,resource_id FROM drive_links WHERE token_hash=? AND scope='org' AND revoked=0 AND (expires=0 OR expires>?)`, tokenHash(token), time.Now().Unix()).Scan(&link, &item.Kind, &item.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		workspace, folder, file, trashed, err := resource(ctx, tx, item.Kind, item.ID)
		if errors.Is(err, ErrDenied) || trashed {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO drive_link_users(link,user_id,opened) VALUES(?,?,?) ON CONFLICT(link,user_id) DO UPDATE SET opened=excluded.opened`, link, user, now()); err != nil {
			return err
		}
		rank, err := itemRank(ctx, tx, user, workspace, folder, file)
		if err != nil {
			return err
		}
		if rank == 0 {
			return ErrNotFound // inactive user, or a personal workspace whose owner is inactive
		}
		item.Workspace, item.Role = workspace, roles[rank]
		if item.Kind == "file" {
			item.Parent = folder
		}
		return event(ctx, tx, user, "link.opened", link)
	})
	if err != nil {
		return SharedItem{}, err
	}
	return s.describe(ctx, item)
}


// describe fills in an item's name, parent and workspace name for the user's view.
func (s *Store) describe(ctx context.Context, item SharedItem) (SharedItem, error) {
	table := map[string]string{"file": "drive_files", "folder": "drive_folders"}[item.Kind]
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT i.name,i.parent,CASE WHEN p.workspace IS NULL THEN w.name ELSE COALESCE((SELECT COALESCE(NULLIF(display_name,''),username) FROM users WHERE id=p.owner),p.owner_name) END FROM `+table+` i JOIN drive_workspaces w ON w.id=i.workspace LEFT JOIN drive_personal_workspaces p ON p.workspace=i.workspace WHERE i.id=?`, item.ID).Scan(&stored, &item.Parent, &item.WorkspaceName)
	if err != nil {
		return item, err
	}
	item.Name, err = s.names.open(table, item.ID, stored)
	return item, err
}
