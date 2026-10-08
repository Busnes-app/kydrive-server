package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
)

var (
	ErrDenied   = errors.New("drive access denied")
	ErrConflict = errors.New("revision conflict")
	ErrQuota    = errors.New("workspace quota exceeded")
	ErrInvalid  = errors.New("invalid drive input")
)

type Store struct{ db *sql.DB }
type Workspace struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Name  string `json:"name"`
	Quota int64  `json:"quota"`
	Used  int64  `json:"used"`
	Role  string `json:"role"`
}
type Grant struct {
	GroupID string `json:"group_id"`
	Name    string `json:"name"`
	Role    string `json:"role"`
}
type Folder struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Parent    string `json:"parent"`
	Name      string `json:"name"`
	Trashed   bool   `json:"trashed"`
}
type File struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Parent    string `json:"parent"`
	Name      string `json:"name"`
	Revision  int64  `json:"revision"`
	Trashed   bool   `json:"trashed"`
	Size      int64  `json:"size"`
	Digest    string `json:"digest"`
	Blob      string `json:"-"`
}
type Version struct {
	FileID   string `json:"file_id"`
	Revision int64  `json:"revision"`
	Blob     string `json:"-"`
	Digest   string `json:"digest"`
	Size     int64  `json:"size"`
	Created  string `json:"created"`
}
type Event struct {
	ID       string `json:"id"`
	User     string `json:"user"`
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Created  string `json:"created"`
}

