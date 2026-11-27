package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/google/uuid"
)

func cellsSearchSchema(d dialect) string {
	return `ALTER TABLE drive_orgs ADD COLUMN home_region TEXT NOT NULL DEFAULT '';
 CREATE TABLE drive_cells(cell TEXT PRIMARY KEY);
 CREATE TABLE drive_search_index(org_id TEXT NOT NULL PRIMARY KEY REFERENCES drive_orgs(id),object TEXT NOT NULL DEFAULT '',version BIGINT NOT NULL DEFAULT 0,cursor BIGINT NOT NULL DEFAULT 0);
 CREATE TABLE drive_search_queue(org_id TEXT NOT NULL REFERENCES drive_orgs(id),file TEXT NOT NULL,first BIGINT NOT NULL,not_before BIGINT NOT NULL,generation BIGINT NOT NULL DEFAULT 1,PRIMARY KEY(org_id,file));` +
		d.trigger("drive_org_home_fixed", "UPDATE OF home_region", "drive_orgs", `OLD.home_region<>'' AND NEW.home_region<>OLD.home_region`, errOrg+" home_region is immutable") + searchOrgTriggers(d)
}
func cellServing(o string) string {
	return `NOT ` + multiTenantSQL + ` OR COALESCE((SELECT value FROM server_settings WHERE key='cells_configured'),'')<>'1' OR ` + o + `.seeded=1 OR EXISTS(SELECT 1 FROM drive_cells c WHERE c.cell=` + o + `.home_region)`
}
func (s *Store) recordCells(ctx context.Context) error {
	if !s.Multi() || len(s.tenancy.Cells) == 0 {
		return nil
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		current, err := column(ctx, tx, `SELECT cell FROM drive_cells ORDER BY cell`)
		if err != nil {
			return err
		}
		desired := slices.Clone(s.tenancy.Cells)
		slices.Sort(desired)
		if slices.Equal(current, desired) {
			return nil
		}
		// A newly served cell may have been skipped by the previous directory cursor.
		// Restart from a complete listing, and serve customer orgs only after it succeeds.
		if _, err := tx.ExecContext(ctx, `DELETE FROM server_settings WHERE key IN ('orgs_since','orgs_fresh_until')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM drive_cells`); err != nil {
			return err
		}
		for _, cell := range s.tenancy.Cells {
			if _, err := tx.ExecContext(ctx, `INSERT INTO drive_cells(cell) VALUES(?)`, cell); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO server_settings(key,value,updated_at) VALUES('cells_configured','1',?) ON CONFLICT(key) DO UPDATE SET value='1'`, now())
		return err
	})
}

// OrgHomeRegion is the immutable directory routing cell, including deleted tombstones.
func (s *Store) OrgHomeRegion(ctx context.Context, org string) (string, error) {
	var home string
	err := s.db.QueryRowContext(ctx, `SELECT home_region FROM drive_orgs WHERE id=?`, org).Scan(&home)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return home, err
}

var homeSlug = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// ObjectPrefix binds every hosted byte to its cell and organization.
func (s *Store) ObjectPrefix(ctx context.Context, org string) (string, error) {
	home, err := s.OrgHomeRegion(ctx, org)
	if err != nil {
		return "", err
	}
	if !canonicalOrg(org) || !homeSlug.MatchString(home) || (!slices.Contains(s.tenancy.Cells, home) && org != s.tenancy.StaffOrg) {
		return "", ErrDenied
	}
	if org != s.tenancy.StaffOrg {
		var served int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_cells WHERE cell=?`, home).Scan(&served); err != nil {
			return "", err
		}
		if served != 1 {
			return "", ErrDenied
		}
	}
	return fmt.Sprintf("cells/%s/orgs/%s/", home, org), nil
}
func canonicalOrg(org string) bool {
	id, err := uuid.Parse(org)
	return err == nil && id.String() == org
}

// StorageOrgs includes tombstones so failed cleanup remains retryable after key destruction.
func (s *Store) StorageOrgs(ctx context.Context, deleted bool) ([]string, error) {
	where := " WHERE home_region<>'' AND (" + cellServing("drive_orgs") + ")"
	if deleted {
		where += " AND status='deleted' AND seeded=0"
	}
	return column(ctx, s.db, `SELECT id FROM drive_orgs`+where+` ORDER BY id`)
}
func (s *Store) SetOrgPurger(p func(context.Context, string) error) { s.orgPurger = p }

func searchOrgTriggers(d dialect) string {
	var out string
	for _, table := range []string{"drive_search_index", "drive_search_queue"} {
		out += d.trigger(table+"_org_exists", "INSERT", table, `NOT EXISTS(SELECT 1 FROM drive_orgs WHERE id=NEW.org_id AND status<>'deleted')`, errOrg+" no such org")
		out += d.trigger(table+"_org_fixed", "UPDATE OF org_id", table, "NEW.org_id"+d.isNot()+"OLD.org_id", errOrg+" org_id is immutable")
	}
	return out
}
