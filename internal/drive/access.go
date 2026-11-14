package drive

import (
	"context"
	"errors"
	"strings"
)

// The only definition of drive access. A rank is 0 none, 1 reader, 2 editor, 3 manager.
// Every argument is a column reference (never "?"), since each appears more than once.

// rankSQL is the rank a user holds on a whole workspace. The active owner of a personal
// workspace is its manager and has no co-owners; a shared workspace grants the highest role
// of the user's active group memberships. A deleted workspace grants nobody anything.
// Administration grants nothing. Service tokens are narrowed in authorize, outside SQL.
func rankSQL(user, workspace string) string {
	return strings.NewReplacer("$U", user, "$W", workspace).Replace(`(CASE
 WHEN EXISTS(SELECT 1 FROM drive_workspaces dw WHERE dw.id=$W AND dw.deleted_at!='') THEN 0
 WHEN EXISTS(SELECT 1 FROM drive_personal_workspaces pw JOIN users po ON po.id=pw.owner WHERE pw.workspace=$W AND pw.owner=$U AND po.status='active') THEN 3
 WHEN EXISTS(SELECT 1 FROM drive_personal_workspaces pw WHERE pw.workspace=$W) THEN 0
 ELSE COALESCE((SELECT MAX(CASE g.role WHEN 'manager' THEN 3 WHEN 'editor' THEN 2 ELSE 1 END) FROM drive_grants g JOIN group_members gm ON gm.group_id=g.group_id JOIN users gu ON gu.id=gm.user_id WHERE g.workspace=$W AND gu.id=$U AND gu.status='active'),0)
END)`)
}

// itemRankSQL is the rank a user holds on one item: the workspace rank, raised by any grant on
// the file or on folder and its ancestors. A grant is a share to the active user or one of
// their groups, or an organisation link the user opened that is neither revoked nor expired.
// Grants only add. A personal workspace's grants lapse while its owner is not active, and every
// grant lapses while the workspace is deleted.
// folder is the item's own folder (for a folder) or its parent (for a file, "" at the root);
// file is the file's ID, or empty for a folder.
func itemRankSQL(user, workspace, folder, file string) string {
	grants := strings.NewReplacer("$U", user, "$W", workspace, "$D", folder, "$F", file).Replace(`COALESCE((SELECT MAX(CASE x.role WHEN 'manager' THEN 3 WHEN 'editor' THEN 2 ELSE 1 END) FROM (
  SELECT s.resource_kind AS kind,s.resource_id AS rid,s.role FROM drive_shares s WHERE s.workspace=$W
   AND ((s.principal_kind='user' AND s.principal_id=$U) OR (s.principal_kind='group' AND EXISTS(SELECT 1 FROM group_members sm WHERE sm.group_id=s.principal_id AND sm.user_id=$U)))
  UNION ALL
  SELECT l.resource_kind,l.resource_id,l.role FROM drive_links l JOIN drive_link_users lu ON lu.link=l.id WHERE l.workspace=$W AND lu.user_id=$U
   AND l.scope='org' AND l.revoked=0 AND (l.expires=0 OR l.expires>CAST(strftime('%s','now') AS INTEGER))
 ) x
 WHERE ((x.kind='file' AND x.rid=$F)
  OR (x.kind='folder' AND x.rid IN (WITH RECURSIVE up(id) AS (SELECT $D WHERE $D!='' UNION SELECT f.parent FROM drive_folders f JOIN up ON f.id=up.id WHERE f.parent!='') SELECT id FROM up)))
 AND EXISTS(SELECT 1 FROM users su WHERE su.id=$U AND su.status='active')
 AND NOT EXISTS(SELECT 1 FROM drive_workspaces sw WHERE sw.id=$W AND sw.deleted_at!='')
 AND NOT EXISTS(SELECT 1 FROM drive_personal_workspaces sp WHERE sp.workspace=$W AND NOT EXISTS(SELECT 1 FROM users so WHERE so.id=sp.owner AND so.status='active'))),0)`)
	return "MAX(" + rankSQL(user, workspace) + "," + grants + ")"
}

// authorizeItem is authorize for one item (folder: the item's folder or a file's parent; file:
// the file ID or ""). A service token keeps its workspace scope and rank and gets no shares.
func authorizeItem(ctx context.Context, q queryer, user, workspace, folder, file string, need int) error {
	if isPeer(ctx) {
		if need != 2 {
			return ErrDenied
		}
		return peerAllows(ctx, q, user, workspace, folder, file, peerAdd)
	}
	if _, ok := ctx.Value(serviceAccessKey{}).(ServiceAccess); ok {
		return authorize(ctx, q, user, workspace, need)
	}
	rank, err := itemRank(ctx, q, user, workspace, folder, file)
	if err != nil {
		return err
	}
	if rank < need {
		return ErrDenied
	}
	return nil
}

