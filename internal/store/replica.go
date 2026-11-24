package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
)

// UpdateReplicaConfig checks or explicitly accepts shared settings. A row lock serializes
// first starts, upgrades and operator acceptance across independent database connections.
// The fingerprint and acceptance audit commit together. Single mode never records a row.
func (s *SQLStore) UpdateReplicaConfig(ctx context.Context, current map[string]string, accept bool) ([]string, error) {
	if !s.drive.Multi() || s.driver != "postgres" {
		return nil, errors.New("replica configuration requires multi-tenant PostgreSQL")
	}
	if !validFingerprint(current) {
		return nil, errors.New("invalid current replica fingerprint")
	}
	var changed []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		changed = nil // transactions may retry
		raw, err := json.Marshal(current)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, s.rebind(`INSERT INTO server_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`), config.ReplicaConfigSetting, string(raw), time.Now().UTC())
		if err != nil {
			return err
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return err
		}
		var existing string
		if err = tx.QueryRowContext(ctx, s.rebind(`SELECT value FROM server_settings WHERE key=? FOR UPDATE`), config.ReplicaConfigSetting).Scan(&existing); err != nil {
			return err
		}
		var stored map[string]string
		if json.Unmarshal([]byte(existing), &stored) != nil || !validFingerprint(stored) {
			return errors.New("stored replica fingerprint is unreadable; restore a valid record before accepting changes")
		}
		changed = config.DiffFingerprint(stored, current)
		if slices.Equal(changed, []string{config.EncryptionKeyField}) {
			return errors.New("KY_ENCRYPTION_KEY differs; supply the retained deployment key; accept-replica-config cannot rotate it")
		}
		for _, name := range []string{"KYDRIVE_IDENTITY_KEY_FILE", "KYDRIVE_STAFF_ORG", "KYDRIVE_TENANCY"} {
			if slices.Contains(changed, name) {
				return fmt.Errorf("%s differs; this identity is immutable and cannot be changed with accept-replica-config", name)
			}
		}
		if len(changed) > 0 && !accept {
			return fmt.Errorf("replica configuration differs: %s; after changing every replica deliberately, run accept-replica-config once", strings.Join(changed, ", "))
		}
		// Preserve unknown fields from a newer version. It will compare them on its next start.
		added := false
		for name, value := range current {
			if _, ok := stored[name]; !ok {
				added = true
			}
			stored[name] = value
		}
		if len(changed) > 0 || added {
			raw, err = json.Marshal(stored)
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, s.rebind(`UPDATE server_settings SET value=?,updated_at=? WHERE key=?`), string(raw), time.Now().UTC(), config.ReplicaConfigSetting); err != nil {
				return err
			}
		}
		if accept {
			if inserted > 0 {
				changed = slices.Sorted(maps.Keys(current))
			}
			if _, err = tx.ExecContext(ctx, s.auditSQL(), "cli", "admin.replica_config_accepted", config.ReplicaConfigSetting, "fields="+strings.Join(changed, ","), "", time.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return changed, nil
}

// A malformed key check must never turn explicit acceptance into a key rotation. Errors
// also never echo untrusted persisted field names or values.
func validFingerprint(fp map[string]string) bool {
	if fp[config.EncryptionKeyField] == "" {
		return false
	}
	for name, value := range fp {
		if name == "" {
			return false
		}
		for _, c := range name {
			if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
				return false
			}
		}
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			return false
		}
	}
	return true
}
