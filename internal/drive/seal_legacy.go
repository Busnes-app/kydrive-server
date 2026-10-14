package drive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// SealLegacyBlobs replaces every plaintext blob with a sealed copy under a new ID. Each swap
// moves all version rows naming the old blob in one transaction and records the old ID in
// drive_blob_retire; the file is removed after commit. Pending retirements from an
// interrupted run are finished first, so a rerun is always safe.
func (s *Store) SealLegacyBlobs(ctx context.Context, root string, key []byte) (int, error) {
	if err := s.retireBlobs(ctx, root); err != nil {
		return 0, err
	}
	sealed, failed, after := 0, 0, ""
	defer func() {
		if failed > 0 {
			log.Printf("blob sealing: %d blobs left in plaintext", failed)
		}
	}()
	for {
		ids, err := column(ctx, s.db, `SELECT DISTINCT blob FROM drive_versions WHERE sealed=0 AND blob>? ORDER BY blob LIMIT 100`, after)
		if err != nil || len(ids) == 0 {
			return sealed, err
		}
		for _, id := range ids {
			after = id
			v, err := s.sealCopy(ctx, root, key, id)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err == nil {
				err = s.swapBlob(ctx, root, id, v)
			}
			if ctx.Err() != nil {
				return sealed, ctx.Err()
			}
			if err != nil {
				failed++
				log.Printf("blob sealing: %s: %v", id, err)
				continue
			}
			sealed++
		}
		if err = s.retireBlobs(ctx, root); err != nil {
			return sealed, err
		}
	}
}

// sealCopy writes a sealed copy of a plaintext blob, checked against its recorded digest.
func (s *Store) sealCopy(ctx context.Context, root string, key []byte, id string) (Version, error) {
	var want Version
	if err := s.db.QueryRowContext(ctx, `SELECT digest,size FROM drive_versions WHERE blob=? AND sealed=0 LIMIT 1`, id).Scan(&want.Digest, &want.Size); err != nil {
		return want, err
	}
	path, err := BlobPath(root, id)
	if err != nil {
		return want, err
	}
	f, err := os.Open(path)
	if err != nil {
		return want, err
	}
	defer f.Close()
	v, err := WriteBlob(root, key, f, want.Size+1)
	if err != nil {
		return v, err
	}
	if v.Size != want.Size || v.Digest != want.Digest {
		os.Remove(filepath.Join(root, v.Blob))
		return v, fmt.Errorf("bytes differ from version metadata")
	}
	return v, nil
}

// swapBlob points every version naming old at the sealed copy v and retires old. If a purge
// removed every reference first, the copy is discarded instead. After a failed commit the
// copy stays: an orphan costs disk space, a wrong guess would cost a version its bytes.
func (s *Store) swapBlob(ctx context.Context, root, old string, v Version) error {
	swapped := false
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE drive_versions SET blob=?,sealed=1 WHERE blob=?`, v.Blob, old)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			return err
		}
		swapped = true
		_, err = tx.ExecContext(ctx, `INSERT INTO drive_blob_retire(blob) VALUES(?)`, old)
		return err
	})
	if err == nil && !swapped {
		os.Remove(filepath.Join(root, v.Blob))
	}
	return err
}

// retireBlobs removes swapped-out blobs. A retired ID that is somehow referenced again is
// kept on disk; only the retire row goes.
func (s *Store) retireBlobs(ctx context.Context, root string) error {
	ids, err := column(ctx, s.db, `SELECT blob FROM drive_blob_retire`)
	if err != nil {
		return err
	}
	for _, id := range ids {
		var n int
		if err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_versions WHERE blob=?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if err = RemoveBlobs(root, []string{id}); err != nil {
				return err
			}
		}
		if _, err = s.db.ExecContext(ctx, `DELETE FROM drive_blob_retire WHERE blob=?`, id); err != nil {
			return err
		}
	}
	return nil
}
