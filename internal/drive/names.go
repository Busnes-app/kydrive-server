package drive

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

// names seals file and folder names. The ciphertext is bound to its table and row id;
// name_hmac carries exact, case-sensitive uniqueness in the live-name indexes.
type names struct{ enc, mac []byte }

func newNames(key []byte) (names, error) {
	if len(key) != 32 {
		return names{}, errors.New("drive: name encryption requires a 32-byte deployment key")
	}
	enc, err := hkdf.Key(sha256.New, key, nil, "kydrive:name", 32)
	if err != nil {
		return names{}, err
	}
	mac, err := hkdf.Key(sha256.New, key, nil, "kydrive:name-hmac", 32)
	return names{enc, mac}, err
}

// seal returns the stored name and name_hmac for a row of table (drive_files or drive_folders).
// Every write that sets a name must set both columns from here.
func (n names) seal(table, id, name string) (string, string, error) {
	ct, err := crypto.EncryptAESGCMAAD([]byte(name), n.enc, []byte(table+":"+id))
	h := hmac.New(sha256.New, n.mac)
	h.Write([]byte(name))
	return ct, hex.EncodeToString(h.Sum(nil)), err
}

func (n names) open(table, id, stored string) (string, error) {
	name, err := crypto.DecryptAESGCMAAD(stored, n.enc, []byte(table+":"+id))
	if err != nil {
		return "", fmt.Errorf("drive: %s %s name does not decrypt under this deployment key", table, id)
	}
	return string(name), nil
}

// encryptNames seals every plaintext name in place and moves the live-name indexes to
// name_hmac. It runs once, inside the schema transaction that adds name_hmac.
func (n names) encryptNames(ctx context.Context, tx *sql.Tx) error {
	for _, table := range []string{"drive_files", "drive_folders"} {
		rows, err := tx.QueryContext(ctx, `SELECT id,name FROM `+table)
		if err != nil {
			return err
		}
		plain := map[string]string{}
		for rows.Next() {
			var id, name string
			if err = rows.Scan(&id, &name); err != nil {
				rows.Close()
				return err
			}
			plain[id] = name
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			return err
		}
		for id, name := range plain {
			ct, mac, err := n.seal(table, id, name)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE `+table+` SET name=?,name_hmac=? WHERE id=?`, ct, mac, id); err != nil {
				return err
			}
		}
	}
	_, err := tx.ExecContext(ctx, `CREATE UNIQUE INDEX drive_live_names ON drive_files(workspace,parent,name_hmac) WHERE trashed=false;
CREATE UNIQUE INDEX drive_live_folders ON drive_folders(workspace,parent,name_hmac) WHERE trashed=false;
CREATE TABLE drive_name_scrub(done INTEGER)`)
	return err
}

// scrub rewrites the database file once after encryptNames, so freed pages and the WAL no
// longer hold the plaintext names. A crash before the drop just scrubs again.
func scrub(ctx context.Context, db *sql.DB) error {
	var pending int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='drive_name_scrub'`).Scan(&pending); err != nil || pending == 0 {
		return err
	}
	for _, q := range []string{`VACUUM`, `PRAGMA wal_checkpoint(TRUNCATE)`, `DROP TABLE drive_name_scrub`} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("drive name scrub: %w", err)
		}
	}
	return nil
}
