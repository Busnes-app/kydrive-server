package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Lifecycle purges (multi-tenant only, tenancy design "Lifecycle"): an org KyIdentity reports
// deleted, and a member's Delete everything. Both run on the elected worker, are idempotent
// and resume after a crash: each step deletes what is left, and the record that the purge is
// done commits with its last step. Blobs are returned for removal after commit.

// errOpenDrops skips a workspace whose editor sessions still wait for their drop.
var errOpenDrops = fmt.Errorf("%w: editor sessions not yet dropped", ErrConflict)

// purgeWorkspace deletes one workspace with everything in it and every access to it, and
// returns the blobs it left unreferenced.
func purgeWorkspace(ctx context.Context, tx *sql.Tx, workspace string) ([]string, string, error) {
	var open int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_editor_sessions WHERE drop_done=false AND file_id IN (SELECT id FROM drive_files WHERE workspace=?)`, workspace).Scan(&open); err != nil {
		return nil, "", err
	}
	if open > 0 {
		return nil, "", errOpenDrops
	}
	candidates, err := column(ctx, tx, `SELECT DISTINCT v.blob FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=?`, workspace)
	if err != nil {
		return nil, "", err
	}
	counts, err := run(ctx, tx, workspace, []step{
		{"", `DELETE FROM drive_editor_saves WHERE key IN (SELECT key FROM drive_editor_documents WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?))`},
		{"", `DELETE FROM drive_editor_documents WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?)`},
		{"", `DELETE FROM drive_editor_sessions WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?)`},
		{"versions", `DELETE FROM drive_versions WHERE file_id IN (SELECT id FROM drive_files WHERE workspace=?)`},
		{"files", `DELETE FROM drive_files WHERE workspace=?`},
		{"folders", `DELETE FROM drive_folders WHERE workspace=?`},
		{"", `DELETE FROM drive_suite_groups WHERE workspace=?`},
		{"", `DELETE FROM drive_personal_workspaces WHERE workspace=?`},
	})
	if err != nil {
		return nil, "", err
	}
	rest, err := removeWorkspace(ctx, tx, workspace)
	if err != nil {
		return nil, "", err
	}
	blobs, err := unreferenced(ctx, tx, candidates)
	return blobs, counts + rest, err
}

// revokeEditorsIn revokes the open editor sessions on the files files selects (one ?, arg),
// closing their documents, in a transaction of its own.
func (s *Store) revokeEditorsIn(ctx context.Context, files, arg string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`UPDATE drive_editor_sessions SET revoked=true WHERE revoked=false AND file_id IN (` + files + `)`,
			`UPDATE drive_editor_documents SET closed=true WHERE file_id IN (` + files + `)`,
		} {
			if _, err := tx.ExecContext(ctx, q, arg); err != nil {
				return err
			}
		}
		return nil
	})
}

// platformEvent writes a drive_events row with no org: an org being purged accepts none.
func platformEvent(ctx context.Context, tx *sql.Tx, action, resource string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO drive_events(id,user_id,action,resource,created,org_id) VALUES(?,?,?,?,?,NULL)`, uuid.NewString(), "system", action, resource, now())
	return err
}

// orgRows deletes, in order, every row an org still holds once its workspaces are gone. ? is
// the org. users last: sessions and MFA challenges cascade from it.
var orgRows = []step{
	{"view_copies", `DELETE FROM drive_view_copies WHERE org_id=?`},
	{"", `DELETE FROM drive_editor_saves WHERE org_id=?`},
	{"", `DELETE FROM drive_editor_documents WHERE org_id=?`},
	{"", `DELETE FROM drive_peer_tickets WHERE org_id=?`},
	{"", `DELETE FROM drive_peer_picks WHERE org_id=?`},
	{"", `DELETE FROM drive_peer_folders WHERE org_id=?`},
	{"", `DELETE FROM drive_peer_settings WHERE org_id=?`},
	{"", `DELETE FROM drive_peer_guest WHERE org_id=?`},
	{"app_passwords", `DELETE FROM drive_app_passwords WHERE org_id=?`},
	{"devices", `DELETE FROM drive_devices WHERE org_id=?`},
	{"", `DELETE FROM drive_device_codes WHERE org_id=?`},
	{"", `DELETE FROM drive_service_tokens WHERE org_id=?`},
	{"", `DELETE FROM drive_changes WHERE org_id=?`},
	{"", `DELETE FROM drive_change_floor WHERE org_id=?`},
	{"events", `DELETE FROM drive_events WHERE org_id=?`},
	{"audit", `DELETE FROM audit_records WHERE org_id=?`},
	{"", `DELETE FROM group_members WHERE group_id IN (SELECT id FROM groups WHERE org_id=?)`},
	{"groups", `DELETE FROM groups WHERE org_id=?`},
	{"users", `DELETE FROM users WHERE org_id=?`},
}

