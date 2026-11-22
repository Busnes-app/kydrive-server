package drive

import (
	"context"
	"database/sql"
	"fmt"
)

// postgresVersion is the schema version postgresSchema creates. A PostgreSQL drive is always
// new (SQLite moves over by migrate-postgres), so it starts at the latest version; an entry
// appended to schema needs its PostgreSQL form here too, and TestPostgresSchemaIsCurrent fails
// until it has one.
const postgresVersion = 28

// postgresTables is every drive table at postgresVersion without org_id, which
// tenancySchema adds as on SQLite. SQLite's INTEGER is 64-bit, so it is BIGINT here.
const postgresTables = `
CREATE TABLE drive_workspaces(id TEXT PRIMARY KEY,name TEXT NOT NULL,quota BIGINT NOT NULL CHECK(quota>0),trash_days BIGINT NOT NULL DEFAULT 0 CHECK(trash_days BETWEEN 0 AND 3650),keep_versions BIGINT NOT NULL DEFAULT 0 CHECK(keep_versions BETWEEN 0 AND 1000),editors_share BIGINT NOT NULL DEFAULT 0,deleted_at TEXT NOT NULL DEFAULT '',deleted_by TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_service_tokens(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,workspace TEXT NOT NULL,name TEXT NOT NULL,role TEXT NOT NULL,token_hash TEXT NOT NULL UNIQUE,expires BIGINT NOT NULL,revoked BOOLEAN NOT NULL DEFAULT false);
CREATE TABLE drive_personal_workspaces(workspace TEXT PRIMARY KEY REFERENCES drive_workspaces(id),owner TEXT NOT NULL UNIQUE,owner_name TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_grants(workspace TEXT NOT NULL REFERENCES drive_workspaces(id),group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,role TEXT NOT NULL CHECK(role IN ('reader','editor','manager')),PRIMARY KEY(workspace,group_id));
CREATE TABLE drive_folders(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,trashed BOOLEAN NOT NULL DEFAULT false,trashed_at TEXT NOT NULL DEFAULT '',trash_batch TEXT NOT NULL DEFAULT '',name_hmac TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_files(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,revision BIGINT NOT NULL,trashed BOOLEAN NOT NULL DEFAULT false,trashed_at TEXT NOT NULL DEFAULT '',trash_batch TEXT NOT NULL DEFAULT '',editor_epoch BIGINT NOT NULL DEFAULT 0,name_hmac TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_versions(file_id TEXT NOT NULL REFERENCES drive_files(id),revision BIGINT NOT NULL,blob TEXT NOT NULL,digest TEXT NOT NULL,size BIGINT NOT NULL CHECK(size>=0),created TEXT NOT NULL,sealed BIGINT NOT NULL DEFAULT 0,mtime TEXT NOT NULL DEFAULT '',PRIMARY KEY(file_id,revision));
CREATE TABLE drive_events(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,action TEXT NOT NULL,resource TEXT NOT NULL,created TEXT NOT NULL);
CREATE TABLE drive_editor_sessions(id TEXT PRIMARY KEY,file_id TEXT NOT NULL REFERENCES drive_files(id),revision BIGINT NOT NULL,user_id TEXT NOT NULL,token_hash TEXT NOT NULL,expires BIGINT NOT NULL,revoked BOOLEAN NOT NULL DEFAULT false,drop_done BOOLEAN NOT NULL DEFAULT false,editable BOOLEAN NOT NULL DEFAULT true,epoch BIGINT NOT NULL DEFAULT 0,attempts BIGINT NOT NULL DEFAULT 0,next_attempt BIGINT NOT NULL DEFAULT 0,sso_sid TEXT NOT NULL DEFAULT '',login_at BIGINT NOT NULL DEFAULT 0,service_token TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_editor_documents(key TEXT PRIMARY KEY,file_id TEXT NOT NULL,base_revision BIGINT NOT NULL,current_revision BIGINT NOT NULL,closed BOOLEAN NOT NULL DEFAULT false);
CREATE TABLE drive_editor_saves(key TEXT NOT NULL,digest TEXT NOT NULL,revision BIGINT NOT NULL,PRIMARY KEY(key,digest));
CREATE TABLE drive_blob_retire(blob TEXT PRIMARY KEY);
CREATE TABLE drive_upload_sessions(id TEXT PRIMARY KEY,owner TEXT NOT NULL,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,name_hmac TEXT NOT NULL,file TEXT NOT NULL,expected BIGINT NOT NULL,size BIGINT NOT NULL CHECK(size>=0),received BIGINT NOT NULL DEFAULT 0,expires BIGINT NOT NULL,completing BIGINT NOT NULL DEFAULT 0,mtime TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_upload_segments(session TEXT NOT NULL REFERENCES drive_upload_sessions(id),start BIGINT NOT NULL,path TEXT NOT NULL,size BIGINT NOT NULL,PRIMARY KEY(session,start));
CREATE TABLE drive_app_passwords(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,name TEXT NOT NULL,hash TEXT NOT NULL UNIQUE,created TEXT NOT NULL,last_used BIGINT NOT NULL DEFAULT 0,scope TEXT NOT NULL DEFAULT 'dav' CHECK(scope IN ('dav','mcp:read','mcp:write')),root_workspace TEXT NOT NULL DEFAULT '',root_folder TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_shares(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),resource_kind TEXT NOT NULL CHECK(resource_kind IN ('folder','file')),resource_id TEXT NOT NULL,principal_kind TEXT NOT NULL CHECK(principal_kind IN ('group','user','link')),principal_id TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('reader','editor','manager')),created_by TEXT NOT NULL,created TEXT NOT NULL,UNIQUE(resource_kind,resource_id,principal_kind,principal_id));
CREATE TABLE drive_links(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),resource_kind TEXT NOT NULL CHECK(resource_kind IN ('folder','file')),resource_id TEXT NOT NULL,role TEXT NOT NULL CHECK(role IN ('reader','editor')),scope TEXT NOT NULL CHECK(scope IN ('org','guest')),token_hash TEXT NOT NULL UNIQUE,expires BIGINT NOT NULL DEFAULT 0,password_hash TEXT NOT NULL DEFAULT '',upload_only BIGINT NOT NULL DEFAULT 0,created_by TEXT NOT NULL,created TEXT NOT NULL,revoked BIGINT NOT NULL DEFAULT 0);
CREATE TABLE drive_link_users(link TEXT NOT NULL REFERENCES drive_links(id),user_id TEXT NOT NULL,opened TEXT NOT NULL,PRIMARY KEY(link,user_id));
CREATE TABLE drive_changes(seq BIGINT GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,workspace TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('file','folder')),item TEXT NOT NULL,created BIGINT NOT NULL);
CREATE TABLE drive_change_floor(workspace TEXT PRIMARY KEY,seq BIGINT NOT NULL);
CREATE TABLE drive_devices(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,name TEXT NOT NULL,platform TEXT NOT NULL,hash TEXT NOT NULL UNIQUE,created TEXT NOT NULL,last_used BIGINT NOT NULL,revoked_at BIGINT NOT NULL DEFAULT 0,wipe BIGINT NOT NULL DEFAULT 0,pubkey TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_device_codes(code_hash TEXT PRIMARY KEY,user_id TEXT NOT NULL,name TEXT NOT NULL,platform TEXT NOT NULL,challenge TEXT NOT NULL,expires BIGINT NOT NULL,used BIGINT NOT NULL DEFAULT 0);
CREATE TABLE drive_blob_kyb1(blob TEXT PRIMARY KEY);
CREATE TABLE drive_peer_settings(user_id TEXT PRIMARY KEY,enabled BIGINT NOT NULL);
CREATE TABLE drive_peer_folders(owner TEXT PRIMARY KEY,folder TEXT NOT NULL);
CREATE TABLE drive_peer_tickets(id TEXT PRIMARY KEY,token_hash TEXT NOT NULL UNIQUE,owner TEXT NOT NULL,name TEXT NOT NULL,size BIGINT NOT NULL,expires BIGINT NOT NULL,pairing TEXT NOT NULL,session TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_peer_picks(owner TEXT NOT NULL,file TEXT NOT NULL,expires BIGINT NOT NULL,PRIMARY KEY(owner,file));
CREATE TABLE drive_peer_guest(user_id TEXT PRIMARY KEY);
CREATE TABLE drive_view_copies(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,workspace TEXT NOT NULL,name TEXT NOT NULL,size BIGINT NOT NULL,pairing TEXT NOT NULL,token_hash TEXT UNIQUE,ticket_expires BIGINT NOT NULL,expires BIGINT NOT NULL,blob TEXT NOT NULL DEFAULT '',sso_sid TEXT NOT NULL DEFAULT '',login_at BIGINT NOT NULL DEFAULT 0,revoked BIGINT NOT NULL DEFAULT 0,dropped BIGINT NOT NULL DEFAULT 0,next_attempt BIGINT NOT NULL DEFAULT 0,service_token TEXT NOT NULL DEFAULT '');
CREATE TABLE drive_suite_groups(kyid TEXT PRIMARY KEY,workspace TEXT NOT NULL UNIQUE REFERENCES drive_workspaces(id),grp TEXT NOT NULL UNIQUE REFERENCES groups(id));
CREATE UNIQUE INDEX drive_scim_subject ON users(sso_subject) WHERE sso_provider='scim' AND sso_subject!='';
CREATE UNIQUE INDEX drive_group_external ON groups(external_id) WHERE external_id!='';
CREATE UNIQUE INDEX drive_live_names ON drive_files(workspace,parent,name_hmac) WHERE trashed=false;
CREATE UNIQUE INDEX drive_live_folders ON drive_folders(workspace,parent,name_hmac) WHERE trashed=false;
CREATE INDEX drive_editor_expiry ON drive_editor_sessions(expires);
CREATE INDEX drive_editor_pending ON drive_editor_sessions(next_attempt) WHERE revoked=true AND drop_done=false;
CREATE INDEX drive_editor_users ON drive_editor_sessions(user_id) WHERE revoked=false;
CREATE INDEX drive_editor_sids ON drive_editor_sessions(sso_sid) WHERE revoked=false AND sso_sid!='';
CREATE INDEX drive_files_workspace ON drive_files(workspace);
CREATE INDEX drive_folders_workspace ON drive_folders(workspace);
CREATE INDEX drive_versions_blob ON drive_versions(blob);
CREATE INDEX drive_upload_owner ON drive_upload_sessions(owner,expires);
CREATE INDEX drive_app_passwords_user ON drive_app_passwords(user_id);
CREATE INDEX drive_shares_resource ON drive_shares(resource_id);
CREATE INDEX drive_shares_principal ON drive_shares(principal_kind,principal_id);
CREATE INDEX drive_links_resource ON drive_links(resource_id);
CREATE INDEX drive_changes_ws ON drive_changes(workspace,seq);
CREATE INDEX drive_devices_user ON drive_devices(user_id);
CREATE INDEX drive_peer_tickets_owner ON drive_peer_tickets(owner,expires);
CREATE INDEX drive_view_copies_workspace ON drive_view_copies(workspace);
CREATE INDEX drive_view_copies_user ON drive_view_copies(user_id);
-- SQLite's scalar functions as the shared queries use them: julianday of an RFC 3339 text
-- ('' is NULL, as SQLite reads it), unixepoch() for now, two-argument max and min (NULL if
-- either is), and printf's zero-padded '%020d' (any other format is NULL).
CREATE FUNCTION max(a BIGINT, b BIGINT) RETURNS BIGINT LANGUAGE sql IMMUTABLE STRICT AS $$SELECT GREATEST(a,b)$$;
CREATE FUNCTION min(a BIGINT, b BIGINT) RETURNS BIGINT LANGUAGE sql IMMUTABLE STRICT AS $$SELECT LEAST(a,b)$$;
CREATE FUNCTION printf(f TEXT, n BIGINT) RETURNS TEXT LANGUAGE sql IMMUTABLE STRICT AS $$SELECT CASE WHEN f='%020d' AND n>=0 THEN lpad(n::text,20,'0') END$$;
-- A change row takes its seq under its workspace's lock, held to commit, so one workspace's
-- rows commit in seq order and a cursor never passes a row still to commit. SQLite's one
-- writer gives that order.
CREATE FUNCTION drive_changes_in_order() RETURNS trigger LANGUAGE plpgsql AS $t$BEGIN PERFORM pg_advisory_xact_lock(hashtextextended('kydrive changes '||NEW.workspace,0)); NEW.seq := nextval(pg_get_serial_sequence('drive_changes','seq')); RETURN NEW; END$t$;
CREATE TRIGGER drive_changes_in_order BEFORE INSERT ON drive_changes FOR EACH ROW EXECUTE FUNCTION drive_changes_in_order();
CREATE FUNCTION julianday(t TEXT) RETURNS DOUBLE PRECISION LANGUAGE sql STABLE AS $$SELECT CASE WHEN t='' THEN NULL ELSE extract(epoch FROM t::timestamptz)/86400+2440587.5 END$$;
CREATE FUNCTION unixepoch() RETURNS BIGINT LANGUAGE sql STABLE AS $$SELECT extract(epoch FROM now())::bigint$$;
`

