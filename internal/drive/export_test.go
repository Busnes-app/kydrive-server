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
