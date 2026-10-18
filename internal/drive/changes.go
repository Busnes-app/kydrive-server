package drive

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrGone is a change cursor below rows that were pruned; the client must re-enumerate.
var ErrGone = errors.New("change cursor expired")

// Change is an item's current state: live with File or Folder set, or Removed (trashed,
// purged or now in another workspace).
type Change struct {
	Kind    string  `json:"kind"`
	ID      string  `json:"id"`
	Removed bool    `json:"removed"`
	File    *File   `json:"file,omitempty"`
	Folder  *Folder `json:"folder,omitempty"`
}

// change records that item (kind "file" or "folder") changed in workspace. Readers return
// its current state, so a row names the item and nothing else.
func change(ctx context.Context, tx *sql.Tx, workspace, kind, item string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO drive_changes(workspace,kind,item,created) VALUES(?,?,?,?)`, workspace, kind, item, time.Now().Unix())
	return err
}

// Changes reads up to limit rows after the cursor and returns each item they name once, in
// order of first appearance, as it is now. next is the cursor for the following call; more
// reports rows beyond it.
func (s *Store) Changes(ctx context.Context, user, workspace string, after int64, limit int) (cs []Change, next int64, more bool, err error) {
	if after < 0 || limit < 1 {
		return nil, 0, false, ErrInvalid
	}
	type row struct {
		seq        int64
		kind, item string
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		cs, next = []Change{}, after
		if err := authorize(ctx, tx, user, workspace, 1); err != nil {
			return err
		}
		var floor int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM drive_change_floor WHERE workspace=?`, workspace).Scan(&floor); err != nil {
			return err
		}
		if after < floor {
			return ErrGone
		}
		rows, err := tx.QueryContext(ctx, `SELECT seq,kind,item FROM drive_changes WHERE workspace=? AND seq>? ORDER BY seq LIMIT ?`, workspace, after, limit+1)
		if err != nil {
			return err
		}
		var page []row
		for rows.Next() {
			var r row
			if err = rows.Scan(&r.seq, &r.kind, &r.item); err != nil {
				rows.Close()
				return err
			}
			page = append(page, r)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		if more = len(page) > limit; more {
			page = page[:limit]
		}
		seen := map[row]bool{}
		for _, r := range page {
			next = r.seq
			key := row{kind: r.kind, item: r.item}
			if seen[key] {
				continue
			}
			seen[key] = true
			c := Change{Kind: r.kind, ID: r.item}
			if r.kind == "file" {
				files, err := s.queryFiles(ctx, tx, `f.id=? AND f.workspace=? AND f.trashed=false`, r.item, workspace)
				if err != nil {
					return err
				}
				if len(files) == 1 {
					c.File = &files[0]
				}
			} else {
				folders, err := s.queryFolders(ctx, tx, `id=? AND workspace=? AND trashed=false`, r.item, workspace)
				if err != nil {
					return err
				}
				if len(folders) == 1 {
					c.Folder = &folders[0]
				}
			}
			c.Removed = c.File == nil && c.Folder == nil
			cs = append(cs, c)
		}
		return nil
	})
	if err != nil {
		return nil, 0, false, err
	}
	return cs, next, more, nil
}

// Snapshot pages a workspace's live folders and files by ID after afterID. head is the
// workspace's change cursor in the same transaction: a client that follows Changes from the
// first page's head misses nothing written while it paged. nextID is "" on the last page.
func (s *Store) Snapshot(ctx context.Context, user, workspace, afterID string, limit int) (folders []Folder, files []File, head int64, nextID string, err error) {
	if limit < 1 {
		return nil, nil, 0, "", ErrInvalid
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		folders, files, nextID = []Folder{}, []File{}, ""
		if err := authorize(ctx, tx, user, workspace, 1); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq),0) FROM drive_changes WHERE workspace=?`, workspace).Scan(&head); err != nil {
			return err
		}
		ids, err := column(ctx, tx, `SELECT id FROM (SELECT id FROM drive_folders WHERE workspace=? AND trashed=false UNION ALL SELECT id FROM drive_files WHERE workspace=? AND trashed=false) WHERE id>? ORDER BY id LIMIT ?`, workspace, workspace, afterID, limit+1)
		if err != nil || len(ids) == 0 {
			return err
		}
		if len(ids) > limit {
			ids = ids[:limit]
			nextID = ids[limit-1]
		}
		last := ids[len(ids)-1]
		if folders, err = s.queryFolders(ctx, tx, `workspace=? AND trashed=false AND id>? AND id<=?`, workspace, afterID, last); err != nil {
			return err
		}
		files, err = s.queryFiles(ctx, tx, `f.workspace=? AND f.trashed=false AND f.id>? AND f.id<=?`, workspace, afterID, last)
		return err
	})
	if err != nil {
		return nil, nil, 0, "", err
	}
	return folders, files, head, nextID, nil
}

// PruneChanges deletes rows created before before, except each workspace's newest, so an
// idle workspace's cursor stays valid. It raises each workspace's floor to the highest seq
// it deleted there.
func (s *Store) PruneChanges(ctx context.Context, before time.Time) error {
	const old = `created<? AND seq NOT IN (SELECT MAX(seq) FROM drive_changes GROUP BY workspace)`
	return s.transaction(ctx, func(tx *sql.Tx) error {
		cutoff := before.Unix()
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_change_floor(workspace,seq) SELECT workspace,MAX(seq) FROM drive_changes WHERE `+old+` GROUP BY workspace ON CONFLICT(workspace) DO UPDATE SET seq=MAX(seq,excluded.seq)`, cutoff); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM drive_changes WHERE `+old, cutoff)
		return err
	})
}
