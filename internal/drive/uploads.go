package drive

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
)

// An upload session stages one large upload as sealed segments under <staging>/<session>/,
// one per request, so a dropped connection loses only the request in flight. CompleteUpload
// streams the segments through WriteBlob, which hashes and seals the final blob.
const (
	uploadTTL      = 24 * time.Hour
	uploadsPerUser = 8
	// completionClaim is how long a completion's claim holds; a running completion renews it
	// every third of that (completion has no time limit), so only a crashed one's lapses.
	completionClaim = 30 * time.Minute
)

type UploadSession struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Parent    string `json:"parent"`
	Name      string `json:"name"`
	File      string `json:"file"`
	Expected  int64  `json:"revision"`
	Size      int64  `json:"size"`
	Received  int64  `json:"received"`
	Expires   int64  `json:"expires"`
	Mtime     string `json:"mtime"` // applied to the published version
}

// StartUpload opens a session for a new file (File empty, Expected 0) or a new version of
// File at revision Expected. Size must fit max and the workspace's free quota now; Publish
// checks quota again at completion.
func (s *Store) StartUpload(ctx context.Context, user string, u UploadSession, max int64) (UploadSession, error) {
	u.ID, u.Received, u.Expires = uuid.NewString(), 0, time.Now().Add(uploadTTL).Unix()
	if u.Size < 0 || u.Expected < 0 || (u.File == "") != (u.Expected == 0) || (u.File == "" && !ValidName(u.Name)) {
		return u, ErrInvalid
	}
	if u.Size > max {
		return u, fmt.Errorf("%w: file is larger than the upload limit", ErrQuota)
	}
	var err error
	if u.Mtime, err = Mtime(u.Mtime); err != nil {
		return u, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var stored string
		var revision int64
		if u.File != "" {
			// A new version keeps the file's place and name.
			err := tx.QueryRowContext(ctx, `SELECT parent,name,revision FROM drive_files WHERE id=? AND workspace=? AND trashed=false`, u.File, u.Workspace).Scan(&u.Parent, &stored, &revision)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrDenied
			}
			if err != nil {
				return err
			}
		}
		if err := authorizeItem(ctx, tx, user, u.Workspace, u.Parent, u.File, 2); err != nil {
			return err
		}
		if u.File != "" {
			var err error
			if revision != u.Expected {
				return ErrConflict
			}
			if u.Name, err = s.names.open("drive_files", u.File, stored); err != nil {
				return err
			}
		}
		if err := parentOK(ctx, tx, u.Workspace, u.Parent); err != nil {
			return err
		}
		if err := fits(ctx, tx, u.Workspace, u.Size); err != nil {
			return err
		}
		var open int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_upload_sessions WHERE owner=? AND expires>?`, user, time.Now().Unix()).Scan(&open); err != nil {
			return err
		}
		if open >= uploadsPerUser {
			return fmt.Errorf("%w: too many uploads in progress", ErrQuota)
		}
		stored, mac, err := s.names.seal("drive_upload_sessions", u.ID, u.Name)
		if err != nil {
			return err
		}
		if u.File == "" {
			// Refuse a name a live file or folder holds before the bytes are sent; Publish still decides.
			var taken int
			if err = tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM drive_files WHERE workspace=?1 AND parent=?2 AND name_hmac=?3 AND trashed=false)+(SELECT COUNT(*) FROM drive_folders WHERE workspace=?1 AND parent=?2 AND name_hmac=?3 AND trashed=false)`, u.Workspace, u.Parent, mac).Scan(&taken); err != nil {
				return err
			}
			if taken > 0 {
				return fmt.Errorf("%w: name or file exists", ErrConflict)
			}
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO drive_upload_sessions(id,owner,workspace,parent,name,name_hmac,file,expected,size,expires,mtime) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			u.ID, user, u.Workspace, u.Parent, stored, mac, u.File, u.Expected, u.Size, u.Expires, u.Mtime); err != nil {
			return err
		}
		return event(ctx, tx, user, "upload.started", u.ID)
	})
	return u, err
}

// Upload returns the caller's own unexpired session, rechecking edit access to its workspace.
func (s *Store) Upload(ctx context.Context, user, id string) (UploadSession, error) {
	u := UploadSession{ID: id}
	var stored string
	err := s.db.QueryRowContext(ctx, `SELECT workspace,parent,name,file,expected,size,received,expires,mtime FROM drive_upload_sessions WHERE id=? AND owner=? AND expires>?`, id, user, time.Now().Unix()).
		Scan(&u.Workspace, &u.Parent, &stored, &u.File, &u.Expected, &u.Size, &u.Received, &u.Expires, &u.Mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrDenied
	}
	if err != nil {
		return u, err
	}
	if u.Name, err = s.names.open("drive_upload_sessions", id, stored); err != nil {
		return u, err
	}
	return u, authorizeItem(ctx, s.db, user, u.Workspace, u.Parent, u.File, 2)
}

