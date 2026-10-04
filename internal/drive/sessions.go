package drive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type EditorSession struct {
	ID, FileID, User, Key string
	Revision              int64
	Expires               int64
}

func DocumentKey(file string, revision int64) string { return file + "-" + fmtRevision(revision) }
func fmtRevision(n int64) string                     { return fmt.Sprintf("%d", n) }
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func (s *Store) EditorSession(ctx context.Context, user, id, token string) (EditorSession, error) {
	f, err := s.File(ctx, user, id, 1)
	if err != nil || f.Trashed {
		return EditorSession{}, ErrDenied
	}
	e := EditorSession{ID: uuid.NewString(), FileID: id, User: user, Revision: f.Revision, Key: DocumentKey(id, f.Revision), Expires: time.Now().Add(12 * time.Hour).Unix()}
	editable := s.Authorize(ctx, user, f.Workspace, 2) == nil
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := authorize(ctx, tx, user, f.Workspace, 1); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO drive_editor_sessions(id,file_id,revision,user_id,token_hash,expires,editable) SELECT ?,id,?,?,?,?,? FROM drive_files WHERE id=? AND revision=? AND trashed=false`, e.ID, e.Revision, user, tokenHash(token), e.Expires, editable, id, e.Revision)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return ErrConflict
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drive_editor_documents(key,file_id,base_revision,current_revision) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, e.Key, id, e.Revision, e.Revision)
		return err
	})
	return e, err
}
func (s *Store) EditorDownload(ctx context.Context, id, token string) (File, error) {
	var user, file string
	var revision int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id,file_id,revision FROM drive_editor_sessions WHERE id=? AND token_hash=? AND revoked=false AND expires>?`, id, tokenHash(token), time.Now().Unix()).Scan(&user, &file, &revision)
	if err != nil {
		return File{}, ErrDenied
	}
	f, err := s.File(ctx, user, file, 1)
	if err != nil || f.Trashed {
		return File{}, ErrDenied
	}
	err = s.db.QueryRowContext(ctx, `SELECT blob,digest,size FROM drive_versions WHERE file_id=? AND revision=?`, file, revision).Scan(&f.Blob, &f.Digest, &f.Size)
	f.Revision = revision
	return f, err
}

// CallbackActor chooses an authorized participant; ownership does not follow the first opener.
func (s *Store) CallbackActor(ctx context.Context, file string, revision int64) (string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id FROM drive_editor_sessions WHERE file_id=? AND revision=? AND revoked=false AND expires>?`, file, revision, time.Now().Unix())
	if err != nil {
		return "", err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		f, e := s.File(ctx, id, file, 2)
		if e == nil && !f.Trashed {
			return id, nil
		}
	}
	return "", ErrDenied
}
func (s *Store) PendingRevocations(ctx context.Context) ([]EditorSession, error) {
	// Query live memberships at the boundary. No stale role snapshots authorize edits.
	_, err := s.db.ExecContext(ctx, `UPDATE drive_editor_sessions SET revoked=true WHERE expires<=? OR NOT EXISTS(SELECT 1 FROM drive_files f JOIN drive_grants g ON g.workspace=f.workspace JOIN group_members m ON m.group_id=g.group_id JOIN users u ON u.id=m.user_id WHERE f.id=drive_editor_sessions.file_id AND f.trashed=false AND u.id=drive_editor_sessions.user_id AND u.status='active' AND (g.role IN ('editor','manager') OR drive_editor_sessions.editable=false))`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,file_id,revision,user_id,expires FROM drive_editor_sessions WHERE revoked=true AND drop_done=false LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EditorSession{}
	for rows.Next() {
		var e EditorSession
		if err = rows.Scan(&e.ID, &e.FileID, &e.Revision, &e.User, &e.Expires); err != nil {
			return nil, err
		}
		e.Key = DocumentKey(e.FileID, e.Revision)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) MarkDropped(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE drive_editor_sessions SET drop_done=true WHERE id=? AND revoked=true`, id)
	return err
}

// PublishEditor serializes a document's saves with other uploads and acknowledges exact retries.
// A digest already recorded for this document is successful without publishing another blob.
func (s *Store) PublishEditor(ctx context.Context, user string, f File, v Version, key string, final bool) (bool, error) {
	duplicate := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := authorize(ctx, tx, user, f.Workspace, 2); err != nil {
			return err
		}
		var revision int64
		err := tx.QueryRowContext(ctx, `SELECT revision FROM drive_editor_saves WHERE key=? AND digest=?`, key, v.Digest).Scan(&revision)
		if err == nil {
			duplicate = true
			if final {
				_, err = tx.ExecContext(ctx, `UPDATE drive_editor_documents SET closed=true WHERE key=?`, key)
				return err
			}
			return nil
		}
		if err != sql.ErrNoRows {
			return err
		}
		var closed bool
		if err = tx.QueryRowContext(ctx, `SELECT current_revision,closed FROM drive_editor_documents WHERE key=? AND file_id=?`, key, f.ID).Scan(&revision, &closed); err != nil {
			return ErrConflict
		}
		if closed {
			return ErrConflict
		}
		v.FileID = f.ID
		v.Revision = revision + 1
		v.Created = time.Now().UTC().Format(time.RFC3339Nano)
		if err = publishTX(ctx, tx, user, f, v, revision); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE drive_editor_documents SET current_revision=?,closed=? WHERE key=?`, v.Revision, final, key); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drive_editor_saves VALUES(?,?,?)`, key, v.Digest, v.Revision)
		return err
	})
	return duplicate, err
}

func (s *Store) RevocationStatus(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	for key, where := range map[string]string{"pending": "revoked=true AND drop_done=false", "completed": "revoked=true AND drop_done=true", "active": "revoked=false"} {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_editor_sessions WHERE `+where).Scan(&n); err != nil {
			return nil, err
		}
		out[key] = n
	}
	return out, nil
}
