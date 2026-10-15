package drive

import (
	"context"
	"strings"
)

// The only definition of drive access. A rank is 0 none, 1 reader, 2 editor, 3 manager.
// Every argument is a column reference (never "?"), since each appears more than once.

// rankSQL is the rank a user holds on a whole workspace. The active owner of a personal
// workspace is its manager and has no co-owners; a shared workspace grants the highest role
// of the user's active group memberships. Administration grants nothing. Service tokens are
// narrowed in authorize, outside SQL.
func rankSQL(user, workspace string) string {
	return strings.NewReplacer("$U", user, "$W", workspace).Replace(`(CASE
 WHEN EXISTS(SELECT 1 FROM drive_personal_workspaces pw JOIN users po ON po.id=pw.owner WHERE pw.workspace=$W AND pw.owner=$U AND po.status='active') THEN 3
 WHEN EXISTS(SELECT 1 FROM drive_personal_workspaces pw WHERE pw.workspace=$W) THEN 0
 ELSE COALESCE((SELECT MAX(CASE g.role WHEN 'manager' THEN 3 WHEN 'editor' THEN 2 ELSE 1 END) FROM drive_grants g JOIN group_members gm ON gm.group_id=g.group_id JOIN users gu ON gu.id=gm.user_id WHERE g.workspace=$W AND gu.id=$U AND gu.status='active'),0)
END)`)
}

// itemRankSQL is the rank a user holds on one item: the workspace rank, raised by any share on
// the file or on folder and its ancestors, to the active user directly or to one of their
// groups. Shares only add. A personal workspace's shares lapse while its owner is not active.
// folder is the item's own folder (for a folder) or its parent (for a file, "" at the root);
// file is the file's ID, or empty for a folder.
func itemRankSQL(user, workspace, folder, file string) string {
	share := strings.NewReplacer("$U", user, "$W", workspace, "$D", folder, "$F", file).Replace(`COALESCE((SELECT MAX(CASE s.role WHEN 'manager' THEN 3 WHEN 'editor' THEN 2 ELSE 1 END) FROM drive_shares s
 WHERE s.workspace=$W
 AND ((s.resource_kind='file' AND s.resource_id=$F)
  OR (s.resource_kind='folder' AND s.resource_id IN (WITH RECURSIVE up(id) AS (SELECT $D WHERE $D!='' UNION SELECT f.parent FROM drive_folders f JOIN up ON f.id=up.id WHERE f.parent!='') SELECT id FROM up)))
 AND ((s.principal_kind='user' AND s.principal_id=$U) OR (s.principal_kind='group' AND EXISTS(SELECT 1 FROM group_members sm WHERE sm.group_id=s.principal_id AND sm.user_id=$U)))
 AND EXISTS(SELECT 1 FROM users su WHERE su.id=$U AND su.status='active')
 AND NOT EXISTS(SELECT 1 FROM drive_personal_workspaces sp WHERE sp.workspace=$W AND NOT EXISTS(SELECT 1 FROM users so WHERE so.id=sp.owner AND so.status='active'))),0)`)
	return "MAX(" + rankSQL(user, workspace) + "," + share + ")"
}

// authorizeItem is authorize for one item (folder: the item's folder or a file's parent; file:
// the file ID or ""). A service token keeps its workspace scope and rank and gets no shares.
func authorizeItem(ctx context.Context, q queryer, user, workspace, folder, file string, need int) error {
	if _, ok := ctx.Value(serviceAccessKey{}).(ServiceAccess); ok {
		return authorize(ctx, q, user, workspace, need)
	}
	var rank int
	err := q.QueryRowContext(ctx, `SELECT `+itemRankSQL("q.u", "q.w", "q.d", "q.f")+` FROM (SELECT ? AS u, ? AS w, ? AS d, ? AS f) q`, user, workspace, folder, file).Scan(&rank)
	if err != nil {
		return err
	}
	if rank < need {
		return ErrDenied
	}
	return nil
}

// AuthorizeItem is authorizeItem for callers outside the package.
func (s *Store) AuthorizeItem(ctx context.Context, user, workspace, folder, file string, need int) error {
	return authorizeItem(ctx, s.db, user, workspace, folder, file, need)
}
