package drive

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/vault"
	"github.com/google/uuid"
)

const liveCheckpointCharge = `(checkpoint_digest IS NOT NULL OR checkpoint_until>unixepoch())`
const vaultCheckpointBytes = `SELECT COALESCE(SUM(x.checkpoint_size),0)
FROM drive_vault_editor_blobs x JOIN drive_vault_bindings b USING(binding_id)
WHERE b.org_id=?1 AND ` + liveCheckpointCharge

// VaultRelayGrant is the scope of a MAC-verified grant. Only the relay API supplies one.
type VaultRelayGrant struct {
	Org, Vault, File, User, Device, Binding string
	Expires                                 int64
	Edit                                    bool
}

func (g VaultRelayGrant) checkCheckpoint(ctx context.Context, q queryer, blob string, edit bool, clock func() time.Time) error {
	if clock().UnixMilli() >= g.Expires || edit && !g.Edit {
		return vault.ErrDenied
	}
	return checkVaultRelay(ctx, q, g.Org, g.Vault, g.File, g.User, g.Device, g.Binding, "", blob)
}

// WriteVaultCheckpoint charges a durable reservation before reading the body. The blob
// remains private until its metadata commits. The normal orphan sweep handles any
// object left behind by an interrupted or ambiguous publication.
func (s *Store) WriteVaultCheckpoint(ctx context.Context, st objects.Store, grant VaultRelayGrant, blob string, size int64, body io.Reader, clock func() time.Time) error {
	if !vault.ValidEditorBlob(blob) || size < 1 || size > vault.MaxDocumentBytes {
		return vault.ErrInvalid
	}
	object := uuid.NewString()
	var keys Keys
	var staged, committed bool
	// A request can lose its SQL commit reply. Clear only our own unfinished
	// reservation even in that case, and let its deadline cover a process crash.
	defer func() {
		if committed {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		result, err := s.db.ExecContext(cleanup, `UPDATE drive_vault_editor_blobs
SET checkpoint_object=NULL,checkpoint_size=0,checkpoint_until=0
WHERE blob=? AND checkpoint_object=? AND checkpoint_digest IS NULL`, blob, object)
		if err == nil && staged {
			if n, err := result.RowsAffected(); err == nil && n == 1 {
				_ = objects.Scope(st, keys.org).Remove(cleanup, object)
			}
		}
	}()
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := grant.checkCheckpoint(ctx, tx, blob, true, clock); err != nil {
			return err
		}
		var taken bool
		if err := tx.QueryRowContext(ctx, `SELECT `+liveCheckpointCharge+` FROM drive_vault_editor_blobs WHERE blob=?`, blob).Scan(&taken); err != nil {
			return err
		}
		if taken {
			return vault.ErrConflict
		}
		if err := vaultQuotaFits(ctx, tx, grant.Org, grant.Vault, size); err != nil {
			return err
		}
		if s.Multi() {
			if err := poolFits(ctx, tx, grant.Org, size); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE drive_vault_editor_blobs
SET checkpoint_object=?,checkpoint_size=?,checkpoint_until=unixepoch()+60 WHERE blob=?`, object, size, blob)
		return err
	}); err != nil {
		return err
	}

	var err error
	keys, err = s.UserKeys(ctx, grant.User)
	if err != nil {
		return err
	}
	v, err := writeObject(st, keys, object, blobBinding(object), body, size)
	if err != nil {
		return err
	}
	staged = true
	if v.Size != size {
		return vault.ErrInvalid
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := grant.checkCheckpoint(ctx, tx, blob, true, clock); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE drive_vault_editor_blobs SET checkpoint_digest=?,checkpoint_until=0
WHERE blob=? AND checkpoint_object=? AND checkpoint_until>unixepoch()`, v.Digest, blob, object)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n != 1 {
			return vault.ErrConflict
		}
		return err
	})
	committed = err == nil
	return err
}

func (s *Store) ReadVaultCheckpoint(ctx context.Context, st objects.Store, grant VaultRelayGrant, blob string, clock func() time.Time) (io.ReadSeekCloser, error) {
	var object string
	var size int64
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := grant.checkCheckpoint(ctx, tx, blob, false, clock); err != nil {
			return err
		}
		err := tx.QueryRowContext(ctx, `SELECT checkpoint_object,checkpoint_size
FROM drive_vault_editor_blobs WHERE blob=? AND checkpoint_digest IS NOT NULL`, blob).Scan(&object, &size)
		if errors.Is(err, sql.ErrNoRows) {
			return vault.ErrMissing
		}
		return err
	}); err != nil {
		return nil, err
	}
	keys, err := s.UserKeys(ctx, grant.User)
	if err != nil {
		return nil, err
	}
	return OpenBlob(st, object, keys, true, size)
}
