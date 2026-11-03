package drive

import (
	"context"
	"database/sql"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"
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
	// drive_change_floor is the highest pruned seq per workspace: a cursor below it missed rows.
	`CREATE TABLE drive_changes(seq INTEGER PRIMARY KEY AUTOINCREMENT,workspace TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('file','folder')),item TEXT NOT NULL,created INTEGER NOT NULL);
CREATE INDEX drive_changes_ws ON drive_changes(workspace,seq);
CREATE TABLE drive_change_floor(workspace TEXT PRIMARY KEY,seq INTEGER NOT NULL);`,
	`CREATE TABLE drive_devices(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,name TEXT NOT NULL,platform TEXT NOT NULL,hash TEXT NOT NULL UNIQUE,created TEXT NOT NULL,last_used INTEGER NOT NULL,revoked_at INTEGER NOT NULL DEFAULT 0,wipe INTEGER NOT NULL DEFAULT 0);
CREATE INDEX drive_devices_user ON drive_devices(user_id);
CREATE TABLE drive_device_codes(code_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL,name TEXT NOT NULL,platform TEXT NOT NULL,challenge TEXT NOT NULL,expires INTEGER NOT NULL,used INTEGER NOT NULL DEFAULT 0);`,
	// AI agent tokens share the app password table; existing rows stay WebDAV passwords. An agent
	// token may be limited to one workspace (root_folder '') or folder.
	`ALTER TABLE drive_app_passwords ADD COLUMN scope TEXT NOT NULL DEFAULT 'dav' CHECK(scope IN ('dav','mcp:read','mcp:write'));
ALTER TABLE drive_app_passwords ADD COLUMN root_workspace TEXT NOT NULL DEFAULT '';
ALTER TABLE drive_app_passwords ADD COLUMN root_folder TEXT NOT NULL DEFAULT '';`,
	// Blobs sealed as KYB1, which UpgradeBlobs rewrites as KYB2; every new blob is KYB2.
	`CREATE TABLE drive_blob_kyb1(blob TEXT PRIMARY KEY);
INSERT INTO drive_blob_kyb1(blob) SELECT DISTINCT blob FROM drive_versions WHERE sealed=1;`,
	// Editor sessions record the sign-in they were opened through (login_at in Unix
	// nanoseconds), so a back-channel logout reaches them after the browser session is gone.
	// Open editor sessions of directory accounts predate that binding, so no logout could
	// reach them: revoke them once, here.
	`ALTER TABLE drive_editor_sessions ADD COLUMN sso_sid TEXT NOT NULL DEFAULT '';
ALTER TABLE drive_editor_sessions ADD COLUMN login_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX drive_editor_sids ON drive_editor_sessions(sso_sid) WHERE revoked=false AND sso_sid!='';
UPDATE drive_files SET editor_epoch=editor_epoch+1 WHERE id IN (SELECT file_id FROM drive_editor_sessions WHERE revoked=false AND user_id IN (SELECT id FROM users WHERE sso_provider!='local'));
UPDATE drive_editor_sessions SET revoked=true WHERE revoked=false AND user_id IN (SELECT id FROM users WHERE sso_provider!='local');`,
	// A deleted shared workspace keeps its rows until purge; deleted_at (RFC 3339, '' = live)
	// takes away every access to it (access.go) and starts its recovery period.
	`ALTER TABLE drive_workspaces ADD COLUMN deleted_at TEXT NOT NULL DEFAULT '';
ALTER TABLE drive_workspaces ADD COLUMN deleted_by TEXT NOT NULL DEFAULT '';
CREATE INDEX drive_folders_workspace ON drive_folders(workspace);`,
	// An editor session opened with a service token names it, so revoking the token ends it.
	// Open sessions with no sign-in binding (login_at 0) came from service tokens, which no
	// revocation could reach: revoke them once, here, as schema 17 did for its sessions.
	`ALTER TABLE drive_editor_sessions ADD COLUMN service_token TEXT NOT NULL DEFAULT '';
UPDATE drive_files SET editor_epoch=editor_epoch+1 WHERE id IN (SELECT file_id FROM drive_editor_sessions WHERE revoked=false AND login_at=0);
UPDATE drive_editor_sessions SET revoked=true WHERE revoked=false AND login_at=0;`,
	// A live file and a live folder never share a name in one parent: WebDAV and the desktop
	// client could reach only one. The message makes uniqueViolation report ErrConflict.
	`CREATE TRIGGER drive_file_name_free_insert BEFORE INSERT ON drive_files WHEN NEW.trashed=false AND EXISTS(SELECT 1 FROM drive_folders d WHERE d.workspace=NEW.workspace AND d.parent=NEW.parent AND d.name_hmac=NEW.name_hmac AND d.trashed=false)
BEGIN SELECT RAISE(ABORT,'UNIQUE constraint failed: a folder has this name'); END;
CREATE TRIGGER drive_file_name_free_update BEFORE UPDATE ON drive_files WHEN NEW.trashed=false AND (OLD.trashed!=NEW.trashed OR OLD.workspace!=NEW.workspace OR OLD.parent!=NEW.parent OR OLD.name_hmac!=NEW.name_hmac) AND EXISTS(SELECT 1 FROM drive_folders d WHERE d.workspace=NEW.workspace AND d.parent=NEW.parent AND d.name_hmac=NEW.name_hmac AND d.trashed=false)
BEGIN SELECT RAISE(ABORT,'UNIQUE constraint failed: a folder has this name'); END;
CREATE TRIGGER drive_folder_name_free_insert BEFORE INSERT ON drive_folders WHEN NEW.trashed=false AND EXISTS(SELECT 1 FROM drive_files f WHERE f.workspace=NEW.workspace AND f.parent=NEW.parent AND f.name_hmac=NEW.name_hmac AND f.trashed=false)
BEGIN SELECT RAISE(ABORT,'UNIQUE constraint failed: a file has this name'); END;
CREATE TRIGGER drive_folder_name_free_update BEFORE UPDATE ON drive_folders WHEN NEW.trashed=false AND (OLD.trashed!=NEW.trashed OR OLD.workspace!=NEW.workspace OR OLD.parent!=NEW.parent OR OLD.name_hmac!=NEW.name_hmac) AND EXISTS(SELECT 1 FROM drive_files f WHERE f.workspace=NEW.workspace AND f.parent=NEW.parent AND f.name_hmac=NEW.name_hmac AND f.trashed=false)
BEGIN SELECT RAISE(ABORT,'UNIQUE constraint failed: a file has this name'); END;`,
}

