package drive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// WriteBlob seals r under key into a unique staging file and publishes it only after a
// durable flush. Digest and Size describe the plaintext.
func WriteBlob(root string, key []byte, r io.Reader, limit int64) (Version, error) {
	var v Version
	if limit < 1 {
		return v, ErrInvalid
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return v, err
	}
	f, err := os.CreateTemp(root, ".upload-*")
	if err != nil {
		return v, err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	h := sha256.New()
	n, err := seal(f, key, io.TeeReader(io.LimitReader(r, limit+1), h))
	if err != nil {
		return v, err
	}
	if n > limit {
		return v, fmt.Errorf("%w: upload limit", ErrQuota)
	}
	if err = f.Sync(); err != nil {
		return v, err
	}
	if err = f.Close(); err != nil {
		return v, err
	}
	v = Version{Blob: uuid.NewString(), Digest: hex.EncodeToString(h.Sum(nil)), Size: n, Sealed: true}
	if err = os.Rename(tmp, filepath.Join(root, v.Blob)); err != nil {
		return v, err
	}
	if err = SyncDirectory(root); err != nil {
		os.Remove(filepath.Join(root, v.Blob))
		return v, err
	}
	return v, nil
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

// RemoveBlobs deletes blobs whose last reference was purged. A missing file is already gone.
func RemoveBlobs(root string, ids []string) error {
	var errs []error
	for _, id := range ids {
		path, err := BlobPath(root, id)
		if err == nil {
			err = os.Remove(path)
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if len(ids) > 0 {
		errs = append(errs, SyncDirectory(root))
	}
	return errors.Join(errs...)
}

// SweepOrphans removes staging files left by a crash and blobs no version names. Only files
// older than grace are touched, so an upload between its rename and its commit is safe; only
// names WriteBlob creates are considered.
func (s *Store) SweepOrphans(ctx context.Context, root string, grace time.Duration) (int, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-grace)
	removed := 0
	var errs []error
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		if !strings.HasPrefix(e.Name(), ".upload-") {
			if _, err := BlobPath(root, e.Name()); err != nil {
				continue
			}
			var n int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_versions WHERE blob=?`, e.Name()).Scan(&n); err != nil {
				return removed, err
			}
			if n > 0 {
				continue
			}
		}
		if err := os.Remove(filepath.Join(root, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}