// createPostgres creates the drive schema on an empty PostgreSQL database and records versions
// 1 to postgresVersion. Concurrent first starts serialise on the transaction-scoped lock.
func (s *Store) createPostgres(ctx context.Context) error {
	if len(schema) != postgresVersion {
		return fmt.Errorf("drive: PostgreSQL schema is at %d, SQLite at %d", postgresVersion, len(schema))
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS drive_schema(version BIGINT PRIMARY KEY)`); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('kydrive drive schema'))`); err != nil {
			return err
		}
		var done int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_schema`).Scan(&done); err != nil || done == postgresVersion {
			return err
		}
		if done != 0 {
			return fmt.Errorf("drive: PostgreSQL schema half recorded (%d of %d)", done, postgresVersion)
		}
		if _, err := tx.ExecContext(ctx, postgresSchema()); err != nil {
			return fmt.Errorf("drive: PostgreSQL schema: %w", err)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO drive_schema SELECT generate_series(1,?)`, postgresVersion)
		return err
	})
}

// postgresSchema is the drive schema at postgresVersion.
func postgresSchema() string {
	pg := dialect{pg: true}
	return postgresTables + nameFreeSchema(pg) + tenancySchema(pg, orgOwned, orgNullable) + staffAdminSchema(pg)
}

// dialect spells what the two backends write differently: triggers and IS NOT between values.
type dialect struct{ pg bool }

var sqlite = dialect{}

// isNot is SQLite's IS NOT between two values, which PostgreSQL spells IS DISTINCT FROM.
func (d dialect) isNot() string {
	if d.pg {
		return " IS DISTINCT FROM "
	}
	return " IS NOT "
}

// trigger refuses a row of table, before event, when cond holds, with msg as the error.
func (d dialect) trigger(name, event, table, cond, msg string) string {
	if !d.pg {
		return fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s WHEN %s\nBEGIN SELECT RAISE(ABORT,'%s'); END;\n", name, event, table, cond, msg)
	}
	return fmt.Sprintf("CREATE FUNCTION %[1]s() RETURNS trigger LANGUAGE plpgsql AS $t$BEGIN IF %[4]s THEN RAISE EXCEPTION '%[5]s'; END IF; RETURN NEW; END$t$;\nCREATE TRIGGER %[1]s BEFORE %[2]s ON %[3]s FOR EACH ROW EXECUTE FUNCTION %[1]s();\n", name, event, table, cond, msg)
}
