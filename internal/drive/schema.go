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
	`ALTER TABLE drive_files ADD COLUMN editor_epoch INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drive_editor_sessions ADD COLUMN epoch INTEGER NOT NULL DEFAULT 0;`,
	`ALTER TABLE drive_versions ADD COLUMN sealed INTEGER NOT NULL DEFAULT 0;
CREATE INDEX drive_versions_blob ON drive_versions(blob);
CREATE TABLE drive_blob_retire(blob TEXT PRIMARY KEY);`,
	`ALTER TABLE drive_personal_workspaces ADD COLUMN owner_name TEXT NOT NULL DEFAULT '';
UPDATE drive_personal_workspaces SET owner_name=COALESCE((SELECT COALESCE(NULLIF(display_name,''),username) FROM users WHERE id=owner),'');`,
	// names.encryptNames finishes this step in the same transaction.
	`ALTER TABLE drive_files ADD COLUMN name_hmac TEXT NOT NULL DEFAULT '';
ALTER TABLE drive_folders ADD COLUMN name_hmac TEXT NOT NULL DEFAULT '';
DROP INDEX drive_live_names;
DROP INDEX drive_live_folders;`,
	`ALTER TABLE drive_editor_sessions ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE drive_editor_sessions ADD COLUMN next_attempt INTEGER NOT NULL DEFAULT 0;
CREATE INDEX drive_editor_pending ON drive_editor_sessions(next_attempt) WHERE revoked=true AND drop_done=false;
CREATE INDEX drive_editor_users ON drive_editor_sessions(user_id) WHERE revoked=false;
CREATE INDEX drive_files_workspace ON drive_files(workspace);`,
	`CREATE TABLE drive_upload_sessions(id TEXT PRIMARY KEY,owner TEXT NOT NULL,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,name_hmac TEXT NOT NULL,file TEXT NOT NULL,expected INTEGER NOT NULL,size INTEGER NOT NULL CHECK(size>=0),received INTEGER NOT NULL DEFAULT 0,expires INTEGER NOT NULL);
CREATE INDEX drive_upload_owner ON drive_upload_sessions(owner,expires);
CREATE TABLE drive_upload_segments(session TEXT NOT NULL REFERENCES drive_upload_sessions(id),start INTEGER NOT NULL,path TEXT NOT NULL,size INTEGER NOT NULL,PRIMARY KEY(session,start));`,
	`ALTER TABLE drive_upload_sessions ADD COLUMN completing INTEGER NOT NULL DEFAULT 0;`,
	`CREATE TABLE drive_app_passwords(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,name TEXT NOT NULL,hash TEXT NOT NULL UNIQUE,created TEXT NOT NULL,last_used INTEGER NOT NULL DEFAULT 0);
CREATE INDEX drive_app_passwords_user ON drive_app_passwords(user_id);`,
	// 'link' principals arrive with organisation links; the check allows them now because SQLite
	// cannot alter a CHECK later.
	`CREATE TABLE drive_shares(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),resource_kind TEXT NOT NULL CHECK(resource_kind IN ('folder','file')),resource_id TEXT NOT NULL,principal_kind TEXT NOT NULL CHECK(principal_kind IN ('group','user','link')),principal_id TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('reader','editor','manager')),created_by TEXT NOT NULL,created TEXT NOT NULL,UNIQUE(resource_kind,resource_id,principal_kind,principal_id));
CREATE INDEX drive_shares_resource ON drive_shares(resource_id);
CREATE INDEX drive_shares_principal ON drive_shares(principal_kind,principal_id);
ALTER TABLE drive_workspaces ADD COLUMN editors_share INTEGER NOT NULL DEFAULT 0;`,
	// One table for organisation and (later) guest links; guest columns are unused by 'org'.
	`CREATE TABLE drive_links(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),resource_kind TEXT NOT NULL CHECK(resource_kind IN ('folder','file')),resource_id TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('reader','editor')),scope TEXT NOT NULL CHECK(scope IN ('org','guest')),token_hash TEXT NOT NULL UNIQUE,expires INTEGER NOT NULL DEFAULT 0,password_hash TEXT NOT NULL DEFAULT '',upload_only INTEGER NOT NULL DEFAULT 0,created_by TEXT NOT NULL,created TEXT NOT NULL,revoked INTEGER NOT NULL DEFAULT 0);
CREATE INDEX drive_links_resource ON drive_links(resource_id);
CREATE TABLE drive_link_users(link TEXT NOT NULL REFERENCES drive_links(id),user_id TEXT NOT NULL,opened TEXT NOT NULL,PRIMARY KEY(link,user_id));`,
	// Client mtime; '' falls back to the version's creation time.
	`ALTER TABLE drive_versions ADD COLUMN mtime TEXT NOT NULL DEFAULT '';
ALTER TABLE drive_upload_sessions ADD COLUMN mtime TEXT NOT NULL DEFAULT '';`,
}

// nameVersion is the schema version whose transaction encrypts existing names.
const nameVersion = 5

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
			if i+1 == nameVersion {
				if err := s.names.encryptNames(ctx, tx); err != nil {
					return fmt.Errorf("drive schema %d: %w", i+1, err)
				}
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO drive_schema VALUES(?)`, i+1)
			return err
		})
		if err != nil {
			return err
		}
	}
	return scrub(ctx, s.db)
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
