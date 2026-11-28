package drive

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

func vaultDocumentScope(ctx context.Context, q queryer, user, id string, editable bool) (vault.Record, vault.Head, error) {
	org, err := vaultPrincipal(ctx, q, user)
	if err != nil {
		return vault.Record{}, vault.Head{}, err
	}
	head, state, err := vaultHead(ctx, q, org, id)
	if err != nil {
		return vault.Record{}, vault.Head{}, err
	}
	member, found := state.State.Member(user)
	if !found {
		return vault.Record{}, vault.Head{}, vault.ErrMissing
	}
	if editable && member.Role == vault.Reader {
		return vault.Record{}, vault.Head{}, vault.ErrDenied
	}
	return state, head, nil
}

func vaultDocumentSigner(ctx context.Context, q queryer, user string, doc vault.Document, cp vault.Checkpoint, witness *hybridsig.PublicKey, clock func() time.Time) (*hybridsig.PublicKey, error) {
	record, head, err := vaultDocumentScope(ctx, q, user, doc.Vault, true)
	if err != nil {
		return nil, err
	}
	if doc.Signer != user || doc.Org != record.Org {
		return nil, vault.ErrDenied
	}
	if doc.Generation != record.State.Generation || doc.Head != head || vault.VerifyCheckpoint(witness, cp, head, clock()) != nil {
		return nil, vault.ErrConflict
	}
	key, err := vaultUserKey(ctx, q, record.Org, user)
	if err != nil {
		return nil, err
	}
	return hybridsig.ParsePublicKey(key.Signing)
}

func vaultLatest(ctx context.Context, q queryer, org, id, file string) (int64, error) {
	var revision int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(revision),0) FROM drive_vault_versions WHERE org_id=? AND vault_id=? AND file_id=?`, org, id, file).Scan(&revision)
	return revision, err
}

func vaultRevisionReady(ctx context.Context, q queryer, r vault.Revision) error {
	current, err := vaultLatest(ctx, q, r.Org, r.Vault, r.File)
	if err != nil {
		return err
	}
	if current != r.Number-1 {
		return vault.ErrConflict
	}
	var active string
	err = q.QueryRowContext(ctx, `SELECT binding_id FROM drive_vault_bindings WHERE org_id=? AND vault_id=? AND file_id=? AND head_hash=?`,
		r.Org, r.Vault, r.File, r.Head.Hash).Scan(&active)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if active != r.Binding {
		return vault.ErrConflict
	}
	return nil
}

// WriteVaultVersion preflights rights, streams ciphertext into the normal sealed
// blob store, and checks the signed state again when publishing.
func (s *Store) WriteVaultVersion(ctx context.Context, st objects.Store, user string, r vault.Revision, cp vault.Checkpoint, witness *hybridsig.PublicKey, body io.Reader, clock func() time.Time) error {
	verify := func(q queryer) error {
		signer, err := vaultDocumentSigner(ctx, q, user, r.Document, cp, witness, clock)
		if err != nil {
			return err
		}
		return r.Verify(signer)
	}
	var alreadyStored bool
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		alreadyStored = false
		if err := verify(tx); err != nil {
			return err
		}
		var existing string
		err := tx.QueryRowContext(ctx, `SELECT manifest FROM drive_vault_versions WHERE org_id=? AND vault_id=? AND file_id=? AND revision=?`,
			r.Org, r.Vault, r.File, r.Number).Scan(&existing)
		if err == nil {
			var committed vault.Revision
			if err := vault.Decode(strings.NewReader(existing), &committed); err != nil {
				return err
			}
			alreadyStored = bytes.Equal(committed.Unsigned(), r.Unsigned())
			if alreadyStored {
				return nil
			}
			return vault.ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := vaultRevisionReady(ctx, tx, r); err != nil {
			return err
		}
		return vaultQuotaFits(ctx, tx, r.Org, r.Vault, r.Size)
	}); err != nil || alreadyStored {
		return err
	}
	const header = "age-encryption.org/v1\n"
	buffer := bufio.NewReader(body)
	if prefix, err := buffer.Peek(len(header)); err != nil || string(prefix) != header {
		return vault.ErrInvalid
	}
	keys, err := s.UserKeys(ctx, user)
	if err != nil {
		return err
	}
	blob, err := WriteBlob(st, keys, buffer, r.Size)
	if err != nil {
		return err
	}
	// Leave a failed publication for the normal orphan sweep. An ambiguous SQL
	// commit must never cause the bytes of a committed revision to be deleted.
	if blob.Digest != r.Digest || blob.Size != r.Size {
		return vault.ErrInvalid
	}
	manifest, _ := json.Marshal(r)
	return s.transaction(ctx, func(tx *sql.Tx) error {
		if err := verify(tx); err != nil {
			return err
		}
		if err := vaultRevisionReady(ctx, tx, r); err != nil {
			return err
		}
		if s.Multi() {
			if err := poolFits(ctx, tx, r.Org, blob.Size); err != nil {
				return err
			}
		}
		// Fence quota accounting and membership races even when different files are saved.
		result, err := tx.ExecContext(ctx, `UPDATE drive_vaults SET hash=hash WHERE org_id=? AND id=? AND sequence=? AND hash=?`,
			r.Org, r.Vault, r.Head.Sequence, r.Head.Hash)
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			if err != nil {
				return err
			}
			return vault.ErrConflict
		}
		if err := vaultQuotaFits(ctx, tx, r.Org, r.Vault, r.Size); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO drive_vault_versions(org_id,vault_id,file_id,revision,blob,digest,size,manifest)
VALUES(?,?,?,?,?,?,?,?)`, r.Org, r.Vault, r.File, r.Number, blob.Blob, blob.Digest, blob.Size, string(manifest))
		if uniqueViolation(err) {
			return vault.ErrConflict
		}
		if err != nil {
			return err
		}
		return event(WithOrg(ctx, r.Org), tx, user, "vault.version_published", r.File)
	})
}

