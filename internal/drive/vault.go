package drive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

// VaultKey is the public part of a member's vault identity. Devices verify its fingerprint
// independently before granting access. Enrolment is immutable within an organisation.
type VaultKey struct {
	Recipient string `json:"recipient"`
	Signing   []byte `json:"signing"`
}

func (key VaultKey) Fingerprint() string {
	data, _ := json.Marshal(key)
	return vault.Digest(data)
}

func (s *Store) EnrollVaultKey(ctx context.Context, user string, key VaultKey) error {
	if _, err := hybridsig.ParsePublicKey(key.Signing); err != nil || !vault.ValidRecipient(key.Recipient) {
		return ErrInvalid
	}
	data, err := json.Marshal(key)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		org, err := vaultPrincipal(ctx, tx, user)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO drive_vault_user_keys(org_id,user_id,body) VALUES(?,?,?) ON CONFLICT DO NOTHING`, org, user, string(data))
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if inserted == 1 {
			return event(WithOrg(ctx, org), tx, user, "vault.key_enrolled", user)
		}
		var existing string
		if err := tx.QueryRowContext(ctx, `SELECT body FROM drive_vault_user_keys WHERE org_id=? AND user_id=?`, org, user).Scan(&existing); err != nil {
			return err
		}
		if existing != string(data) {
			return ErrConflict
		}
		return nil
	})
}

func (s *Store) VaultUserKey(ctx context.Context, user, target string) (VaultKey, error) {
	var key VaultKey
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		key = VaultKey{}
		org, err := vaultPrincipal(ctx, tx, user)
		if err != nil {
			return err
		}
		key, err = vaultUserKey(ctx, tx, org, target)
		return err
	})
	return key, err
}

// vaultPrincipal takes the org from the active account; URL and payload selectors never set it.
func vaultPrincipal(ctx context.Context, q queryer, user string) (string, error) {
	var org string
	err := q.QueryRowContext(ctx, `SELECT u.org_id FROM users u WHERE u.id=? AND `+activePrincipal("u.id"), user).Scan(&org)
	if errors.Is(err, sql.ErrNoRows) {
		return "", vault.ErrDenied
	}
	return org, err
}

func vaultUserKey(ctx context.Context, q queryer, org, user string) (VaultKey, error) {
	var data []byte
	var key VaultKey
	err := q.QueryRowContext(ctx, `SELECT body FROM drive_vault_user_keys WHERE org_id=? AND user_id=?`, org, user).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return key, vault.ErrMissing
	}
	if err != nil {
		return key, err
	}
	err = json.Unmarshal(data, &key)
	return key, err
}

type VaultHistory struct {
	Head    vault.Head     `json:"head"`
	Records []vault.Record `json:"records"`
}

func vaultHead(ctx context.Context, q queryer, org, id string) (vault.Head, vault.Record, error) {
	var head vault.Head
	var record vault.Record
	var data []byte
	err := q.QueryRowContext(ctx, `SELECT v.log_id,v.sequence,v.hash,r.body FROM drive_vaults v
JOIN drive_vault_records r ON r.org_id=v.org_id AND r.vault_id=v.id AND r.sequence=v.sequence
WHERE v.org_id=? AND v.id=?`, org, id).Scan(&head.Log, &head.Sequence, &head.Hash, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return head, record, vault.ErrMissing
	}
	if err != nil {
		return head, record, err
	}
	err = json.Unmarshal(data, &record)
	return head, record, err
}

// CommitVaultRecord publishes the signed membership and all wraps with one commit. One
// append wins the compare-and-swap. Callers can retry the exact last record.
func (s *Store) CommitVaultRecord(ctx context.Context, user string, record vault.Record) (vault.Head, error) {
	var head vault.Head
	data, err := json.Marshal(record)
	if err != nil {
		return head, err
	}
	link := record.Link()
	hash := link.Hash()
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		head = vault.Head{}
		org, err := vaultPrincipal(ctx, tx, user)
		if err != nil {
			return err
		}
		if record.Org != org || record.Signer != user {
			return vault.ErrDenied
		}
		current, old, err := vaultHead(ctx, tx, org, record.Vault)
		var before *vault.Record
		if err == nil {
			before = &old
			if current.Sequence == link.Sequence && current.Hash == hash {
				head = current
				return nil
			}
			if _, ok := old.State.Member(user); !ok {
				return vault.ErrMissing
			}
		} else if !errors.Is(err, vault.ErrMissing) || record.Sequence != 1 {
			return err
		}
		key, err := vaultUserKey(ctx, tx, org, user)
		if err != nil {
			return err
		}
		signer, err := hybridsig.ParsePublicKey(key.Signing)
		if err != nil {
			return err
		}
		if err = vault.VerifyRecord(before, record, signer); err != nil {
			return err
		}
		if before == nil || record.Change == "add" {
			target := record.Target
			if before == nil {
				target = user
			}
			if targetOrg, err := vaultPrincipal(ctx, tx, target); err != nil || targetOrg != org {
				if err != nil && !errors.Is(err, vault.ErrDenied) {
					return err
				}
				return vault.ErrMissing
			}
			recipient, err := vaultUserKey(ctx, tx, org, target)
			if err != nil {
				return err
			}
			member, _ := record.State.Member(target)
			if member.Fingerprint != recipient.Fingerprint() {
				return vault.ErrConflict
			}
		}
		logID := current.Log
		var result sql.Result
		if before == nil {
			logID = hash
			result, err = tx.ExecContext(ctx, `INSERT INTO drive_vaults(org_id,id,log_id,sequence,hash) VALUES(?,?,?,?,?) ON CONFLICT DO NOTHING`, org, record.Vault, logID, link.Sequence, hash)
		} else {
			result, err = tx.ExecContext(ctx, `UPDATE drive_vaults SET sequence=?,hash=? WHERE org_id=? AND id=? AND sequence=? AND hash=?`, link.Sequence, hash, org, record.Vault, current.Sequence, current.Hash)
		}
		if err != nil {
			return err
		}
		if n, err := result.RowsAffected(); err != nil || n != 1 {
			if err != nil {
				return err
			}
			return vault.ErrConflict
		}
		var newRecipient *string
		if before == nil || before.State.Generation != record.State.Generation {
			newRecipient = &record.State.Recipient
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_vault_records(org_id,vault_id,sequence,body,new_recipient) VALUES(?,?,?,?,?)`, org, record.Vault, link.Sequence, string(data), newRecipient); err != nil {
			if uniqueViolation(err) {
				return vault.ErrConflict
			}
			return err
		}
		head = vault.Head{Log: logID, Sequence: link.Sequence, Hash: hash}
		return event(WithOrg(ctx, org), tx, user, "vault."+record.Change, record.Vault)
	})
	return head, err
}