func New(ctx context.Context, db *sql.DB) (*Store, error) {
	_, err := db.ExecContext(ctx, `
 CREATE UNIQUE INDEX IF NOT EXISTS drive_scim_subject ON users(sso_subject) WHERE sso_provider='scim' AND sso_subject!='';
 CREATE UNIQUE INDEX IF NOT EXISTS drive_group_external ON groups(external_id) WHERE external_id!='';
 CREATE TABLE IF NOT EXISTS drive_service_tokens(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,workspace TEXT NOT NULL,name TEXT NOT NULL,role TEXT NOT NULL,token_hash TEXT NOT NULL UNIQUE,expires BIGINT NOT NULL,revoked BOOLEAN NOT NULL DEFAULT false);
 CREATE TABLE IF NOT EXISTS drive_workspaces(id TEXT PRIMARY KEY,name TEXT NOT NULL,quota BIGINT NOT NULL CHECK(quota>0));
 CREATE TABLE IF NOT EXISTS drive_personal_workspaces(workspace TEXT PRIMARY KEY REFERENCES drive_workspaces(id),owner TEXT NOT NULL UNIQUE);
 CREATE TABLE IF NOT EXISTS drive_grants(workspace TEXT NOT NULL REFERENCES drive_workspaces(id),group_id TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,role TEXT NOT NULL CHECK(role IN ('reader','editor','manager')),PRIMARY KEY(workspace,group_id));
 CREATE TABLE IF NOT EXISTS drive_folders(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,UNIQUE(workspace,parent,name));
 CREATE TABLE IF NOT EXISTS drive_files(id TEXT PRIMARY KEY,workspace TEXT NOT NULL REFERENCES drive_workspaces(id),parent TEXT NOT NULL,name TEXT NOT NULL,revision BIGINT NOT NULL,trashed BOOLEAN NOT NULL DEFAULT false);
 CREATE UNIQUE INDEX IF NOT EXISTS drive_live_names ON drive_files(workspace,parent,name) WHERE trashed=false;
 CREATE TABLE IF NOT EXISTS drive_versions(file_id TEXT NOT NULL REFERENCES drive_files(id),revision BIGINT NOT NULL,blob TEXT NOT NULL,digest TEXT NOT NULL,size BIGINT NOT NULL CHECK(size>=0),created TEXT NOT NULL,PRIMARY KEY(file_id,revision));
 CREATE TABLE IF NOT EXISTS drive_events(id TEXT PRIMARY KEY,user_id TEXT NOT NULL,action TEXT NOT NULL,resource TEXT NOT NULL,created TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS drive_editor_sessions(id TEXT PRIMARY KEY,file_id TEXT NOT NULL REFERENCES drive_files(id),revision BIGINT NOT NULL,user_id TEXT NOT NULL,token_hash TEXT NOT NULL,expires BIGINT NOT NULL,revoked BOOLEAN NOT NULL DEFAULT false,drop_done BOOLEAN NOT NULL DEFAULT false,editable BOOLEAN NOT NULL DEFAULT true);
 CREATE TABLE IF NOT EXISTS drive_editor_documents(key TEXT PRIMARY KEY,file_id TEXT NOT NULL,base_revision BIGINT NOT NULL,current_revision BIGINT NOT NULL,closed BOOLEAN NOT NULL DEFAULT false);
 CREATE TABLE IF NOT EXISTS drive_editor_saves(key TEXT NOT NULL,digest TEXT NOT NULL,revision BIGINT NOT NULL,PRIMARY KEY(key,digest));
 CREATE INDEX IF NOT EXISTS drive_editor_expiry ON drive_editor_sessions(expires);
 `)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err = s.migrate(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func ValidName(name string) bool {
	if name == "" || len(name) > 255 || strings.TrimSpace(name) != name || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if r == '/' || r == '\\' || unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return false
		}
	}
	return true
}
func Rank(role string) int {
	switch role {
	case "reader":
		return 1
	case "editor":
		return 2
	case "manager":
		return 3
	}
	return 0
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func authorize(ctx context.Context, q queryer, user, workspace string, need int) error {
	if a, ok := ctx.Value(serviceAccessKey{}).(ServiceAccess); ok && (a.Workspace != workspace || a.Rank < need) {
		return ErrDenied
	}
	var rank int
	err := q.QueryRowContext(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM drive_personal_workspaces p JOIN users u ON u.id=p.owner WHERE p.workspace=? AND p.owner=? AND u.status='active') THEN 3 ELSE COALESCE((SELECT MAX(CASE g.role WHEN 'manager' THEN 3 WHEN 'editor' THEN 2 ELSE 1 END) FROM drive_grants g JOIN group_members m ON m.group_id=g.group_id JOIN users u ON u.id=m.user_id WHERE g.workspace=? AND u.id=? AND u.status='active' AND NOT EXISTS(SELECT 1 FROM drive_personal_workspaces WHERE workspace=g.workspace)),0) END`, workspace, user, workspace, user).Scan(&rank)
	if err != nil {
		return err
	}
	if rank < need {
		return ErrDenied
	}
	return nil
}
func admin(ctx context.Context, q queryer, user string) error {
	if _, ok := ctx.Value(serviceAccessKey{}).(ServiceAccess); ok {
		return ErrDenied
	}
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND status='active' AND role='admin'`, user).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrDenied
	}
	return nil
}
func event(ctx context.Context, tx *sql.Tx, user, action, resource string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO drive_events VALUES(?,?,?,?,?)`, uuid.NewString(), user, action, resource, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}
func (s *Store) transaction(ctx context.Context, f func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = f(tx); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Authorize(ctx context.Context, user, workspace string, need int) error {
	return authorize(ctx, s.db, user, workspace, need)
}

func (s *Store) Workspaces(ctx context.Context, user string, administration bool) ([]Workspace, error) {
	if administration {
		if err := admin(ctx, s.db, user); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT w.id,w.name,w.quota,CASE WHEN p.workspace IS NULL THEN 'shared' ELSE 'personal' END,COALESCE((SELECT SUM(v.size) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=w.id),0),CASE WHEN p.owner=? THEN 3 ELSE COALESCE((SELECT MAX(CASE g.role WHEN 'manager' THEN 3 WHEN 'editor' THEN 2 ELSE 1 END) FROM drive_grants g JOIN group_members m ON m.group_id=g.group_id WHERE g.workspace=w.id AND m.user_id=? AND p.workspace IS NULL),0) END FROM drive_workspaces w LEFT JOIN drive_personal_workspaces p ON p.workspace=w.id WHERE (p.workspace IS NULL OR p.owner=?) AND EXISTS(SELECT 1 FROM users WHERE id=? AND status='active') ORDER BY w.name`, user, user, user, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Workspace{}
	for rows.Next() {
		var w Workspace
		var rank int
		if err = rows.Scan(&w.ID, &w.Name, &w.Quota, &w.Kind, &w.Used, &rank); err != nil {
			return nil, err
		}
		w.Role = []string{"", "reader", "editor", "manager"}[rank]
		if a, ok := ctx.Value(serviceAccessKey{}).(ServiceAccess); ok && a.Workspace != w.ID {
			continue
		}
		if rank > 0 || administration {
			out = append(out, w)
		}
	}
	return out, rows.Err()
}

