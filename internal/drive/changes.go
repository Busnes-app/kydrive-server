package drive

import (
	"context"
	"database/sql"
	"time"
)

// change records that item (kind "file" or "folder") changed in workspace. Readers return
// its current state, so a row names the item and nothing else.
func change(ctx context.Context, tx *sql.Tx, workspace, kind, item string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO drive_changes(workspace,kind,item,created) VALUES(?,?,?,?)`, workspace, kind, item, time.Now().Unix())
	return err
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
