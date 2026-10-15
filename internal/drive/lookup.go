package drive

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
)

// ErrNotFound means no live item has that name; distinct from ErrDenied, which hides existence.
var ErrNotFound = errors.New("drive item not found")

// Item is a live folder or file; exactly one is set.
type Item struct {
	Folder *Folder
	File   *File
}

// Lookup finds the live folder or file named name under parent by name_hmac, so it never
// decrypts siblings. A folder wins when both exist.
func (s *Store) Lookup(ctx context.Context, user, workspace, parent, name string) (Item, error) {
	if err := authorizeItem(ctx, s.db, user, workspace, parent, "", 1); err != nil {
		return Item{}, err
	}
	if !ValidName(name) {
		return Item{}, ErrNotFound
	}
	mac := s.names.hmac(name)
	folders, err := s.queryFolders(ctx, `workspace=? AND parent=? AND name_hmac=? AND trashed=false`, workspace, parent, mac)
	if err != nil {
		return Item{}, err
	}
	if len(folders) > 0 {
		return Item{Folder: &folders[0]}, nil
	}
	files, err := s.queryFiles(ctx, `f.workspace=? AND f.parent=? AND f.name_hmac=? AND f.trashed=false`, workspace, parent, mac)
	if err != nil || len(files) == 0 {
		return Item{}, cmp.Or(err, ErrNotFound)
	}
	return Item{File: &files[0]}, nil
}

// Children lists the live folders and files directly under parent, sorted by name.
func (s *Store) Children(ctx context.Context, user, workspace, parent string) ([]Folder, []File, error) {
	if err := authorizeItem(ctx, s.db, user, workspace, parent, "", 1); err != nil {
		return nil, nil, err
	}
	folders, err := s.queryFolders(ctx, `workspace=? AND parent=? AND trashed=false`, workspace, parent)
	if err != nil {
		return nil, nil, err
	}
	files, err := s.queryFiles(ctx, `f.workspace=? AND f.parent=? AND f.trashed=false`, workspace, parent)
	return folders, files, err
}

// queryFolders and queryFiles decrypt the rows matching where (never a condition on name)
// and sort them by name.
func (s *Store) queryFolders(ctx context.Context, where string, args ...any) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,workspace,parent,name,trashed FROM drive_folders WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Folder{}
	for rows.Next() {
		var f Folder
		if err = rows.Scan(&f.ID, &f.Workspace, &f.Parent, &f.Name, &f.Trashed); err != nil {
			return nil, err
		}
		if f.Name, err = s.names.open("drive_folders", f.ID, f.Name); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	slices.SortStableFunc(out, func(a, b Folder) int { return strings.Compare(a.Name, b.Name) })
	return out, rows.Err()
}

func (s *Store) queryFiles(ctx context.Context, where string, args ...any) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileColumns+` FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		f, err := s.scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	slices.SortStableFunc(out, func(a, b File) int { return strings.Compare(a.Name, b.Name) })
	return out, rows.Err()
}
