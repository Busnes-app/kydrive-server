package drive

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/Busnes-app/kydrive-server/internal/vault"
)

// Schema 36: each account's recovery chain. Only the newest row keeps a copy, the account
// key's secrets encrypted to that row's recovery key; the server never holds either secret.
const vaultRecoverySchema = `
CREATE TABLE drive_vault_recovery (
 org_id TEXT NOT NULL, user_id TEXT NOT NULL, sequence BIGINT NOT NULL CHECK(sequence>0),
 body TEXT NOT NULL, recovery_copy TEXT, PRIMARY KEY(org_id,user_id,sequence),
 FOREIGN KEY(org_id,user_id) REFERENCES drive_vault_user_keys(org_id,user_id) ON DELETE CASCADE
);`

// MaxRecoveryRecords bounds an account's chain, so a history read stays small.
const MaxRecoveryRecords = 256

// VaultRecovery is an account's newest recovery record and its opaque copy.
type VaultRecovery struct {
	Record vault.RecoveryRecord `json:"record"`
	Copy   []byte               `json:"copy"`
}

func vaultRecoveryHead(ctx context.Context, q queryer, org, user string) (*vault.RecoveryRecord, error) {
	var body string
	err := q.QueryRowContext(ctx, `SELECT body FROM drive_vault_recovery WHERE org_id=? AND user_id=? ORDER BY sequence DESC LIMIT 1`, org, user).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record vault.RecoveryRecord
	return &record, json.Unmarshal([]byte(body), &record)
}

// CommitVaultRecovery registers, rotates or (signed by the enrolled recovery key) replaces
// the caller's account key. A retry of the newest record succeeds unchanged.
func (s *Store) CommitVaultRecovery(ctx context.Context, user string, in VaultRecovery) error {
	if !vault.ValidRecoveryCopy(in.Copy, in.Record.Copy) {
		return vault.ErrInvalid
	}
	body, err := json.Marshal(in.Record)
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		org, err := vaultPrincipal(ctx, tx, user)
		if err != nil {
			return err
		}
		if in.Record.Org != org || in.Record.User != user {
			return vault.ErrDenied
		}
		current, err := vaultUserKey(ctx, tx, org, user)
		if err != nil {
			return err
		}
		before, err := vaultRecoveryHead(ctx, tx, org, user)
		if err != nil {
			return err
		}
		if before != nil && before.Hash() == in.Record.Hash() {
			return nil
		}
		if before != nil && before.Sequence >= MaxRecoveryRecords {
			return vault.ErrConflict
		}
		if err := vault.VerifyRecovery(before, in.Record); err != nil {
			return err
		}
		// The chain must start from, and a rotation keep, the enrolled key.
		from := in.Record.Key
		if in.Record.Change == "replace" {
			from = before.Key
		}
		if from.Fingerprint() != current.Fingerprint() {
			return vault.ErrConflict
		}
		if in.Record.Change == "replace" {
			old, _ := json.Marshal(current)
			next, _ := json.Marshal(in.Record.Key)
			result, err := tx.ExecContext(ctx, `UPDATE drive_vault_user_keys SET body=? WHERE org_id=? AND user_id=? AND body=?`, string(next), org, user, string(old))
			if err != nil {
				return err
			}
			if n, err := result.RowsAffected(); err != nil || n != 1 {
				return errors.Join(vault.ErrConflict, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE drive_vault_recovery SET recovery_copy=NULL WHERE org_id=? AND user_id=?`, org, user); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_vault_recovery(org_id,user_id,sequence,body,recovery_copy) VALUES(?,?,?,?,?)`,
			org, user, in.Record.Sequence, string(body), base64.StdEncoding.EncodeToString(in.Copy)); err != nil {
			if uniqueViolation(err) {
				return vault.ErrConflict
			}
			return err
		}
		return event(WithOrg(ctx, org), tx, user, "vault.recovery_"+in.Record.Change, user)
	})
}

// VaultRecoveryCopy returns the caller's own newest record and copy, never another account's.
func (s *Store) VaultRecoveryCopy(ctx context.Context, user string) (VaultRecovery, error) {
	var out VaultRecovery
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		out = VaultRecovery{}
		org, err := vaultPrincipal(ctx, tx, user)
		if err != nil {
			return err
		}
		var body, sealed string
		err = tx.QueryRowContext(ctx, `SELECT body,recovery_copy FROM drive_vault_recovery WHERE org_id=? AND user_id=? AND recovery_copy IS NOT NULL`, org, user).Scan(&body, &sealed)
		if errors.Is(err, sql.ErrNoRows) {
			return vault.ErrMissing
		}
		if err != nil {
			return err
		}
		if out.Copy, err = base64.StdEncoding.DecodeString(sealed); err != nil {
			return err
		}
		return json.Unmarshal([]byte(body), &out.Record)
	})
	return out, err
}

// VaultRecoveryHistory returns an account's signed chain, without copies, to accounts in its
// org: a manager's client verifies a replacement key with it before a rekey record.
func (s *Store) VaultRecoveryHistory(ctx context.Context, user, target string) ([]vault.RecoveryRecord, error) {
	var records []vault.RecoveryRecord
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		records = nil
		org, err := vaultPrincipal(ctx, tx, user)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT body FROM drive_vault_recovery WHERE org_id=? AND user_id=? ORDER BY sequence LIMIT ?`, org, target, MaxRecoveryRecords)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var body string
			var record vault.RecoveryRecord
			if err := rows.Scan(&body); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(body), &record); err != nil {
				return err
			}
			records = append(records, record)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(records) == 0 {
			return vault.ErrMissing
		}
		return nil
	})
	return records, err
}
