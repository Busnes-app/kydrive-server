package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/suitedir"
	"github.com/google/uuid"
)

// suiteQuota is a new suite group workspace's quota, the admin form's default (1 GiB).
const suiteQuota = int64(1 << 30)

// SuiteReport counts what one ReconcileSuiteGroups pass changed.
type SuiteReport struct {
	Created, Renamed, Restored, Archived, MembersChanged int
}

// ReconcileSuiteGroups moves each suite group's workspace towards the state KyIdentity lists
// (KyIdentity docs/SUITE-DIRECTORY.md), keyed on the group ID only, as actor system, one
// transaction per group, so a crash halfway repeats safely and one failing group does not stop
// the rest. A group absent from groups is left alone. Nothing it made is ever deleted.
func (s *Store) ReconcileSuiteGroups(ctx context.Context, groups []suitedir.Group) (SuiteReport, error) {
	var total SuiteReport
	if _, ok := ctx.Value(serviceAccessKey{}).(ServiceAccess); ok || narrowed(ctx) {
		return total, ErrDenied
	}
	var errs []error
	for _, g := range groups {
		if err := ctx.Err(); err != nil {
			return total, errors.Join(append(errs, err)...)
		}
		var r SuiteReport
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			r = SuiteReport{}
			return s.reconcileSuiteGroup(ctx, tx, g, &r)
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("suite group %s: %w", g.ID, err))
			continue
		}
		total.Created += r.Created
		total.Renamed += r.Renamed
		total.Restored += r.Restored
		total.Archived += r.Archived
		total.MembersChanged += r.MembersChanged
	}
	return total, errors.Join(errs...)
}