func vaultQuotaFits(ctx context.Context, q queryer, org, id string, extra int64) error {
	var used int64
	if err := q.QueryRowContext(ctx, `SELECT (SELECT COALESCE(SUM(size),0) FROM drive_vault_versions WHERE org_id=?1 AND vault_id=?2)+(`+vaultCheckpointBytes+` AND b.vault_id=?2)`,
		org, id).Scan(&used); err != nil {
		return err
	}
	if extra > vault.MaxVaultBytes-used {
		return ErrQuota
	}
	return nil
}

func (s *Store) ReadVaultVersion(ctx context.Context, st objects.Store, user, id, file string, number int64) (io.ReadSeekCloser, vault.Revision, error) {
	var r vault.Revision
	var blob string
	var size int64
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		r, blob, size = vault.Revision{}, "", 0
		record, _, err := vaultDocumentScope(ctx, tx, user, id, false)
		if err != nil {
			return err
		}
		var manifest string
		err = tx.QueryRowContext(ctx, `SELECT blob,size,manifest FROM drive_vault_versions WHERE org_id=? AND vault_id=? AND file_id=?
AND (?=0 OR revision=?) ORDER BY revision DESC LIMIT 1`, record.Org, id, file, number, number).Scan(&blob, &size, &manifest)
		if errors.Is(err, sql.ErrNoRows) {
			return vault.ErrMissing
		}
		if err != nil {
			return err
		}
		return vault.Decode(strings.NewReader(manifest), &r)
	}); err != nil {
		return nil, r, err
	}
	k, err := s.UserKeys(ctx, user)
	if err != nil {
		return nil, r, err
	}
	src, err := OpenBlob(st, blob, k, true, size)
	return src, r, err
}