// nameVersion is the schema version whose transaction encrypts existing names.
const nameVersion = 5

// clashVersion is the schema version that forbids a file and a folder sharing a name; its
// transaction renames the files of existing clashes (renameClashes).
const clashVersion = 20

// renameClashes renames each file that shares its parent and name with a folder in the same
// state (both live, or both trashed and so restored together) to the first free "<name> (n)",
// extension kept, so a handover or restore that moves both never trips the triggers. Folder
// places are loaded once: no index serves an all-states file-to-folder lookup.
func (s *Store) renameClashes(ctx context.Context, tx *sql.Tx) error {
	type place struct {
		workspace, parent, mac string
		trashed                bool
	}
	folders := map[place]bool{}
	rows, err := tx.QueryContext(ctx, `SELECT workspace,parent,name_hmac,trashed FROM drive_folders`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var p place
		if err := rows.Scan(&p.workspace, &p.parent, &p.mac, &p.trashed); err != nil {
			rows.Close()
			return err
		}
		folders[p] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	type clash struct{ id, workspace, parent, stored string }
	var clashes []clash
	rows, err = tx.QueryContext(ctx, `SELECT id,workspace,parent,name_hmac,trashed,name FROM drive_files`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var c clash
		var p place
		if err := rows.Scan(&c.id, &p.workspace, &p.parent, &p.mac, &p.trashed, &c.stored); err != nil {
			rows.Close()
			return err
		}
		if folders[p] {
			c.workspace, c.parent = p.workspace, p.parent
			clashes = append(clashes, c)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range clashes {
		name, err := s.names.open("drive_files", c.id, c.stored)
		if err != nil {
			return err
		}
		for n := 2; ; n++ {
			candidate := clashName(name, n)
			mac := s.names.hmac(candidate)
			var taken int
			if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM drive_files WHERE workspace=?1 AND parent=?2 AND name_hmac=?3 AND trashed=false)+(SELECT COUNT(*) FROM drive_folders WHERE workspace=?1 AND parent=?2 AND name_hmac=?3 AND trashed=false)`, c.workspace, c.parent, mac).Scan(&taken); err != nil {
				return err
			}
			if taken > 0 || folders[place{c.workspace, c.parent, mac, true}] {
				continue
			}
			stored, mac, err := s.names.seal("drive_files", c.id, candidate)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE drive_files SET name=?,name_hmac=? WHERE id=?`, stored, mac, c.id); err != nil {
				return err
			}
			if err := change(ctx, tx, c.workspace, "file", c.id); err != nil {
				return err
			}
			if err := event(ctx, tx, "system", "file.renamed", c.id+" name clashed with a folder"); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

// clashName is numbered(name, n) within ValidName's 255 bytes: whole runes are cut from the
// end of the base, so the extension, which picks the editor, survives.
func clashName(name string, n int) string {
	for {
		candidate := numbered(name, n)
		if len(candidate) <= 255 {
			return candidate
		}
		ext := path.Ext(name)
		if ext == name {
			ext = ""
		}
		base := strings.TrimSuffix(name, ext)
		_, size := utf8.DecodeLastRuneInString(base) // base is never empty: then ext would be ""
		name = base[:len(base)-size] + ext
	}
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
			if i+1 == nameVersion {
				if err := s.names.encryptNames(ctx, tx); err != nil {
					return fmt.Errorf("drive schema %d: %w", i+1, err)
				}
			}
			if i+1 == clashVersion {
				if err := s.renameClashes(ctx, tx); err != nil {
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