// EnsurePersonalWorkspace creates one private workspace per immutable account ID.
// Ownership is retained when the directory account is removed; another account cannot inherit it.
func (s *Store) EnsurePersonalWorkspace(ctx context.Context, user string) (string, error) {
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service {
		return "", ErrDenied
	}
	var id string
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND status='active'`, user).Scan(&active); err != nil {
			return err
		}
		if active != 1 {
			return ErrDenied
		}
		err := tx.QueryRowContext(ctx, `SELECT workspace FROM drive_personal_workspaces WHERE owner=?`, user).Scan(&id)
		if err == nil {
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		id = uuid.NewString()
		if _, err = tx.ExecContext(ctx, `INSERT INTO drive_workspaces(id,name,quota) VALUES(?,?,?)`, id, "My files", int64(10<<30)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO drive_personal_workspaces VALUES(?,?)`, id, user); err != nil {
			return err
		}
		return event(ctx, tx, user, "workspace.personal_created", id)
	})
	return id, err
}

func (s *Store) CreateWorkspace(ctx context.Context, user, name string, quota int64) (Workspace, error) {
	w := Workspace{ID: uuid.NewString(), Name: name, Quota: quota, Kind: "shared"}
	if !ValidName(name) || quota < 1 {
		return w, ErrInvalid
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, user); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_workspaces(id,name,quota) VALUES(?,?,?)`, w.ID, name, quota); err != nil {
			return err
		}
		return event(ctx, tx, user, "workspace.created", w.ID)
	})
	return w, err
}
func sharedWorkspace(ctx context.Context, q queryer, workspace string) error {
	var shared int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_workspaces w LEFT JOIN drive_personal_workspaces p ON p.workspace=w.id WHERE w.id=? AND p.workspace IS NULL`, workspace).Scan(&shared); err != nil {
		return err
	}
	if shared != 1 {
		return ErrDenied
	}
	return nil
}

func (s *Store) AuthorizeSharedManager(ctx context.Context, user, workspace string) error {
	if err := sharedWorkspace(ctx, s.db, workspace); err != nil {
		return err
	}
	return authorize(ctx, s.db, user, workspace, 3)
}