func itemRank(ctx context.Context, q queryer, user, workspace, folder, file string) (int, error) {
	var rank int
	err := q.QueryRowContext(ctx, `SELECT `+itemRankSQL("q.u", "q.w", "q.d", "q.f")+` FROM (SELECT ? AS u, ? AS w, ? AS d, ? AS f) q`, user, workspace, folder, file).Scan(&rank)
	return rank, err
}

// AuthorizeItem is authorizeItem for callers outside the package.
func (s *Store) AuthorizeItem(ctx context.Context, user, workspace, folder, file string, need int) error {
	return authorizeItem(ctx, s.db, user, workspace, folder, file, need)
}

// A peer context is a paired suite app (KyPost) acting for one user over a signed route or an
// upload ticket. It holds no rank: authorize, admin and every read, listing, share, move,
// trash and purge refuse it. It may do two things, each marked on ctx by the one code path
// that does it, so no other method that reaches the same check can borrow it:
//   - peerAdd (StartUpload, Upload and publishTX for a new file): add a new file directly in
//     the user's peer folder (drive_peer_folders, "Mail attachments") in their own live My files;
//   - peerLink (CreateLink): a reader organisation link on a live file directly in that folder.
//
// The folder itself is made by peerFolder, never through AddFolder.
type peerKey struct{}
type peerOpKey struct{}

const (
	peerAdd  = 1
	peerLink = 2
)

// Peer is the pairing a peer context acts under. SwitchDefault is the user's switch when
// they never chose (on for a KyIdentity pairing, off for an environment one); Live reports
// whether that pairing still stands. peerSwitch checks both in the transaction of the write
// it allows, so an unpair or a switch turned off while bytes stream publishes nothing.
type Peer struct {
	SwitchDefault bool
	Live          func() bool
}

// WithPeer marks ctx as KyPost acting, under p, for the user it names in each call.
func WithPeer(ctx context.Context, p Peer) context.Context {
	return context.WithValue(ctx, peerKey{}, p)
}

var (
	// ErrPeerOff is a peer write refused because the user's KyPost switch is off.
	ErrPeerOff = errors.New("kypost switch off")
	// ErrPeerGone is a peer write refused because the pairing it came under has ended.
	ErrPeerGone = errors.New("kypost no longer paired")
)

func peerSwitch(ctx context.Context, q queryer, user string) error {
	p, _ := ctx.Value(peerKey{}).(Peer)
	if p.Live == nil || !p.Live() {
		return ErrPeerGone
	}
	var on bool
	if err := q.QueryRowContext(ctx, `SELECT COALESCE((SELECT enabled FROM drive_peer_settings WHERE user_id=?),?)`, user, p.SwitchDefault).Scan(&on); err != nil {
		return err
	}
	if !on {
		return ErrPeerOff
	}
	return nil
}

func isPeer(ctx context.Context) bool { return ctx.Value(peerKey{}) != nil }

// narrowed is a device or peer context: neither creates or changes access, nor mints a
// session or credential.
func narrowed(ctx context.Context) bool { return isDevice(ctx) || isPeer(ctx) }

// peerOp marks ctx as the one code path that performs op.
func peerOp(ctx context.Context, op int) context.Context {
	return context.WithValue(ctx, peerOpKey{}, op)
}

// peerAllows is the whole of a peer's access. For peerAdd, file is empty or an ID no file
// has yet; for peerLink it is the file.
func peerAllows(ctx context.Context, q queryer, user, workspace, folder, file string, op int) error {
	if got, _ := ctx.Value(peerOpKey{}).(int); got != op {
		return ErrDenied
	}
	if err := peerSwitch(ctx, q, user); err != nil {
		return err
	}
	item := `NOT EXISTS(SELECT 1 FROM drive_files f WHERE f.id=q.f)`
	if op == peerLink {
		item = `EXISTS(SELECT 1 FROM drive_files f WHERE f.id=q.f AND f.workspace=q.w AND f.parent=q.d AND f.trashed=false)`
	}
	var ok int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT ? AS u, ? AS w, ? AS d, ? AS f) q
 JOIN drive_personal_workspaces pw ON pw.workspace=q.w AND pw.owner=q.u
 JOIN users u ON u.id=q.u AND u.status='active'
 JOIN drive_workspaces w ON w.id=q.w AND w.deleted_at=''
 JOIN drive_peer_folders pf ON pf.owner=q.u AND pf.folder=q.d
 JOIN drive_folders d ON d.id=q.d AND d.workspace=q.w AND d.trashed=false
 WHERE `+item, user, workspace, folder, file).Scan(&ok)
	if err != nil {
		return err
	}
	if ok != 1 {
		return ErrDenied
	}
	return nil
}