// AppendUpload seals exactly length bytes of r as the segment starting at start, which must be
// the session's received count. Concurrent appends at one offset: one wins, the rest ErrConflict.
func (s *Store) AppendUpload(ctx context.Context, staging string, key []byte, user, id string, start, length int64, r io.Reader) (UploadSession, error) {
	u, err := s.Upload(ctx, user, id)
	if err != nil {
		return u, err
	}
	if start != u.Received {
		return u, ErrConflict
	}
	if length < 1 || length > u.Size-start {
		return u, ErrInvalid
	}
	dir := filepath.Join(staging, id)
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return u, err
	}
	path := filepath.Join(dir, uuid.NewString())
	if err = writeSegment(dir, path, key, r, length); err != nil {
		return u, err
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE drive_upload_sessions SET received=received+? WHERE id=? AND owner=? AND received=?`, length, id, user, start)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return cmp.Or(err, ErrConflict)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drive_upload_segments(session,start,path,size) VALUES(?,?,?,?)`, id, start, filepath.Base(path), length)
		return err
	})
	if err != nil {
		os.Remove(path)
		return u, err
	}
	u.Received += length
	return u, nil
}

func writeSegment(dir, path string, key []byte, r io.Reader, length int64) error {
	f, err := os.CreateTemp(dir, ".part-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	n, err := seal(f, key, blobBinding(filepath.Base(path)), io.LimitReader(r, length+1))
	if err != nil {
		return err
	}
	if n != length {
		return fmt.Errorf("%w: chunk is %d bytes, not %d", ErrInvalid, n, length)
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return SyncDirectory(dir)
}

// CompleteUpload publishes a fully received session as a file version and removes it.
func (s *Store) CompleteUpload(ctx context.Context, staging, blobs string, key []byte, user, id string) (File, error) {
	u, err := s.Upload(ctx, user, id)
	if err != nil {
		return File{}, err
	}
	release, err := s.claimCompletion(ctx, user, id, completionClaim/3)
	if err != nil {
		return File{}, err
	}
	published := false
	defer func() { release(published) }()
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(s.readSegments(ctx, pw, staging, key, id)) }()
	v, err := WriteBlob(blobs, key, pr, max(u.Size, 1))
	pr.Close()
	if err == nil && v.Size != u.Size {
		RemoveBlobs(blobs, []string{v.Blob})
		err = fmt.Errorf("%w: staged %d bytes, expected %d", ErrInvalid, v.Size, u.Size)
	}
	if err != nil {
		return File{}, err
	}
	v.Mtime = u.Mtime
	f, err := s.Publish(ctx, user, File{ID: u.File, Workspace: u.Workspace, Parent: u.Parent, Name: u.Name}, v, u.Expected)
	if err != nil {
		RemoveBlobs(blobs, []string{v.Blob})
		return f, err
	}
	published = true
	// The file is published: a failed cleanup must not tell the client otherwise, or its
	// retry publishes again. ExpireUploads removes what is left.
	if err := s.dropUpload(context.WithoutCancel(ctx), staging, id, nil); err != nil {
		log.Printf("upload %s: published, cleanup failed: %v", id, err)
	}
	return f, nil
}

// claimCompletion claims a fully received session before its file-sized re-seal, so concurrent
// completions cannot each write a full copy, and renews the claim every interval until release,
// since completion has no time limit: a long one is never taken for crashed by a second
// completion, ExpireUploads or a workspace delete. Only a crashed request's claim lapses.
// release(published) stops the renewal and, unless the file was published, gives the claim up.
func (s *Store) claimCompletion(ctx context.Context, user, id string, every time.Duration) (release func(published bool), err error) {
	claim := time.Now().Unix()
	n, err := rowsAffected(s.db.ExecContext(ctx, `UPDATE drive_upload_sessions SET completing=? WHERE id=? AND owner=? AND received=size AND completing<?`, claim, id, user, claim-int64(completionClaim/time.Second)))
	if err != nil || n != 1 {
		return nil, cmp.Or(err, fmt.Errorf("%w: upload incomplete or already completing", ErrConflict))
	}
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				now := time.Now().Unix()
				if n, _ := rowsAffected(s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE drive_upload_sessions SET completing=? WHERE id=? AND completing=?`, now, id, claim)); n == 1 {
					claim = now
				}
			}
		}
	}()
	return func(published bool) {
		close(done)
		<-stopped
		if !published {
			s.db.ExecContext(context.WithoutCancel(ctx), `UPDATE drive_upload_sessions SET completing=0 WHERE id=? AND completing=?`, id, claim)
		}
	}, nil
}

func rowsAffected(res sql.Result, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) readSegments(ctx context.Context, w io.Writer, staging string, key []byte, id string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT path,size FROM drive_upload_segments WHERE session=? ORDER BY start`, id)
	if err != nil {
		return err
	}
	type segment struct {
		path string
		size int64
	}
	var segments []segment
	for rows.Next() {
		var g segment
		if err = rows.Scan(&g.path, &g.size); err != nil {
			rows.Close()
			return err
		}
		segments = append(segments, g)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, g := range segments {
		r, err := OpenBlob(filepath.Join(staging, id, g.path), key, true, g.size)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, r)
		r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// CancelUpload discards the caller's session and its staged bytes.
func (s *Store) CancelUpload(ctx context.Context, staging, user, id string) error {
	if _, err := s.Upload(ctx, user, id); err != nil {
		return err
	}
	return s.dropUpload(ctx, staging, id, func(tx *sql.Tx) error {
		// A completion still holding its claim (or one that crashed less than a claim ago) owns
		// the staged bytes: they may yet be published, or be completed by a retry.
		var claimed int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_upload_sessions WHERE id=? AND completing>=?`, id, time.Now().Unix()-int64(completionClaim/time.Second)).Scan(&claimed); err != nil {
			return err
		}
		if claimed > 0 {
			return fmt.Errorf("%w: upload is completing", ErrConflict)
		}
		return event(ctx, tx, user, "upload.cancelled", id)
	})
}

// dropUpload removes a session and its staging; check, when set, runs first in the same
// transaction and can refuse.
func (s *Store) dropUpload(ctx context.Context, staging, id string, check func(*sql.Tx) error) error {
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		if check != nil {
			if err := check(tx); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM drive_upload_segments WHERE session=?`, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM drive_upload_sessions WHERE id=?`, id)
		return err
	})
	if err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(staging, id))
}

// expireUpload drops one session only if the deleting transaction still finds it expired and
// unclaimed, so a completion that claimed it after the selection keeps its bytes.
func (s *Store) expireUpload(ctx context.Context, staging, id string, now int64) error {
	gone := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		// Segments reference the session, so they go first; the session delete decides.
		if _, err := tx.ExecContext(ctx, `DELETE FROM drive_upload_segments WHERE session=? AND session IN (SELECT id FROM drive_upload_sessions WHERE expires<=? AND completing<?)`, id, now, now-int64(completionClaim/time.Second)); err != nil {
			return err
		}
		n, err := rowsAffected(tx.ExecContext(ctx, `DELETE FROM drive_upload_sessions WHERE id=? AND expires<=? AND completing<?`, id, now, now-int64(completionClaim/time.Second)))
		gone = n == 1
		return err
	})
	if err != nil || !gone {
		return err
	}
	return os.RemoveAll(filepath.Join(staging, id))
}

// ExpireUploads removes expired sessions, then staging directories with no session that are an
// hour old (crash residue; the hour keeps a session that is starting up safe). Staging is not
// user data.
func (s *Store) ExpireUploads(ctx context.Context, staging string) (int, error) {
	// A completion in progress holds its claim; expiring it would delete the bytes it is sealing.
	now := time.Now().Unix()
	expired, err := column(ctx, s.db, `SELECT id FROM drive_upload_sessions WHERE expires<=? AND completing<?`, now, now-int64(completionClaim/time.Second))
	if err != nil {
		return 0, err
	}
	var errs []error
	for _, id := range expired {
		errs = append(errs, s.expireUpload(ctx, staging, id, now))
	}
	entries, err := os.ReadDir(staging)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, err)
	}
	cutoff := time.Now().Add(-time.Hour)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !e.IsDir() || info.ModTime().After(cutoff) {
			continue
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_upload_sessions WHERE id=?`, e.Name()).Scan(&n); err != nil || n > 0 {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, os.RemoveAll(filepath.Join(staging, e.Name())))
	}
	return len(expired), errors.Join(errs...)
}
