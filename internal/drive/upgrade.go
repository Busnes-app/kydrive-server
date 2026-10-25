package drive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

// UpgradeBlobs rewrites every KYB1 blob listed in drive_blob_kyb1 as KYB2 under the same ID,
// at most rate bytes per second. Each rewrite is staged, synced and decrypted against the
// version digest before it atomically replaces the blob and drops the queue row, so a run that
// dies anywhere leaves a readable blob and a rerun is safe. Failed blobs stay queued.
func (s *Store) UpgradeBlobs(ctx context.Context, root string, key []byte, rate int64) (int, error) {
	done, failed, after := 0, 0, ""
	last := time.Now()
	defer func() {
		left, err := s.kyb1Left(context.WithoutCancel(ctx))
		switch {
		case err != nil:
			log.Printf("blob format: %v", err)
		case done > 0 && left == 0:
			log.Printf("blob format: rewrote %d KYB1 blobs as KYB2; none remain", done)
		case done > 0 || failed > 0:
			log.Printf("blob format: rewrote %d KYB1 blobs as KYB2, %d failed, %d remain", done, failed, left)
		}
	}()
	for {
		ids, err := column(ctx, s.db, `SELECT blob FROM drive_blob_kyb1 WHERE blob>? ORDER BY blob LIMIT 100`, after)
		if err != nil || len(ids) == 0 {
			return done, err
		}
		for _, id := range ids {
			after = id
			start := time.Now()
			size, rewrote, err := s.upgradeBlob(ctx, root, key, id)
			if ctx.Err() != nil {
				return done, ctx.Err()
			}
			if err != nil {
				failed++
				log.Printf("blob format: %s: %v", id, err)
			} else if rewrote {
				done++
			}
			if time.Since(last) > 5*time.Minute {
				last = time.Now()
				log.Printf("blob format: rewrote %d KYB1 blobs so far", done)
			}
			if rate > 0 {
				pause := time.NewTimer(time.Duration(float64(SealedSize(size))/float64(rate)*float64(time.Second)) - time.Since(start))
				select {
				case <-ctx.Done():
					pause.Stop()
					return done, ctx.Err()
				case <-pause.C:
				}
			}
		}
	}
}

// kyb1Left counts blobs still awaiting UpgradeBlobs.
func (s *Store) kyb1Left(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_blob_kyb1`).Scan(&n)
	return n, err
}

func (s *Store) upgradeBlob(ctx context.Context, root string, key []byte, id string) (int64, bool, error) {
	tmp, size, err := s.stageUpgrade(ctx, root, key, id)
	if err != nil {
		return size, false, err
	}
	if tmp != "" {
		defer os.Remove(tmp)
	}
	return size, tmp != "", s.commitUpgrade(ctx, root, id, tmp)
}

// stageUpgrade writes a KYB2 copy of blob id to a synced staging file and proves it decrypts
// to the version digest. A purged blob stages nothing ("" path).
func (s *Store) stageUpgrade(ctx context.Context, root string, key []byte, id string) (string, int64, error) {
	var digest string
	var size int64
	err := s.db.QueryRowContext(ctx, `SELECT digest,size FROM drive_versions WHERE blob=? LIMIT 1`, id).Scan(&digest, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	path, err := BlobPath(root, id)
	if err != nil {
		return "", size, err
	}
	old, err := OpenBlob(path, key, true, size)
	if err != nil {
		return "", size, err
	}
	tmp, v, err := stage(root, key, blobBinding(id), old, size+1)
	old.Close()
	if err != nil {
		return "", size, err
	}
	if err = checkStaged(tmp, key, id, size, digest); err == nil && v.Digest != digest {
		err = errors.New("blob bytes differ from version metadata")
	}
	if err != nil {
		os.Remove(tmp)
		return "", size, err
	}
	return tmp, size, nil
}

func checkStaged(tmp string, key []byte, id string, size int64, digest string) error {
	r, err := openFile(tmp, key, true, size, blobBinding(id))
	if err != nil {
		return err
	}
	defer r.Close()
	h := sha256.New()
	if _, err = io.Copy(h, r); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return fmt.Errorf("staged copy of %s does not decrypt to its digest", id)
	}
	return nil
}

// commitUpgrade replaces blob id with tmp and drops its queue row in one transaction, so a
// purge, which removes the file only after its own commit, cannot interleave. A purged blob
// is not brought back.
func (s *Store) commitUpgrade(ctx context.Context, root, id, tmp string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_versions WHERE blob=?`, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			if tmp == "" {
				return errors.New("purged blob is referenced again")
			}
			path, err := BlobPath(root, id)
			if err != nil {
				return err
			}
			if err = os.Rename(tmp, path); err != nil {
				return err
			}
			if err = SyncDirectory(root); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM drive_blob_kyb1 WHERE blob=?`, id)
		return err
	})
}