// PurgeOrgs purges every org recorded deleted and not yet purged: each workspace in its own
// transaction, then every other row and purged=1 in one. The tombstone row stays, so the ID is
// never reused. It returns the blobs and viewing-copy blobs left unreferenced.
func (s *Store) PurgeOrgs(ctx context.Context) (blobs, views []string, err error) {
	if !s.Multi() {
		return nil, nil, nil
	}
	orgs, err := column(ctx, s.db, `SELECT id FROM drive_orgs WHERE status='deleted' AND purged=0 AND seeded=0 ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	for _, org := range orgs {
		b, v, err := s.purgeOrg(ctx, org)
		blobs, views = append(blobs, b...), append(views, v...)
		if err != nil {
			return blobs, views, fmt.Errorf("org %s: %w", org, err)
		}
	}
	return blobs, views, nil
}

func (s *Store) purgeOrg(ctx context.Context, org string) (blobs, views []string, err error) {
	workspaces, err := column(ctx, s.db, `SELECT id FROM drive_workspaces WHERE org_id=? ORDER BY id`, org)
	if err != nil {
		return nil, nil, err
	}
	if err := s.revokeEditorsIn(ctx, `SELECT f.id FROM drive_files f JOIN drive_workspaces w ON w.id=f.workspace WHERE w.org_id=?`, org); err != nil {
		return nil, nil, err
	}
	deleted := `SELECT 1 FROM drive_orgs WHERE id=? AND status='deleted' AND purged=0 AND seeded=0`
	var waiting error
	for _, w := range workspaces {
		var b []string
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			b = nil
			var one int
			if err := tx.QueryRowContext(ctx, deleted+` AND EXISTS(SELECT 1 FROM drive_workspaces WHERE id=? AND org_id=?)`, org, w, org).Scan(&one); err != nil {
				return err // sql.ErrNoRows: purged or gone meanwhile
			}
			var err error
			b, _, err = purgeWorkspace(ctx, tx, w)
			return err
		})
		switch {
		case errors.Is(err, errOpenDrops):
			waiting = err // the rest still goes; purged waits for a later pass
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return blobs, nil, err
		}
		blobs = append(blobs, b...)
	}
	if waiting != nil {
		return blobs, nil, nil
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		views = nil
		var one int
		if err := tx.QueryRowContext(ctx, deleted, org).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		// No workspace can appear meanwhile: <table>_org_exists refuses rows in a deleted org.
		var err error
		if views, err = column(ctx, tx, `SELECT blob FROM drive_view_copies WHERE org_id=? AND blob<>''`, org); err != nil {
			return err
		}
		counts := ""
		for _, st := range orgRows {
			c, err := run(ctx, tx, org, []step{st})
			if err != nil {
				return err
			}
			counts += c
		}
		if _, err := tx.ExecContext(ctx, `UPDATE drive_orgs SET purged=1,grant_ceiling=NULL,grant_expires=0,report_inflight=0 WHERE id=?`, org); err != nil {
			return err
		}
		return platformEvent(ctx, tx, "platform.org_purged", fmt.Sprintf("org:%s workspaces=%d%s", org, len(workspaces), counts))
	})
	return blobs, views, err
}

// EraseMembers carries out every recorded Delete everything (drive_erasures) whose account is
// still here and whose org is active and enabled (D11), one member per transaction. Each
// topmost shared item of the member's My files moves, with its subtree, its person and group
// shares and its organisation links, into a "Handover from" folder in the My files of its
// successor: the other active member its share records name with the strongest role, then
// the earliest account. Guest links on moved items are revoked. Everything else of the
// member's goes: My files, credentials, devices, links they made, their shares, their
// memberships and their account. It returns the blobs and viewing-copy blobs left unreferenced.
func (s *Store) EraseMembers(ctx context.Context) (blobs, views []string, err error) {
	if !s.Multi() {
		return nil, nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.subject,u.id FROM drive_erasures e JOIN users u ON u.sso_provider='kysignon' AND u.sso_subject=e.subject AND u.org_id=e.org_id
JOIN drive_orgs o ON o.id=e.org_id WHERE o.status='active' AND o.enabled=1 ORDER BY e.revision`)
	if err != nil {
		return nil, nil, err
	}
	var todo [][2]string
	for rows.Next() {
		var p [2]string
		if err := rows.Scan(&p[0], &p[1]); err != nil {
			rows.Close()
			return nil, nil, err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	for _, p := range todo {
		// First, committed on its own: end every editor on their My files, so the drops the
		// purge waits for are on their way.
		if err := s.revokeEditorsIn(ctx, `SELECT f.id FROM drive_files f JOIN drive_personal_workspaces p ON p.workspace=f.workspace WHERE p.owner=?`, p[1]); err != nil {
			return blobs, views, err
		}
		var b, v []string
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			var err error
			b, v, err = s.erase(ctx, tx, p[0], p[1])
			return err
		})
		if errors.Is(err, errOpenDrops) {
			continue // My files still has editor sessions to drop; a later pass erases it
		}
		if err != nil {
			return blobs, views, fmt.Errorf("erase %s: %w", p[1], err)
		}
		blobs, views = append(blobs, b...), append(views, v...)
	}
	return blobs, views, nil
}

