package drive

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Share is one grant on a folder or file to a person or a group.
type Share struct {
	ID            string `json:"id"`
	ResourceKind  string `json:"resource_kind"`
	ResourceID    string `json:"resource_id"`
	PrincipalKind string `json:"principal_kind"`
	PrincipalID   string `json:"principal_id"`
	Name          string `json:"name"`
	Role          string `json:"role"`
}

// SharedItem is a folder or file someone shared with the user outside their own workspaces.
type SharedItem struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	Workspace     string `json:"workspace"`
	WorkspaceName string `json:"workspace_name"`
	Parent        string `json:"parent"`
	Name          string `json:"name"`
	Role          string `json:"role"`
}

// Person is an active account a user can share with.
type Person struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
}

var roles = []string{"", "reader", "editor", "manager"}

// resource loads a folder or file: its workspace, the folder/file pair the access definition
// takes, and whether it is trashed. A missing item is ErrDenied, like an unreadable one.
func resource(ctx context.Context, q queryer, kind, id string) (workspace, folder, file string, trashed bool, err error) {
	switch kind {
	case "folder":
		folder = id
		err = q.QueryRowContext(ctx, `SELECT workspace,trashed FROM drive_folders WHERE id=?`, id).Scan(&workspace, &trashed)
	case "file":
		file = id
		err = q.QueryRowContext(ctx, `SELECT workspace,parent,trashed FROM drive_files WHERE id=?`, id).Scan(&workspace, &folder, &trashed)
	default:
		return "", "", "", false, ErrInvalid
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrDenied
	}
	return workspace, folder, file, trashed, err
}

// mayShare decides whether user may grant role on an item (and change or remove a share that
// grants role). A workspace manager (or a personal workspace's owner) may; a workspace editor
// may up to their rank on the item when the workspace's editors_share is on. Share holders who
// are not workspace members never re-share, whatever their share's role.
func mayShare(ctx context.Context, q queryer, user, workspace, folder, file, role string) error {
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service {
		return ErrDenied
	}
	// Sharing is decided by workspace rank: shares, even manager ones, never let their holder
	// pass the item on.
	var member int
	var editorsShare bool
	if err := q.QueryRowContext(ctx, `SELECT `+rankSQL("q.u", "w.id")+`,w.editors_share FROM (SELECT ? AS u) q, drive_workspaces w WHERE w.id=?`, user, workspace).Scan(&member, &editorsShare); err != nil {
		return err
	}
	if member >= 3 {
		return nil
	}
	item, err := itemRank(ctx, q, user, workspace, folder, file)
	if err != nil {
		return err
	}
	if editorsShare && member >= 2 && Rank(role) <= item {
		return nil
	}
	return ErrDenied
}

func principalName(ctx context.Context, q queryer, kind, id string) (string, error) {
	var name string
	var err error
	switch kind {
	case "user":
		err = q.QueryRowContext(ctx, `SELECT COALESCE(NULLIF(display_name,''),username) FROM users WHERE id=? AND status='active'`, id).Scan(&name)
	case "group":
		err = q.QueryRowContext(ctx, `SELECT display_name FROM groups WHERE id=?`, id).Scan(&name)
	default:
		return "", ErrInvalid
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvalid
	}
	return name, err
}

