package drive

import (
	"context"
	"database/sql"
	"time"
)

func PurgeFileUnauthorized(ctx context.Context, s *Store, id string) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) (err error) {
		blobs, err = purgeFile(ctx, tx, id)
		return
	})
	return blobs, err
}

func ExecSQL(ctx context.Context, s *Store, q string, args ...any) error {
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}

// RetainFile and RetainVersion run one retention step in its own transaction, as ApplyRetention does.
func RetainFile(ctx context.Context, s *Store, id string, at time.Time) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) (err error) {
		blobs, err = retainFile(ctx, tx, id, at.UTC().Format(time.RFC3339Nano))
		return
	})
	return blobs, err
}

func RetainVersion(ctx context.Context, s *Store, id string, revision int64) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) (err error) {
		blobs, err = retainVersion(ctx, tx, id, revision)
		return
	})
	return blobs, err
}

// SetTrashAs runs the trash step with a workspace resolved earlier, as a caller holding a stale lookup would.
func SetTrashAs(ctx context.Context, s *Store, user, id, workspace string, expected int64, trash bool) error {
	return s.transaction(ctx, func(tx *sql.Tx) error { return setTrash(ctx, tx, user, id, workspace, expected, trash) })
}

// SealUntilCommit runs one legacy swap up to its commit and stops, as a crash before cleanup would.
func SealUntilCommit(ctx context.Context, s *Store, root string, key []byte, id string) error {
	v, err := s.sealCopy(ctx, root, key, id)
	if err != nil {
		return err
	}
	return s.swapBlob(ctx, root, id, v)
}

// SealCopy and SwapBlob are the two halves of one legacy swap, so tests can interleave a purge.
func SealCopy(ctx context.Context, s *Store, root string, key []byte, id string) (Version, error) {
	return s.sealCopy(ctx, root, key, id)
}

func SwapBlob(ctx context.Context, s *Store, root, old string, v Version) error {
	return s.swapBlob(ctx, root, old, v)
}

func QueryInt(ctx context.Context, s *Store, q string, args ...any) (n int, err error) {
	err = s.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return
}

// RawNames returns the stored name and name_hmac of every row in table.
func RawNames(ctx context.Context, s *Store, table string) ([]string, error) {
	return column(ctx, s.db, `SELECT name||' '||name_hmac FROM `+table)
}
