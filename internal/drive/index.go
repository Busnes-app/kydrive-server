package drive

import (
	"context"
	"errors"
)

// The content search worker's reads. They answer for the whole drive, as the retention and
// sealing workers do, and are never reachable from a request: what a user may see is decided
// when they search.

// IndexChanges returns the files named by up to limit change rows after seq after, each once,
// and the last seq read (after itself when there are none).
func (s *Store) IndexChanges(ctx context.Context, after int64, limit int) ([]string, int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT seq,kind,item FROM drive_changes WHERE seq>? ORDER BY seq LIMIT ?`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var files []string
	for rows.Next() {
		var kind, item string
		if err = rows.Scan(&after, &kind, &item); err != nil {
			return nil, after, err
		}
		if kind == "file" && !seen[item] {
			seen[item] = true
			files = append(files, item)
		}
	}
	return files, after, rows.Err()
}

// IndexFile returns file id at its current version, trashed or not; ErrNotFound when it is gone.
func (s *Store) IndexFile(ctx context.Context, id string) (File, error) {
	f, err := s.scanFile(s.db.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE f.id=?`, id))
	if errors.Is(err, ErrDenied) {
		return f, ErrNotFound
	}
	return f, err
}

// IndexFiles pages every file at its current version, trashed or not, by ID after afterID.
func (s *Store) IndexFiles(ctx context.Context, afterID string, limit int) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileColumns+` FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE f.id>? ORDER BY f.id LIMIT ?`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := s.scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
