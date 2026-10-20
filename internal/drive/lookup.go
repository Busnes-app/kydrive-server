package drive

import (
	"cmp"
	"context"
	"database/sql"
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
	folders, err := s.queryFolders(ctx, s.db, `workspace=? AND parent=? AND name_hmac=? AND trashed=false`, workspace, parent, mac)
	if err != nil {
		return Item{}, err
	}
	if len(folders) > 0 {
		return Item{Folder: &folders[0]}, nil
	}
	files, err := s.queryFiles(ctx, s.db, `f.workspace=? AND f.parent=? AND f.name_hmac=? AND f.trashed=false`, workspace, parent, mac)
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
	folders, err := s.queryFolders(ctx, s.db, `workspace=? AND parent=? AND trashed=false`, workspace, parent)
	if err != nil {
		return nil, nil, err
	}
	files, err := s.queryFiles(ctx, s.db, `f.workspace=? AND f.parent=? AND f.trashed=false`, workspace, parent)
	return folders, files, err
}

// queryFolders and queryFiles decrypt the rows matching where (never a condition on name)
// and sort them by name.
func (s *Store) queryFolders(ctx context.Context, q rowser, where string, args ...any) ([]Folder, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,workspace,parent,name,trashed FROM drive_folders WHERE `+where, args...)
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

func (s *Store) queryFiles(ctx context.Context, q rowser, where string, args ...any) ([]File, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+fileColumns+` FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE `+where, args...)
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

// FolderPath returns the names from the workspace root down to folder id ("" = the root, no
// names), which user must be able to read. ErrNotFound when the folder or an ancestor is
// trashed, gone or in another workspace.
func (s *Store) FolderPath(ctx context.Context, user, workspace, id string) ([]string, error) {
	if err := authorizeItem(ctx, s.db, user, workspace, id, "", 1); err != nil {
		return nil, err
	}
	names := []string{}
	for depth := 0; id != ""; depth++ {
		var ws, parent, stored string
		var trashed bool
		err := s.db.QueryRowContext(ctx, `SELECT workspace,parent,name,trashed FROM drive_folders WHERE id=?`, id).Scan(&ws, &parent, &stored, &trashed)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && (trashed || ws != workspace || depth == 256)) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		name, err := s.names.open("drive_folders", id, stored)
		if err != nil {
			return nil, err
		}
		names = append(names, name)
		id = parent
	}
	slices.Reverse(names)
	return names, nil
}
