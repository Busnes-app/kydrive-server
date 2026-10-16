package drive

import "strings"

// rankSQL is the only definition of drive access: an SQL expression giving the rank (0 none,
// 1 reader, 2 editor, 3 manager) the user expression holds on the workspace expression.
// Both arguments must be column references (never "?"), since each appears more than once.
//
// The active owner of a personal workspace is its manager and nobody else has access to it;
// a shared workspace grants the highest role of the user's active group memberships.
// Administration grants nothing. Service tokens are narrowed in authorize, outside SQL.
func rankSQL(user, workspace string) string {
	return strings.NewReplacer("$U", user, "$W", workspace).Replace(`(CASE
 WHEN EXISTS(SELECT 1 FROM drive_personal_workspaces pw JOIN users po ON po.id=pw.owner WHERE pw.workspace=$W AND pw.owner=$U AND po.status='active') THEN 3
 WHEN EXISTS(SELECT 1 FROM drive_personal_workspaces pw WHERE pw.workspace=$W) THEN 0
 ELSE COALESCE((SELECT MAX(CASE g.role WHEN 'manager' THEN 3 WHEN 'editor' THEN 2 ELSE 1 END) FROM drive_grants g JOIN group_members gm ON gm.group_id=g.group_id JOIN users gu ON gu.id=gm.user_id WHERE g.workspace=$W AND gu.id=$U AND gu.status='active'),0)
END)`)
}