func (s *Store) VaultRecords(ctx context.Context, user, id string, since int64) (VaultHistory, error) {
	var history VaultHistory
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		history = VaultHistory{Records: []vault.Record{}}
		org, err := vaultPrincipal(ctx, tx, user)
		if err != nil {
			return err
		}
		head, current, err := vaultHead(ctx, tx, org, id)
		if err != nil {
			return err
		}
		if _, ok := current.State.Member(user); !ok {
			return vault.ErrMissing
		}
		rows, err := tx.QueryContext(ctx, `SELECT body FROM drive_vault_records WHERE org_id=? AND vault_id=? AND sequence>? ORDER BY sequence LIMIT ?`, org, id, since, vault.MaxReceipts)
		if err != nil {
			return err
		}
		defer rows.Close()
		history.Head = head
		size := 0
		for rows.Next() {
			var data []byte
			var record vault.Record
			if err := rows.Scan(&data); err != nil {
				return err
			}
			size += len(data)
			if size > 1<<20 && len(history.Records) > 0 {
				break
			}
			if err := json.Unmarshal(data, &record); err != nil {
				return err
			}
			history.Records = append(history.Records, record)
		}
		return rows.Err()
	})
	return history, err
}

// CheckVaultAccess checks live identity and membership under the checkpoint's
// exact head. A newer committed head immediately invalidates a cached checkpoint.
func (s *Store) CheckVaultAccess(ctx context.Context, user, id string, cp vault.Checkpoint, key *hybridsig.PublicKey, edit bool, clock func() time.Time) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		org, err := vaultPrincipal(ctx, tx, user)
		if err != nil {
			return err
		}
		head, current, err := vaultHead(ctx, tx, org, id)
		if err != nil {
			return err
		}
		member, found := current.State.Member(user)
		if !found {
			return vault.ErrMissing
		}
		if edit && member.Role == vault.Reader {
			return vault.ErrDenied
		}
		if vault.VerifyCheckpoint(key, cp, head, clock()) != nil {
			return vault.ErrConflict
		}
		return nil
	})
}