func (s *Store) Grants(ctx context.Context, user, workspace string) ([]Grant, error) {
	if err := sharedWorkspace(ctx, s.db, workspace); err != nil {
		return nil, err
	}
	if err := admin(ctx, s.db, user); err != nil {
		if err = authorize(ctx, s.db, user, workspace, 3); err != nil {
			return nil, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT g.group_id,i.display_name,g.role FROM drive_grants g JOIN groups i ON i.id=g.group_id WHERE workspace=? ORDER BY i.display_name`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		if err = rows.Scan(&g.GroupID, &g.Name, &g.Role); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Store) Grant(ctx context.Context, user, workspace, group, role string) error {
	if role != "" && Rank(role) == 0 {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := sharedWorkspace(ctx, tx, workspace); err != nil {
			return err
		}
		if err := admin(ctx, tx, user); err != nil {
			if err = authorize(ctx, tx, user, workspace, 3); err != nil {
				return err
			}
		}
		if role == "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM drive_grants WHERE workspace=? AND group_id=?`, workspace, group); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `INSERT INTO drive_grants VALUES(?,?,?) ON CONFLICT(workspace,group_id) DO UPDATE SET role=excluded.role`, workspace, group, role); err != nil {
				return err
			}
		}
		return event(ctx, tx, user, "workspace.permission_changed", workspace+":"+group)
	})
}
func (s *Store) Quota(ctx context.Context, user, workspace string, quota int64) error {
	if quota < 1 {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := admin(ctx, tx, user); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE drive_workspaces SET quota=? WHERE id=?`, quota, workspace)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrInvalid
		}
		return event(ctx, tx, user, "workspace.quota_changed", workspace)
	})
}
func parentOK(ctx context.Context, q queryer, workspace, parent string) error {
	if parent == "" {
		return nil
	}
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_folders WHERE id=? AND workspace=? AND trashed=false`, parent, workspace).Scan(&n)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrInvalid
	}
	return nil
}
func (s *Store) AddFolder(ctx context.Context, user, workspace, parent, name string) (Folder, error) {
	f := Folder{ID: uuid.NewString(), Workspace: workspace, Parent: parent, Name: name}
	if !ValidName(name) {
		return f, ErrInvalid
	}
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := authorize(ctx, tx, user, workspace, 2); err != nil {
			return err
		}
		if err := parentOK(ctx, tx, workspace, parent); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_folders(id,workspace,parent,name) VALUES(?,?,?,?)`, f.ID, workspace, parent, name); err != nil {
			return err
		}
		return event(ctx, tx, user, "folder.created", f.ID)
	})
	return f, err
}
func (s *Store) Folders(ctx context.Context, user, workspace string, trashed bool) ([]Folder, error) {
	if err := authorize(ctx, s.db, user, workspace, 1); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace,parent,name,trashed FROM drive_folders WHERE workspace=? AND trashed=? ORDER BY name`, workspace, trashed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Folder{}
	for rows.Next() {
		var f Folder
		if err = rows.Scan(&f.ID, &f.Workspace, &f.Parent, &f.Name, &f.Trashed); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

const fileColumns = `f.id,f.workspace,f.parent,f.name,f.revision,f.trashed,v.size,v.digest,v.blob`

func scanFile(row interface{ Scan(...any) error }) (File, error) {
	var f File
	err := row.Scan(&f.ID, &f.Workspace, &f.Parent, &f.Name, &f.Revision, &f.Trashed, &f.Size, &f.Digest, &f.Blob)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrDenied
	}
	return f, err
}
func (s *Store) File(ctx context.Context, user, id string, need int) (File, error) {
	f, err := scanFile(s.db.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE f.id=?`, id))
	if err != nil {
		return f, err
	}
	err = authorize(ctx, s.db, user, f.Workspace, need)
	return f, err
}
func (s *Store) Files(ctx context.Context, user, workspace string, trashed bool) ([]File, error) {
	if err := authorize(ctx, s.db, user, workspace, 1); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileColumns+` FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE f.workspace=? AND f.trashed=? ORDER BY f.name`, workspace, trashed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		f, e := scanFile(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Publish makes a durably stored immutable blob visible. Caller owns that blob until success.
func (s *Store) Publish(ctx context.Context, user string, f File, v Version, expected int64) (File, error) {
	if !ValidName(f.Name) || v.Size < 0 || len(v.Digest) != 64 || v.Blob == "" {
		return f, ErrInvalid
	}
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	v.FileID = f.ID
	v.Revision = expected + 1
	v.Created = time.Now().UTC().Format(time.RFC3339Nano)
	err := s.transaction(ctx, func(tx *sql.Tx) error { return publishTX(ctx, tx, user, f, v, expected) })
	if err != nil {
		return f, err
	}
	f.Revision = v.Revision
	f.Size = v.Size
	f.Digest = v.Digest
	f.Blob = v.Blob
	return f, nil
}
func (s *Store) Versions(ctx context.Context, user, id string) ([]Version, error) {
	if _, err := s.File(ctx, user, id, 1); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT file_id,revision,blob,digest,size,created FROM drive_versions WHERE file_id=? ORDER BY revision DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		if err = rows.Scan(&v.FileID, &v.Revision, &v.Blob, &v.Digest, &v.Size, &v.Created); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) SetTrash(ctx context.Context, user, id string, expected int64, trash bool) error {
	f, err := s.File(ctx, user, id, 2)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := authorize(ctx, tx, user, f.Workspace, 2); err != nil {
			return err
		}
		query := `UPDATE drive_files SET trashed=true,trashed_at=?,trash_batch=? WHERE id=? AND revision=? AND trashed=false`
		args := []any{now(), uuid.NewString(), id, expected}
		if !trash {
			// A file whose folder is still in the trash comes back at the workspace root.
			query = `UPDATE drive_files SET trashed=false,trashed_at='',trash_batch='',parent=CASE WHEN parent='' OR EXISTS(SELECT 1 FROM drive_folders d WHERE d.id=drive_files.parent AND d.trashed=false) THEN parent ELSE '' END WHERE id=? AND revision=? AND trashed=true`
			args = []any{id, expected}
		}
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return ErrConflict
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		return event(ctx, tx, user, "file.trash_changed", id)
	})
}
func (s *Store) RestoreVersion(ctx context.Context, user, id string, revision, expected int64) (File, error) {
	f, err := s.File(ctx, user, id, 2)
	if err != nil {
		return f, err
	}
	if f.Trashed {
		return f, ErrConflict
	}
	v := Version{FileID: id, Revision: expected + 1, Created: now()}
	// Read the old version inside the publish transaction: a purge cannot slip in between.
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT blob,digest,size FROM drive_versions WHERE file_id=? AND revision=?`, id, revision).Scan(&v.Blob, &v.Digest, &v.Size); err != nil {
			return ErrInvalid
		}
		return publishTX(ctx, tx, user, f, v, expected)
	})
	if err != nil {
		return f, err
	}
	f.Revision, f.Size, f.Digest, f.Blob = v.Revision, v.Size, v.Digest, v.Blob
	return f, nil
}
func (s *Store) Events(ctx context.Context, user string) ([]Event, error) {
	if err := admin(ctx, s.db, user); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,action,resource,created FROM drive_events ORDER BY created DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.ID, &e.User, &e.Action, &e.Resource, &e.Created); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func publishTX(ctx context.Context, tx *sql.Tx, user string, f File, v Version, expected int64) error {
	if err := authorize(ctx, tx, user, f.Workspace, 2); err != nil {
		return err
	}
	if err := parentOK(ctx, tx, f.Workspace, f.Parent); err != nil {
		return err
	}
	if err := fits(ctx, tx, f.Workspace, v.Size); err != nil {
		return err
	}
	if expected == 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_files(id,workspace,parent,name,revision) VALUES(?,?,?,?,?)`, f.ID, f.Workspace, f.Parent, f.Name, 1); err != nil {
			if uniqueViolation(err) {
				return fmt.Errorf("%w: name or file exists", ErrConflict)
			}
			return err
		}
	} else {
		res, err := tx.ExecContext(ctx, `UPDATE drive_files SET revision=revision+1 WHERE id=? AND workspace=? AND revision=? AND trashed=false`, f.ID, f.Workspace, expected)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO drive_versions VALUES(?,?,?,?,?,?)`, v.FileID, v.Revision, v.Blob, v.Digest, v.Size, v.Created); err != nil {
		return err
	}
	return event(ctx, tx, user, "file.version_created", f.ID)
}

// fits returns ErrQuota unless workspace can take extra more bytes of retained versions.
func fits(ctx context.Context, q queryer, workspace string, extra int64) error {
	var used, quota int64
	if err := q.QueryRowContext(ctx, `SELECT quota,COALESCE((SELECT SUM(v.size) FROM drive_versions v JOIN drive_files f ON f.id=v.file_id WHERE f.workspace=?),0) FROM drive_workspaces WHERE id=?`, workspace, workspace).Scan(&quota, &used); err != nil {
		return err
	}
	if used > quota || extra > quota-used {
		return ErrQuota
	}
	return nil
}

func uniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