func (s *Store) erase(ctx context.Context, tx *sql.Tx, subject, user string) (blobs, views []string, err error) {
	var org, owner string
	err = tx.QueryRowContext(ctx, `SELECT u.org_id,`+ownerLabel+` FROM users u LEFT JOIN drive_personal_workspaces p ON p.owner=u.id
WHERE u.id=? AND u.sso_subject=? AND EXISTS(SELECT 1 FROM drive_erasures e WHERE e.subject=u.sso_subject AND e.org_id=u.org_id)
AND EXISTS(SELECT 1 FROM drive_orgs o WHERE o.id=u.org_id AND o.status='active' AND o.enabled=1)`, user, subject).Scan(&org, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil // erased or the org stopped meanwhile
	}
	if err != nil {
		return nil, nil, err
	}
	var mine string
	err = tx.QueryRowContext(ctx, `SELECT workspace FROM drive_personal_workspaces WHERE owner=?`, user).Scan(&mine)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, nil, err
	}
	moved, purged := 0, ""
	if mine != "" {
		if moved, err = s.handOn(ctx, tx, user, mine, org, owner); err != nil {
			return nil, nil, err
		}
		if blobs, purged, err = purgeWorkspace(ctx, tx, mine); err != nil {
			return nil, nil, err
		}
	}
	if views, err = column(ctx, tx, `SELECT blob FROM drive_view_copies WHERE user_id=? AND org_id=? AND blob<>''`, user, org); err != nil {
		return nil, nil, err
	}
	counts := ""
	for _, st := range []step{
		{"", `DELETE FROM drive_view_copies WHERE user_id=?1 AND org_id=?2`},
		{"", `DELETE FROM drive_link_users WHERE user_id=?1`},
		{"", `DELETE FROM drive_link_users WHERE link IN (SELECT id FROM drive_links WHERE created_by=?1)`},
		{"links", `DELETE FROM drive_links WHERE created_by=?1`},
		{"shares", `DELETE FROM drive_shares WHERE principal_kind='user' AND principal_id=?1`},
		{"", `DELETE FROM drive_app_passwords WHERE user_id=?1 AND org_id=?2`},
		{"", `DELETE FROM drive_devices WHERE user_id=?1 AND org_id=?2`},
		{"", `DELETE FROM drive_device_codes WHERE user_id=?1 AND org_id=?2`},
		{"", `DELETE FROM drive_service_tokens WHERE user_id=?1 AND org_id=?2`},
		{"", `DELETE FROM drive_peer_tickets WHERE owner=?1 AND org_id=?2`},
		{"", `DELETE FROM drive_peer_picks WHERE owner=?1 AND org_id=?2`},
		{"", `DELETE FROM drive_peer_folders WHERE owner=?1 AND org_id=?2`},
		{"", `DELETE FROM drive_peer_settings WHERE user_id=?1 AND org_id=?2`},
		{"", `DELETE FROM drive_peer_guest WHERE user_id=?1 AND org_id=?2`},
		{"", `DELETE FROM group_members WHERE user_id=?1`},
		{"", `DELETE FROM users WHERE id=?1 AND org_id=?2`},
	} {
		args := []any{user}
		if strings.Contains(st.query, "?2") {
			args = append(args, org)
		}
		res, err := tx.ExecContext(ctx, st.query, args...)
		if err != nil {
			return nil, nil, err
		}
		if st.label != "" {
			n, err := res.RowsAffected()
			if err != nil {
				return nil, nil, err
			}
			counts += fmt.Sprintf(" %s=%d", st.label, n)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drive_orgs SET report_due=1 WHERE id=?`, org); err != nil {
		return nil, nil, err
	}
	// Their editor sessions elsewhere stay, revoked, until the revocation worker drops them.
	if err := s.RevokeUserEditors(ctx, tx, user); err != nil {
		return nil, nil, err
	}
	return blobs, views, event(WithOrg(ctx, org), tx, "system", "member.erased", fmt.Sprintf("%s handed_on=%d%s%s", user, moved, purged, counts))
}

// handOn moves each topmost shared item of workspace (user's My files) to its successor and
// returns how many moved.
func (s *Store) handOn(ctx context.Context, tx *sql.Tx, user, workspace, org, owner string) (int, error) {
	parents := map[string]string{} // item ID to parent folder ID ("" at the root)
	kinds := map[string]string{}
	for _, kind := range []string{"folder", "file"} {
		rows, err := tx.QueryContext(ctx, `SELECT id,parent FROM drive_`+kind+`s WHERE workspace=?`, workspace)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var id, parent string
			if err := rows.Scan(&id, &parent); err != nil {
				rows.Close()
				return 0, err
			}
			parents[id], kinds[id] = parent, kind
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
	}
	// Share records, read whether or not they work now. A deactivated owner's shares still
	// count; a revoked organisation link and guest links do not.
	rows, err := tx.QueryContext(ctx, `SELECT x.resource_id,u.id FROM (
SELECT s.resource_id,s.principal_id AS uid,s.role FROM drive_shares s WHERE s.workspace=?1 AND s.principal_kind='user'
UNION ALL SELECT s.resource_id,m.user_id,s.role FROM drive_shares s JOIN group_members m ON m.group_id=s.principal_id WHERE s.workspace=?1 AND s.principal_kind='group'
UNION ALL SELECT l.resource_id,lu.user_id,l.role FROM drive_links l JOIN drive_link_users lu ON lu.link=l.id WHERE l.workspace=?1 AND l.scope='org' AND l.revoked=0) x
JOIN users u ON u.id=x.uid WHERE u.id<>?2 AND u.org_id=?3 AND u.status='active'
ORDER BY x.resource_id,CASE x.role WHEN 'manager' THEN 3 WHEN 'editor' THEN 2 ELSE 1 END DESC,u.created_at,u.id`, workspace, user, org)
	if err != nil {
		return 0, err
	}
	best := map[string]string{} // item to successor: the first row per item
	for rows.Next() {
		var item, heir string
		if err := rows.Scan(&item, &heir); err != nil {
			rows.Close()
			return 0, err
		}
		if _, ok := best[item]; !ok {
			best[item] = heir
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	shared := map[string]bool{}
	// Any share record marks its item shared, even one naming nobody who could keep it.
	marked, err := column(ctx, tx, `SELECT resource_id FROM drive_shares WHERE workspace=?1 AND principal_kind IN ('user','group') UNION SELECT resource_id FROM drive_links WHERE workspace=?1 AND scope='org' AND revoked=0`, workspace)
	if err != nil {
		return 0, err
	}
	for _, id := range marked {
		shared[id] = true
	}
	topmost := func(id string) bool {
		for p := parents[id]; p != ""; p = parents[p] {
			if shared[p] {
				return false
			}
		}
		return true
	}
	byHeir := map[string][]string{}
	for id := range shared {
		if _, here := parents[id]; !here || !topmost(id) {
			continue
		}
		if heir, ok := best[id]; ok {
			byHeir[heir] = append(byHeir[heir], id)
		}
	}
	heirs := make([]string, 0, len(byHeir))
	for h := range byHeir {
		heirs = append(heirs, h)
	}
	slices.Sort(heirs)
	moved := 0
	for _, heir := range heirs {
		target, err := ensurePersonal(ctx, tx, heir)
		if err != nil {
			return 0, err
		}
		folder := uuid.NewString()
		if err := s.handoverFolder(ctx, tx, folder, target, owner); err != nil {
			return 0, err
		}
		items := byHeir[heir]
		slices.Sort(items)
		for _, root := range items {
			sub := []string{root}
			for i := 0; i < len(sub); i++ {
				if kinds[sub[i]] != "folder" {
					continue
				}
				for id, p := range parents {
					if p == sub[i] {
						sub = append(sub, id)
					}
				}
			}
			if err := s.moveSubtree(ctx, tx, workspace, target, folder, root, kinds[root], sub, heir); err != nil {
				return 0, err
			}
			moved++
		}
		if err := change(ctx, tx, target, "folder", folder); err != nil {
			return 0, err
		}
	}
	return moved, nil
}

// moveSubtree moves root (of kind) and its subtree sub from workspace into folder of target,
// with the subtree's shares and organisation links, now owned by heir; guest links are revoked
// and editor sessions closed, as on any move.
func (s *Store) moveSubtree(ctx context.Context, tx *sql.Tx, workspace, target, folder, root, kind string, sub []string, heir string) error {
	in := strings.TrimSuffix(strings.Repeat("?,", len(sub)), ",")
	args := func(first ...any) []any {
		for _, id := range sub {
			first = append(first, id)
		}
		return first
	}
	for _, q := range []struct {
		sql  string
		lead []any
	}{
		{`UPDATE drive_links SET revoked=1 WHERE scope='guest' AND workspace=? AND resource_id IN (` + in + `)`, []any{workspace}},
		{`UPDATE drive_links SET workspace=?,created_by=? WHERE workspace=? AND resource_id IN (` + in + `)`, []any{target, heir, workspace}},
		{`UPDATE drive_shares SET workspace=?,created_by=? WHERE workspace=? AND resource_id IN (` + in + `)`, []any{target, heir, workspace}},
		{`UPDATE drive_editor_sessions SET revoked=true WHERE revoked=false AND file_id IN (` + in + `)`, nil},
		{`UPDATE drive_editor_documents SET closed=true WHERE file_id IN (` + in + `)`, nil},
		{`UPDATE drive_files SET editor_epoch=editor_epoch+1 WHERE id IN (` + in + `)`, nil},
		{`UPDATE drive_folders SET workspace=?,parent=CASE WHEN id=? THEN ? ELSE parent END WHERE workspace=? AND id IN (` + in + `)`, []any{target, root, folder, workspace}},
		{`UPDATE drive_files SET workspace=?,parent=CASE WHEN id=? THEN ? ELSE parent END WHERE workspace=? AND id IN (` + in + `)`, []any{target, root, folder, workspace}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, args(q.lead...)...); err != nil {
			return err
		}
	}
	return change(ctx, tx, workspace, kind, root)
}
