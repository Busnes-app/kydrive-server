package drive

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// WriteBlob owns a unique staging file and publishes it only after a durable flush.
func WriteBlob(root string, r io.Reader, limit int64) (Version, error) {
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
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, limit+1))
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
	v = Version{Blob: uuid.NewString(), Digest: hex.EncodeToString(h.Sum(nil)), Size: n}
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