func (s *Store) BindVaultEditor(ctx context.Context, user string, b vault.Binding, cp vault.Checkpoint, witness *hybridsig.PublicKey, clock func() time.Time) error {
	encoded, _ := json.Marshal(b)
	return s.transaction(ctx, func(tx *sql.Tx) error {
		signer, err := vaultDocumentSigner(ctx, tx, user, b.Document, cp, witness, clock)
		if err != nil {
			return err
		}
		if err := b.Verify(signer); err != nil {
			return err
		}
		_, record, err := vaultHead(ctx, tx, b.Org, b.Vault)
		if err != nil {
			return err
		}
		if len(b.Grants) != len(record.State.Members) {
			return vault.ErrConflict
		}
		for i, member := range record.State.Members {
			if b.Grants[i].User != member.User || b.Grants[i].Role != member.Role {
				return vault.ErrConflict
			}
		}
		var existing string
		err = tx.QueryRowContext(ctx, `SELECT binding_id FROM drive_vault_bindings WHERE org_id=? AND vault_id=? AND file_id=? AND head_hash=?`,
			b.Org, b.Vault, b.File, b.Head.Hash).Scan(&existing)
		if err == nil {
			if existing == b.ID() {
				return nil
			}
			return vault.ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		current, err := vaultLatest(ctx, tx, b.Org, b.Vault, b.File)
		if err != nil {
			return err
		}
		if current != b.Source {
			return vault.ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_vault_bindings(org_id,vault_id,file_id,head_hash,binding_id,binding) VALUES(?,?,?,?,?,?)`,
			b.Org, b.Vault, b.File, b.Head.Hash, b.ID(), string(encoded)); err != nil {
			if uniqueViolation(err) {
				return vault.ErrConflict
			}
			return err
		}
		for _, room := range b.Rooms {
			if _, err := tx.ExecContext(ctx, `INSERT INTO drive_vault_rooms(room,binding_id) VALUES(?,?)`, room, b.ID()); err != nil {
				if uniqueViolation(err) {
					return vault.ErrConflict
				}
				return err
			}
		}
		for _, blob := range b.Blobs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO drive_vault_editor_blobs(blob,binding_id) VALUES(?,?)`, blob, b.ID()); err != nil {
				if uniqueViolation(err) {
					return vault.ErrConflict
				}
				return err
			}
		}
		return event(WithOrg(ctx, b.Org), tx, user, "vault.editor_bound", b.File)
	})
}

func (s *Store) VaultEditorBinding(ctx context.Context, user, id, file string) (vault.Binding, error) {
	var binding vault.Binding
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		binding = vault.Binding{}
		record, head, err := vaultDocumentScope(ctx, tx, user, id, false)
		if err != nil {
			return err
		}
		var data string
		err = tx.QueryRowContext(ctx, `SELECT binding FROM drive_vault_bindings WHERE org_id=? AND vault_id=? AND file_id=? AND head_hash=?`,
			record.Org, id, file, head.Hash).Scan(&data)
		if errors.Is(err, sql.ErrNoRows) {
			return vault.ErrMissing
		}
		if err != nil {
			return err
		}
		return vault.Decode(strings.NewReader(data), &binding)
	})
	return binding, err
}

// CheckVaultRelay is called at the forwarding boundary with claims from a
// verified short-lived relay session. It checks revocation without reading wraps.
func (s *Store) CheckVaultRelay(ctx context.Context, org, id, file, user, device, binding, room, blob string) error {
	return checkVaultRelay(ctx, s.db, org, id, file, user, device, binding, room, blob)
}

func checkVaultRelay(ctx context.Context, q queryer, org, id, file, user, device, binding, room, blob string) error {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM drive_vault_bindings b
 JOIN drive_vaults v ON v.org_id=b.org_id AND v.id=b.vault_id AND v.hash=b.head_hash
 JOIN drive_devices d ON d.org_id=b.org_id AND d.user_id=? AND d.id=?
 WHERE b.org_id=? AND b.vault_id=? AND b.file_id=? AND b.binding_id=?
 AND (EXISTS(SELECT 1 FROM drive_vault_rooms r WHERE r.binding_id=b.binding_id AND r.room=?)
   OR EXISTS(SELECT 1 FROM drive_vault_editor_blobs x WHERE x.binding_id=b.binding_id AND x.blob=?))
 AND d.revoked_at=0 AND d.last_used>? AND `+activePrincipal("d.user_id")+`)`,
		user, device, org, id, file, binding, room, blob, time.Now().Add(-deviceIdle).Unix()).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return vault.ErrDenied
	}
	return nil
}
