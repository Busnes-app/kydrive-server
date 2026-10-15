package drive

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
)

// ZipEntry is one member of a folder download. Directories end in "/" and have no Blob.
type ZipEntry struct {
	Path     string
	Blob     string
	Sealed   bool
	Size     int64
	Modified time.Time
}

// FolderTree lists a live folder and everything live beneath it, read in one transaction,
// with paths built only from validated names and rooted at the folder's own name.
// Access is the workspace's read grant; per-item grants (M3) must recheck each member here.
func (s *Store) FolderTree(ctx context.Context, user, folder string) (string, []ZipEntry, error) {
	var name string
	var entries []ZipEntry
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var workspace, stored string
		err := tx.QueryRowContext(ctx, `SELECT workspace,name FROM drive_folders WHERE id=? AND trashed=false`, folder).Scan(&workspace, &stored)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		if err = authorize(ctx, tx, user, workspace, 1); err != nil {
			return err
		}
		if name, err = s.names.open("drive_folders", folder, stored); err != nil {
			return err
		}
		tree, err := subtree(ctx, tx, folder, `d.trashed=false`)
		if err != nil {
			return err
		}
		type node struct{ parent, name string }
		nodes := map[string]node{}
		for _, id := range tree[1:] {
			var n node
			if err = tx.QueryRowContext(ctx, `SELECT parent,name FROM drive_folders WHERE id=?`, id).Scan(&n.parent, &stored); err != nil {
				return err
			}
			if n.name, err = s.names.open("drive_folders", id, stored); err != nil {
				return err
			}
			nodes[id] = n
		}
		paths := map[string]string{folder: name + "/"}
		var pathOf func(id string) string
		pathOf = func(id string) string {
			if p, ok := paths[id]; ok {
				return p
			}
			paths[id] = pathOf(nodes[id].parent) + nodes[id].name + "/"
			return paths[id]
		}
		for _, id := range tree {
			entries = append(entries, ZipEntry{Path: pathOf(id)})
		}
		for _, id := range tree {
			rows, err := tx.QueryContext(ctx, `SELECT f.id,f.name,v.blob,v.sealed,v.size,v.created FROM drive_files f JOIN drive_versions v ON v.file_id=f.id AND v.revision=f.revision WHERE f.parent=? AND f.workspace=? AND f.trashed=false`, id, workspace)
			if err != nil {
				return err
			}
			for rows.Next() {
				var e ZipEntry
				var file, created string
				if err = rows.Scan(&file, &stored, &e.Blob, &e.Sealed, &e.Size, &created); err == nil {
					stored, err = s.names.open("drive_files", file, stored)
				}
				if err != nil {
					rows.Close()
					return err
				}
				e.Path = paths[id] + stored
				e.Modified, _ = time.Parse(time.RFC3339Nano, created)
				entries = append(entries, e)
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return err
			}
		}
		return nil
	})
	slices.SortFunc(entries, func(a, b ZipEntry) int { return strings.Compare(a.Path, b.Path) })
	return name, entries, err
}
