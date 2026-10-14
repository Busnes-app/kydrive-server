package drive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type EditorSession struct {
	ID, FileID, User, Key string
	Revision              int64
	Expires               int64
}

// DocumentKey names one editor document. Epoch 0 keeps the original "<file>-<revision>" form;
// a cross-workspace move starts a new epoch so sessions after it never share a key with older ones.
func DocumentKey(file string, revision, epoch int64) string {
	key := fmt.Sprintf("%s-%d", file, revision)
	if epoch > 0 {
		key += fmt.Sprintf("e%d", epoch)
	}
	return key
}

func ParseDocumentKey(key string) (file string, revision, epoch int64, err error) {
	at := strings.LastIndex(key, "-")
	if at < 1 {
		return "", 0, 0, ErrInvalid
	}
	rev, ep, found := strings.Cut(key[at+1:], "e")
	if revision, err = strconv.ParseInt(rev, 10, 64); err != nil || revision < 1 {
		return "", 0, 0, ErrInvalid
	}
	if found {
		if epoch, err = strconv.ParseInt(ep, 10, 64); err != nil || epoch < 1 {
			return "", 0, 0, ErrInvalid
		}
	}
	return key[:at], revision, epoch, nil
}
func tokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// editorGrace is how long an expired session on a document Euro-Office still holds open
// keeps its save rights; afterwards the sweep revokes it so purge is never blocked for good.
const editorGrace = 7 * 24 * time.Hour

// sessionKey is DocumentKey in SQL over a drive_editor_sessions row.
const sessionKey = `drive_editor_sessions.file_id||'-'||drive_editor_sessions.revision||CASE WHEN drive_editor_sessions.epoch>0 THEN 'e'||drive_editor_sessions.epoch ELSE '' END`

// EditorSession opens the file for user. A document a force-save left open at the current
// revision and epoch is joined, so co-editors always share one key.
func (s *Store) EditorSession(ctx context.Context, user, id string) (EditorSession, error) {
	f, err := s.File(ctx, user, id, 1)
	if err != nil || f.Trashed {
		return EditorSession{}, ErrDenied
	}
	e := EditorSession{ID: uuid.NewString(), FileID: id, User: user, Revision: f.Revision, Expires: time.Now().Add(12 * time.Hour).Unix()}
	editable := s.Authorize(ctx, user, f.Workspace, 2) == nil
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := authorize(ctx, tx, user, f.Workspace, 1); err != nil {
			return err
		}
		var epoch int64
		if err := tx.QueryRowContext(ctx, `SELECT editor_epoch FROM drive_files WHERE id=? AND workspace=? AND revision=? AND trashed=false`, id, f.Workspace, e.Revision).Scan(&epoch); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			return err
		}
		e.Key = DocumentKey(id, e.Revision, epoch)
		open, err := column(ctx, tx, `SELECT key FROM drive_editor_documents WHERE file_id=? AND closed=false AND current_revision=? ORDER BY base_revision`, id, e.Revision)
		if err != nil {
			return err
		}
		for _, key := range open {
			if _, base, ep, err := ParseDocumentKey(key); err == nil && ep == epoch {
				e.Key, e.Revision = key, base
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_editor_sessions(id,file_id,revision,user_id,token_hash,expires,editable,epoch) VALUES(?,?,?,?,'',?,?,?)`, e.ID, id, e.Revision, user, e.Expires, editable, epoch); err != nil {
			return err
		}
		// A key closed without saves (status 4) names unchanged content and opens again.
		_, err = tx.ExecContext(ctx, `INSERT INTO drive_editor_documents(key,file_id,base_revision,current_revision) VALUES(?,?,?,?) ON CONFLICT(key) DO UPDATE SET closed=false WHERE current_revision=base_revision`, e.Key, id, e.Revision, e.Revision)
		return err
	})
	return e, err
}

// EditorDownload serves a live session's document at its newest saved revision. The caller
// has verified that Euro-Office signed the request.
func (s *Store) EditorDownload(ctx context.Context, id string) (File, error) {
	var user, file string
	var revision, epoch int64
	err := s.db.QueryRowContext(ctx, `SELECT user_id,file_id,revision,epoch FROM drive_editor_sessions WHERE id=? AND revoked=false AND expires>?`, id, time.Now().Unix()).Scan(&user, &file, &revision, &epoch)
	if err != nil {
		return File{}, ErrDenied
	}
	if err = s.db.QueryRowContext(ctx, `SELECT current_revision FROM drive_editor_documents WHERE key=?`, DocumentKey(file, revision, epoch)).Scan(&revision); err != nil {
		return File{}, ErrDenied
	}
	f, err := s.File(ctx, user, file, 1)
	if err != nil || f.Trashed {
		return File{}, ErrDenied
	}
	err = s.db.QueryRowContext(ctx, `SELECT blob,digest,size,sealed FROM drive_versions WHERE file_id=? AND revision=?`, file, revision).Scan(&f.Blob, &f.Digest, &f.Size, &f.Sealed)
	f.Revision = revision
	return f, err
}

// CallbackActor chooses an authorized participant; ownership does not follow the first opener.
// Age alone does not refuse a save: the sweep revokes expired sessions once their document closes.
func (s *Store) CallbackActor(ctx context.Context, file string, revision, epoch int64) (string, error) {
	ids, err := column(ctx, s.db, `SELECT user_id FROM drive_editor_sessions WHERE file_id=? AND revision=? AND epoch=? AND revoked=false`, file, revision, epoch)
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

// CloseEditor records that Euro-Office closed key without changes.
func (s *Store) CloseEditor(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE drive_editor_documents SET closed=true WHERE key=?`, key)
	return err
}