// Share grants role on a live folder or file to a person or a group, replacing any role that
// principal already had there.
func (s *Store) Share(ctx context.Context, user, kind, id, principalKind, principalID, role string) (Share, error) {
	if isDevice(ctx) {
		return Share{}, ErrDenied
	}
	sh := Share{ID: uuid.NewString(), ResourceKind: kind, ResourceID: id, PrincipalKind: principalKind, PrincipalID: principalID, Role: role}
	if Rank(role) == 0 {
		return sh, ErrInvalid
	}
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
		if sh.Name, err = principalName(ctx, tx, principalKind, principalID); err != nil {
			return err
		}
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT id,role FROM drive_shares WHERE resource_kind=? AND resource_id=? AND principal_kind=? AND principal_id=?`, kind, id, principalKind, principalID).Scan(&sh.ID, &existing)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			_, err = tx.ExecContext(ctx, `INSERT INTO drive_shares(id,workspace,resource_kind,resource_id,principal_kind,principal_id,role,created_by,created) VALUES(?,?,?,?,?,?,?,?,?)`, sh.ID, workspace, kind, id, principalKind, principalID, role, user, now())
		case err == nil:
			if err = mayShare(ctx, tx, user, workspace, folder, file, existing); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `UPDATE drive_shares SET role=?,created_by=? WHERE id=?`, role, user, sh.ID)
		}
		if err != nil {
			return err
		}
		return event(ctx, tx, user, "share.saved", id)
	})
	return sh, err
}

// Unshare removes a share the user may manage.
func (s *Store) Unshare(ctx context.Context, user, id string) error {
	if isDevice(ctx) {
		return ErrDenied
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var kind, rid, role string
		err := tx.QueryRowContext(ctx, `SELECT resource_kind,resource_id,role FROM drive_shares WHERE id=? AND principal_kind!='link'`, id).Scan(&kind, &rid, &role)
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
		if _, err = tx.ExecContext(ctx, `DELETE FROM drive_shares WHERE id=?`, id); err != nil {
			return err
		}
		return event(ctx, tx, user, "share.removed", rid)
	})
}

// Shares lists the people and groups an item is shared with, for those who may share it.
func (s *Store) Shares(ctx context.Context, user, kind, id string) ([]Share, error) {
	workspace, folder, file, _, err := resource(ctx, s.db, kind, id)
	if err != nil {
		return nil, err
	}
	if err = mayShare(ctx, s.db, user, workspace, folder, file, "reader"); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT s.id,s.principal_kind,s.principal_id,s.role,COALESCE(CASE s.principal_kind WHEN 'user' THEN (SELECT COALESCE(NULLIF(display_name,''),username) FROM users WHERE id=s.principal_id) ELSE (SELECT display_name FROM groups WHERE id=s.principal_id) END,'a removed account') FROM drive_shares s WHERE s.resource_kind=? AND s.resource_id=? AND s.principal_kind!='link'`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		sh := Share{ResourceKind: kind, ResourceID: id}
		if err = rows.Scan(&sh.ID, &sh.PrincipalKind, &sh.PrincipalID, &sh.Role, &sh.Name); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	slices.SortFunc(out, func(a, b Share) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out, rows.Err()
}

// SharedWithMe lists live items shared with the user or their groups in workspaces where the
// user has no access of their own, folders first, then by name. Each item appears once, at
// its highest shared role.
func (s *Store) SharedWithMe(ctx context.Context, user string) ([]SharedItem, error) {
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service {
		return nil, ErrDenied
	}
	// g: every grant reaching the user, by share or by an opened live organisation link.
	rows, err := s.db.QueryContext(ctx, `SELECT g.kind,g.rid,g.workspace,g.role,COALESCE(f.name,d.name),COALESCE(f.parent,d.parent),
 CASE WHEN p.workspace IS NULL THEN w.name ELSE COALESCE((SELECT COALESCE(NULLIF(display_name,''),username) FROM users WHERE id=p.owner),p.owner_name) END
 FROM (SELECT ? AS u) q
 JOIN (SELECT s.resource_kind AS kind,s.resource_id AS rid,s.workspace,s.role,s.principal_kind AS pk,s.principal_id AS pid,'' AS link FROM drive_shares s WHERE s.principal_kind!='link'
  UNION ALL
  SELECT l.resource_kind,l.resource_id,l.workspace,l.role,'link','',l.id FROM drive_links l WHERE l.scope='org' AND l.revoked=0 AND (l.expires=0 OR l.expires>CAST(strftime('%s','now') AS INTEGER))) g
  ON (g.pk='user' AND g.pid=q.u) OR (g.pk='group' AND EXISTS(SELECT 1 FROM group_members m WHERE m.group_id=g.pid AND m.user_id=q.u)) OR (g.pk='link' AND EXISTS(SELECT 1 FROM drive_link_users lu WHERE lu.link=g.link AND lu.user_id=q.u))
 JOIN drive_workspaces w ON w.id=g.workspace
 LEFT JOIN drive_personal_workspaces p ON p.workspace=g.workspace
 LEFT JOIN drive_files f ON g.kind='file' AND f.id=g.rid AND f.trashed=false
 LEFT JOIN drive_folders d ON g.kind='folder' AND d.id=g.rid AND d.trashed=false
 WHERE (f.id IS NOT NULL OR d.id IS NOT NULL)
 AND EXISTS(SELECT 1 FROM users WHERE id=q.u AND status='active')
 AND (p.workspace IS NULL OR EXISTS(SELECT 1 FROM users WHERE id=p.owner AND status='active'))
 AND `+rankSQL("q.u", "g.workspace")+`=0`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	best := map[string]SharedItem{}
	for rows.Next() {
		var i SharedItem
		var stored string
		if err = rows.Scan(&i.Kind, &i.ID, &i.Workspace, &i.Role, &stored, &i.Parent, &i.WorkspaceName); err != nil {
			return nil, err
		}
		table := map[string]string{"file": "drive_files", "folder": "drive_folders"}[i.Kind]
		if i.Name, err = s.names.open(table, i.ID, stored); err != nil {
			return nil, err
		}
		if prev, ok := best[i.ID]; !ok || Rank(i.Role) > Rank(prev.Role) {
			best[i.ID] = i
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SharedItem, 0, len(best))
	for _, i := range best {
		out = append(out, i)
	}
	slices.SortFunc(out, func(a, b SharedItem) int {
		return cmp.Or(strings.Compare(b.Kind, a.Kind), strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.ID, b.ID))
	})
	return out, nil
}

// People finds active accounts by name or username for an active signed-in user to share with:
// 2–100 characters, at most 20 results, never the caller, never an email address.
func (s *Store) People(ctx context.Context, user, q string) ([]Person, error) {
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service {
		return nil, ErrDenied
	}
	q = strings.ToLower(strings.TrimSpace(q))
	if n := utf8.RuneCountInString(q); n < 2 || n > 100 {
		return nil, ErrInvalid
	}
	like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	rows, err := s.db.QueryContext(ctx, `SELECT id,COALESCE(NULLIF(display_name,''),username),username FROM users WHERE status='active' AND id!=? AND (lower(username) LIKE ? ESCAPE '\' OR lower(display_name) LIKE ? ESCAPE '\') AND EXISTS(SELECT 1 FROM users WHERE id=? AND status='active') ORDER BY 2 LIMIT 20`, user, like, like, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		if err = rows.Scan(&p.ID, &p.Name, &p.Username); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetEditorsShare lets a shared workspace's managers allow its editors to share, up to their role.
func (s *Store) SetEditorsShare(ctx context.Context, user, workspace string, on bool) error {
	if isDevice(ctx) {
		return ErrDenied
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := sharedWorkspace(ctx, tx, workspace); err != nil {
			return err
		}
		if err := authorize(ctx, tx, user, workspace, 3); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE drive_workspaces SET editors_share=? WHERE id=?`, on, workspace); err != nil {
			return err
		}
		return event(ctx, tx, user, "workspace.sharing_changed", workspace)
	})
}

// FolderChildren lists a folder the user can read, whatever gives them access to it.
func (s *Store) FolderChildren(ctx context.Context, user, id string) (Folder, []Folder, []File, error) {
	f := Folder{ID: id}
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT workspace,parent,name FROM drive_folders WHERE id=? AND trashed=false`, id).Scan(&f.Workspace, &f.Parent, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return f, nil, nil, ErrDenied
	}
	if err != nil {
		return f, nil, nil, err
	}
	folders, files, err := s.Children(ctx, user, f.Workspace, id)
	if err != nil {
		return f, nil, nil, err
	}
	f.Name, err = s.names.open("drive_folders", id, stored)
	return f, folders, files, err
}
