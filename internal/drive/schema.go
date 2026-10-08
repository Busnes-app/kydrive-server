package drive

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// schema upgrades drive tables in place; entry i is version i+1. Append only: never edit
// a shipped entry. Files trashed before version 1 count from the upgrade, not the epoch.
var schema = []string{
	`ALTER TABLE drive_files ADD COLUMN trashed_at TEXT NOT NULL DEFAULT '';
ALTER TABLE drive_files ADD COLUMN trash_batch TEXT NOT NULL DEFAULT '';
UPDATE drive_files SET trashed_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE trashed;
CREATE TABLE drive_folders_v2(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,trashed BOOLEAN NOT NULL DEFAULT false,trashed_at TEXT NOT NULL DEFAULT '',trash_batch TEXT NOT NULL DEFAULT '');
INSERT INTO drive_folders_v2(id,workspace,parent,name) SELECT id,workspace,parent,name FROM drive_folders;
DROP TABLE drive_folders;
ALTER TABLE drive_folders_v2 RENAME TO drive_folders;
CREATE UNIQUE INDEX drive_live_folders ON drive_folders(workspace,parent,name) WHERE trashed=false;
ALTER TABLE drive_workspaces ADD COLUMN trash_days INTEGER NOT NULL DEFAULT 0 CHECK(trash_days BETWEEN 0 AND 3650);
ALTER TABLE drive_workspaces ADD COLUMN keep_versions INTEGER NOT NULL DEFAULT 0 CHECK(keep_versions BETWEEN 0 AND 1000);`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS drive_schema(version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	for i, ddl := range schema {
		err := s.transaction(ctx, func(tx *sql.Tx) error {
			var done int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_schema WHERE version=?`, i+1).Scan(&done); err != nil || done == 1 {
				return err
			}
			if _, err := tx.ExecContext(ctx, ddl); err != nil {
				return fmt.Errorf("drive schema %d: %w", i+1, err)
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO drive_schema VALUES(?)`, i+1)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
