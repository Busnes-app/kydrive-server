package drive

import (
	"context"
	"database/sql"
)

func PurgeFileUnauthorized(ctx context.Context, s *Store, id string) ([]string, error) {
	var blobs []string
	err := s.transaction(ctx, func(tx *sql.Tx) (err error) {
		blobs, err = purgeFile(ctx, tx, id, 0)
		return
	})
	return blobs, err
}

func ExecSQL(ctx context.Context, s *Store, q string, args ...any) error {
	_, err := s.db.ExecContext(ctx, q, args...)
	return err
}