// RevokeUserEditors revokes user's editor sessions in the caller's transaction and starts a new
// key epoch on their files, so a saved editor configuration cannot rejoin the old document.
// Nil (no drive store) is a no-op.
func (s *Store) RevokeUserEditors(ctx context.Context, tx *sql.Tx, user string) error {
	if s == nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drive_files SET editor_epoch=editor_epoch+1 WHERE id IN (SELECT file_id FROM drive_editor_sessions WHERE user_id=? AND revoked=false)`, user); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE drive_editor_sessions SET revoked=true WHERE user_id=? AND revoked=false`, user)
	return err
}

func (s *Store) PendingRevocations(ctx context.Context) ([]EditorSession, error) {
	// Query live memberships at the boundary. No stale role snapshots authorize edits.
	at := time.Now()
	_, err := s.db.ExecContext(ctx, `UPDATE drive_editor_sessions SET revoked=true WHERE revoked=false AND ((expires<=? AND (expires<=? OR NOT EXISTS(SELECT 1 FROM drive_editor_documents d WHERE d.file_id=drive_editor_sessions.file_id AND d.key=`+sessionKey+` AND d.closed=false))) OR NOT EXISTS(SELECT 1 FROM drive_files f JOIN users u ON u.id=drive_editor_sessions.user_id LEFT JOIN drive_personal_workspaces p ON p.workspace=f.workspace WHERE f.id=drive_editor_sessions.file_id AND f.trashed=false AND u.status='active' AND (p.owner=u.id OR (p.workspace IS NULL AND EXISTS(SELECT 1 FROM drive_grants g JOIN group_members m ON m.group_id=g.group_id WHERE g.workspace=f.workspace AND m.user_id=u.id AND (g.role IN ('editor','manager') OR drive_editor_sessions.editable=false))))))`, at.Unix(), at.Add(-editorGrace).Unix())
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,file_id,revision,epoch,user_id,expires FROM drive_editor_sessions WHERE revoked=true AND drop_done=false AND next_attempt<=? ORDER BY next_attempt LIMIT 100`, at.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EditorSession{}
	for rows.Next() {
		var e EditorSession
		var epoch int64
		if err = rows.Scan(&e.ID, &e.FileID, &e.Revision, &epoch, &e.User, &e.Expires); err != nil {
			return nil, err
		}
		e.Key = DocumentKey(e.FileID, e.Revision, epoch)
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) MarkDropped(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE drive_editor_sessions SET drop_done=true WHERE id=? AND revoked=true`, id)
	return err
}

// DeferRevocation backs a failed drop off exponentially, 2s doubling up to an hour, so drops
// that keep failing queue behind newer ones.
func (s *Store) DeferRevocation(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE drive_editor_sessions SET attempts=attempts+1,next_attempt=?+MIN(3600,2<<MIN(attempts,11)) WHERE id=? AND revoked=true AND drop_done=false`, time.Now().Unix(), id)
	return err
}

// PublishEditor serializes a document's saves with other uploads and acknowledges exact retries.
// The digest of the document's newest save is successful without publishing another blob.
func (s *Store) PublishEditor(ctx context.Context, user string, f File, v Version, key string, final bool) (bool, error) {
	duplicate := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := authorize(ctx, tx, user, f.Workspace, 2); err != nil {
			return err
		}
		var revision int64
		// A retry repeats the newest save; an older digest coming back (A, B, A) is a new edit.
		err := tx.QueryRowContext(ctx, `SELECT s.revision FROM drive_editor_saves s JOIN drive_editor_documents d ON d.key=s.key WHERE s.key=? AND s.digest=? AND s.revision=d.current_revision`, key, v.Digest).Scan(&revision)
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
		if err = s.publishTX(ctx, tx, user, f, v, revision); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE drive_editor_documents SET current_revision=?,closed=? WHERE key=?`, v.Revision, final, key); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drive_editor_saves(key,digest,revision) VALUES(?,?,?) ON CONFLICT(key,digest) DO UPDATE SET revision=excluded.revision`, key, v.Digest, v.Revision)
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
