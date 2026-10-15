package drive

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// App passwords let a user's own WebDAV client sign in with HTTP Basic. Each is 160 random
// bits shown once, stored as SHA-256 (no slow hash: the secret is not guessable), and valid
// only while its account is active.
const (
	appPasswordsPerUser = 20
	appPasswordTouch    = 5 * time.Minute
)

type AppPassword struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Created  string `json:"created"`
	LastUsed int64  `json:"last_used"`
}

var appPasswordEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// CreateAppPassword returns the new password's record and its secret, which is not stored.
func (s *Store) CreateAppPassword(ctx context.Context, user, name string) (AppPassword, string, error) {
	p := AppPassword{ID: uuid.NewString(), Name: name, Created: now()}
	if name == "" || len(name) > 100 || strings.TrimSpace(name) != name {
		return p, "", ErrInvalid
	}
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service {
		return p, "", ErrDenied
	}
	raw := make([]byte, 20)
	rand.Read(raw)
	encoded := strings.ToLower(appPasswordEncoding.EncodeToString(raw))
	var groups []string
	for i := 0; i < len(encoded); i += 4 {
		groups = append(groups, encoded[i:i+4])
	}
	secret := strings.Join(groups, "-")
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		var active, count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id=? AND status='active'`, user).Scan(&active); err != nil {
			return err
		}
		if active != 1 {
			return ErrDenied
		}
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM drive_app_passwords WHERE user_id=?`, user).Scan(&count); err != nil {
			return err
		}
		if count >= appPasswordsPerUser {
			return fmt.Errorf("%w: at most %d app passwords", ErrQuota, appPasswordsPerUser)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_app_passwords(id,user_id,name,hash,created) VALUES(?,?,?,?,?)`, p.ID, user, name, appPasswordHash(secret), p.Created); err != nil {
			return err
		}
		return event(ctx, tx, user, "app_password.created", p.ID)
	})
	return p, secret, err
}

func appPasswordHash(secret string) string {
	return tokenHash(strings.ToLower(strings.ReplaceAll(secret, "-", "")))
}

func (s *Store) AppPasswords(ctx context.Context, user string) ([]AppPassword, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,created,last_used FROM drive_app_passwords WHERE user_id=? ORDER BY created`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppPassword{}
	for rows.Next() {
		var p AppPassword
		if err = rows.Scan(&p.ID, &p.Name, &p.Created, &p.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAppPassword(ctx context.Context, user, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM drive_app_passwords WHERE id=? AND user_id=?`, id, user)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			return errors.Join(err, ErrDenied)
		}
		return event(ctx, tx, user, "app_password.revoked", id)
	})
}

// AppPasswordUser returns the ID of the active user named username (any case) that owns secret.
func (s *Store) AppPasswordUser(ctx context.Context, username, secret string) (string, error) {
	var id, user string
	var lastUsed int64
	err := s.db.QueryRowContext(ctx, `SELECT p.id,u.id,p.last_used FROM drive_app_passwords p JOIN users u ON u.id=p.user_id WHERE p.hash=? AND lower(u.username)=lower(?) AND u.status='active'`, appPasswordHash(secret), username).Scan(&id, &user, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) || secret == "" {
		return "", ErrDenied
	}
	if err != nil {
		return "", err
	}
	if t := time.Now(); t.Sub(time.Unix(lastUsed, 0)) >= appPasswordTouch {
		if _, err = s.db.ExecContext(ctx, `UPDATE drive_app_passwords SET last_used=? WHERE id=?`, t.Unix(), id); err != nil {
			return "", err
		}
	}
	return user, nil
}