func (s *Store) reconcileSuiteGroup(ctx context.Context, tx *sql.Tx, g suitedir.Group, r *SuiteReport) error {
	if g.State != suitedir.GroupActive && g.State != suitedir.GroupArchived {
		return nil
	}
	var workspace, grp string
	err := tx.QueryRowContext(ctx, `SELECT workspace,grp FROM drive_suite_groups WHERE kyid=?`, g.ID).Scan(&workspace, &grp)
	if errors.Is(err, sql.ErrNoRows) {
		if g.State != suitedir.GroupActive {
			return nil // never had a workspace here
		}
		name, err := freeSuiteName(ctx, tx, suiteName(g), "")
		if err != nil {
			return err
		}
		workspace, grp = uuid.NewString(), uuid.NewString()
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_workspaces(id,name,quota) VALUES(?,?,?)`, workspace, name, suiteQuota); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO groups(id,display_name,external_id,created_at,updated_at) VALUES(?,?,'',?,?)`, grp, "suite-group:"+g.ID, now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_suite_groups(kyid,workspace,grp) VALUES(?,?,?)`, g.ID, workspace, grp); err != nil {
			return err
		}
		if err := event(ctx, tx, "system", "suite_group.created", fmt.Sprintf("%s group=%s kyid=%s", workspace, grp, g.ID)); err != nil {
			return err
		}
		r.Created++
	} else if err != nil {
		return err
	}
	var current, deletedAt string
	if err := tx.QueryRowContext(ctx, `SELECT name,deleted_at FROM drive_workspaces WHERE id=?`, workspace).Scan(&current, &deletedAt); err != nil {
		return err
	}
	resource := fmt.Sprintf("%s group=%s kyid=%s", workspace, grp, g.ID)

	if g.State == suitedir.GroupArchived {
		_, removed, err := setSuiteMembers(ctx, tx, grp, nil)
		if err != nil {
			return err
		}
		if deletedAt == "" {
			counts, err := archiveWorkspace(ctx, tx, "system", workspace, time.Now())
			if err != nil {
				return err
			}
			r.Archived++
			return event(ctx, tx, "system", "suite_group.archived", fmt.Sprintf("%s members_removed=%d%s", resource, removed, counts))
		}
		if removed > 0 {
			r.MembersChanged++
			return event(ctx, tx, "system", "suite_group.members_changed", fmt.Sprintf("%s added=0 removed=%d", resource, removed))
		}
		return nil
	}

	if deletedAt != "" {
		if _, err := restoreWorkspace(ctx, tx, workspace); err != nil {
			return err
		}
		r.Restored++
		if err := event(ctx, tx, "system", "suite_group.restored", resource); err != nil {
			return err
		}
	}
	if want := suiteName(g); !suiteNameMatches(current, want) {
		switch name, err := freeSuiteName(ctx, tx, want, workspace); {
		case errors.Is(err, ErrConflict):
			log.Printf("suite group %s: workspace %s keeps its name, every numbered name is taken", g.ID, workspace)
		case err != nil:
			return err
		default:
			if _, err := tx.ExecContext(ctx, `UPDATE drive_workspaces SET name=? WHERE id=?`, name, workspace); err != nil {
				return err
			}
			r.Renamed++
			if err := event(ctx, tx, "system", "suite_group.renamed", resource); err != nil {
				return err
			}
		}
	}
	// The ordinary member role, never manager; an administrator's change is put back.
	res, err := tx.ExecContext(ctx, `INSERT INTO drive_grants(workspace,group_id,role) VALUES(?,?,'editor') ON CONFLICT(workspace,group_id) DO UPDATE SET role='editor' WHERE role!='editor'`, workspace, grp)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n > 0 {
		if err := event(ctx, tx, "system", "suite_group.grant_repaired", resource); err != nil {
			return err
		}
	}
	members, err := suiteMembers(ctx, tx, g.Members)
	if err != nil {
		return err
	}
	added, removed, err := setSuiteMembers(ctx, tx, grp, members)
	if err != nil {
		return err
	}
	if added+removed > 0 {
		r.MembersChanged++
		return event(ctx, tx, "system", "suite_group.members_changed", fmt.Sprintf("%s added=%d removed=%d", resource, added, removed))
	}
	return nil
}

// suiteName is the workspace name for g: its name, else its slug when the name is not a valid
// workspace name.
func suiteName(g suitedir.Group) string {
	if name := strings.TrimSpace(g.Name); ValidName(name) && len(name) <= 240 {
		return name
	}
	return g.Slug
}

// suiteNameMatches reports whether current is want or a numbered want, "want (n)", so a name
// that was numbered on a clash is not renamed again on every pass.
func suiteNameMatches(current, want string) bool {
	if current == want {
		return true
	}
	rest, ok := strings.CutPrefix(current, want+" (")
	if !ok {
		return false
	}
	n, ok := strings.CutSuffix(rest, ")")
	return ok && n != "" && strings.Trim(n, "0123456789") == ""
}

// freeSuiteName is want, else the first free "want (n)"; ErrConflict when none is free.
func freeSuiteName(ctx context.Context, tx *sql.Tx, want, except string) (string, error) {
	for n := 1; n <= 100; n++ {
		name := want
		if n > 1 {
			name = fmt.Sprintf("%s (%d)", want, n)
		}
		err := nameFree(ctx, tx, name, except)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, ErrConflict) {
			return "", err
		}
	}
	return "", fmt.Errorf("%w: no free workspace name", ErrConflict)
}

// suiteMembers maps subjects to local accounts as sign-in does (KySignOn first, then SCIM),
// keeping only active ordinary users: never an application administrator, never the local
// recovery administrator. Unknown subjects are skipped; a later pass adds them after sign-in.
func suiteMembers(ctx context.Context, tx *sql.Tx, subs []string) ([]string, error) {
	var out []string
	for _, sub := range subs {
		var id, status, role string
		err := tx.QueryRowContext(ctx, `SELECT id,status,role FROM users WHERE sso_subject=? AND sso_subject!='' AND sso_provider IN ('kysignon','scim') ORDER BY sso_provider='kysignon' DESC LIMIT 1`, sub).Scan(&id, &status, &role)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if status == "active" && role == "user" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, nil
}

// setSuiteMembers makes grp's members exactly want and counts the change.
func setSuiteMembers(ctx context.Context, tx *sql.Tx, grp string, want []string) (added, removed int, err error) {
	have, err := column(ctx, tx, `SELECT user_id FROM group_members WHERE group_id=?`, grp)
	if err != nil {
		return 0, 0, err
	}
	for _, id := range have {
		if !slices.Contains(want, id) {
			if _, err := tx.ExecContext(ctx, `DELETE FROM group_members WHERE group_id=? AND user_id=?`, grp, id); err != nil {
				return 0, 0, err
			}
			removed++
		}
	}
	for _, id := range want {
		if !slices.Contains(have, id) {
			if _, err := tx.ExecContext(ctx, `INSERT INTO group_members(group_id,user_id) VALUES(?,?)`, grp, id); err != nil {
				return 0, 0, err
			}
			added++
		}
	}
	return added, removed, nil
}
