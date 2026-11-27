package drive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/google/uuid"
)

// WriteBlob seals r under k, bound to a new blob ID, into a draft in st and publishes it only
// after a durable flush. Digest and Size describe the plaintext.
func WriteBlob(st objects.Store, k Keys, r io.Reader, limit int64) (Version, error) {
	id := uuid.NewString()
	v, err := writeObject(st, k, id, blobBinding(id), r, limit)
	if err != nil {
		return v, err
	}
	v.Blob, v.Sealed = id, true
	return v, nil
}

// writeObject seals r under k and bind as st's object name.
func writeObject(st objects.Store, k Keys, name, bind string, r io.Reader, limit int64) (Version, error) {
	var v Version
	if limit < 1 {
		return v, ErrInvalid
	}
	st = objects.Scope(st, k.org)
	w, err := st.Create(context.Background())
	if err != nil {
		return v, err
	}
	defer w.Discard()
	h := sha256.New()
	n, err := seal(w, k, bind, io.TeeReader(io.LimitReader(r, limit+1), h))
	if err != nil {
		return v, err
	}
	if n > limit {
		return v, ErrTooLarge
	}
	if err = w.Publish(name); err != nil {
		return v, err
	}
	return Version{Digest: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

// stage seals r under key and bind into a synced, closed staging file in root and returns its
// path; the caller renames or removes it. Only the single-tenant format upgrade, which replaces
// a file in place, still uses it.
func stage(root string, key []byte, bind string, r io.Reader, limit int64) (string, Version, error) {
	var v Version
	if limit < 1 {
		return "", v, ErrInvalid
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", v, err
	}
	f, err := os.CreateTemp(root, ".upload-*")
	if err != nil {
		return "", v, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	n, err := seal(f, Deployment(key), bind, io.TeeReader(io.LimitReader(r, limit+1), h))
	if err != nil {
		return "", v, err
	}
	if n > limit {
		return "", v, ErrTooLarge
	}
	if err = f.Sync(); err != nil {
		return "", v, err
	}
	if err = f.Close(); err != nil {
		return "", v, err
	}
	ok = true
	return f.Name(), Version{Digest: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

func SyncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func BlobPath(root, id string) (string, error) {
	if parsed, err := uuid.Parse(id); err != nil || parsed.String() != id {
		return "", ErrInvalid
	}
	return filepath.Join(root, id), nil
}

// RemoveBlobs deletes blobs whose last reference was purged. A missing blob is already gone.
// It runs to the end whatever the request does, since the rows are already gone.
func RemoveBlobs(st objects.Store, ids []string) error {
	var errs []error
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := BlobPath("", id); err != nil {
			errs = append(errs, err)
			continue
		}
		valid = append(valid, id)
	}
	if len(ids) > 0 {
		errs = append(errs, st.Remove(context.Background(), valid...))
	}
	return errors.Join(errs...)
}

// SweepOrphans removes staging files left by a crash and blobs no version names. Only files
// older than grace are touched, so an upload between its rename and its commit is safe; only
// names WriteBlob creates are considered.
func (s *Store) SweepOrphans(ctx context.Context, st objects.Store, grace time.Duration) (int, error) {
	cutoff := time.Now().Add(-grace)
	var drafts, orphans []string
	err := st.List(ctx, func(name string, modified time.Time) error {
		if modified.After(cutoff) {
			return nil
		}
		if strings.HasPrefix(name, ".upload-") {
			drafts = append(drafts, name)
			return nil
		}
		if _, err := BlobPath("", name); err != nil {
			return nil
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_versions WHERE blob=?`, name).Scan(&n); err != nil || n > 0 {
			return err
		}
		orphans = append(orphans, name)
		return nil
	})
	if err != nil {
		return 0, err
	}
	removed := 0
	var errs []error
	if l, ok := st.(objects.Local); ok {
		for _, name := range drafts {
			if err := os.Remove(filepath.Join(l.Root(), name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				errs = append(errs, err)
				continue
			}
			removed++
		}
	}
	for _, name := range orphans {
		if err := st.Remove(ctx, name); err != nil {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}
