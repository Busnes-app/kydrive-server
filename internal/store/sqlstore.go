package store

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/drive"

	"github.com/Busnes-app/kydrive-server/internal/store/migrations"
)

// SQLStore implements Store on top of database/sql.
type SQLStore struct {
	db       *sql.DB
	drive    *drive.Store
	driver   string
	users    *userStore
	sessions *sessionStore
	groups   *groupStore
	audit    *auditStore
	settings *settingsStore
}

// newSQLStore creates and initializes a SQLStore, running migrations automatically.
func newSQLStore(ctx context.Context, db *sql.DB, driver string, key []byte) (*SQLStore, error) {
	driver = strings.ToLower(driver)
	if driver == "postgresql" {
		driver = "postgres"
	}

	if err := migrations.Run(ctx, db, driver); err != nil {
		return nil, fmt.Errorf("migration failure on driver %s: %w", driver, err)
	}

	s := &SQLStore{
		db:     db,
		driver: driver,
	}

	s.users = &userStore{store: s}
	s.sessions = &sessionStore{store: s}
	s.groups = &groupStore{store: s}
	s.audit = &auditStore{store: s}
	s.settings = &settingsStore{store: s}

	if driver == "sqlite" {
		var err error
		s.drive, err = drive.New(ctx, db, key)
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *SQLStore) Drive() *drive.Store     { return s.drive }
func (s *SQLStore) Users() UserStore        { return s.users }
func (s *SQLStore) Sessions() SessionStore  { return s.sessions }
func (s *SQLStore) Groups() GroupStore      { return s.groups }
func (s *SQLStore) Audit() AuditStore       { return s.audit }
func (s *SQLStore) Settings() SettingsStore { return s.settings }

func (s *SQLStore) Driver() string                 { return s.driver }
func (s *SQLStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *SQLStore) Close() error                   { return s.db.Close() }

// ResetAfterRestore ends restored access grants in one transaction. Passwords, MFA secrets,
// directory membership and content remain as backed up; operators reconcile those separately.
func (s *SQLStore) ResetAfterRestore(ctx context.Context) error {
	if s.drive == nil {
		return errors.New("restore reset requires a SQLite drive")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{
		"sessions", "mfa_challenges", "drive_app_passwords", "drive_devices", "drive_device_codes",
		"drive_service_tokens", "drive_link_users", "drive_links", "drive_editor_saves",
		"drive_editor_documents", "drive_editor_sessions", "drive_upload_segments", "drive_upload_sessions",
	} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return err
		}
	}
	// A new opener must not reuse a document key held by an editor from before restoration.
	// A random positive 56-bit epoch also separates repeated restores of the same capsule.
	epoch, err := strconv.ParseInt(crypto.RandomHex(7), 16, 64)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE drive_files SET editor_epoch=?`, epoch+1); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_records(user_id,action,resource,details,ip_address,created_at) VALUES(?,?,?,?,?,?)`,
		"system", "system.restore_reset", "store", "sessions, MFA challenges, DAV and agent passwords, desktop devices, service tokens, sharing links, editor sessions and unfinished uploads revoked", "", time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// rebind converts '?' placeholders to '$1, $2, ...' for Postgres
func (s *SQLStore) rebind(query string) string {
	if s.driver != "postgres" {
		return query
	}

	var b strings.Builder
	b.Grow(len(query) + 16)
	paramIdx := 1

	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(paramIdx))
			paramIdx++
		} else {
			b.WriteByte(query[i])
		}
	}
	return b.String()
}

// ---------------------------------------------------------------------
// User Store
// ---------------------------------------------------------------------

type userStore struct {
	store *SQLStore
}

// SetDirectoryRole is an explicit operator grant, never inferred from a directory role.
func (u *userStore) SetDirectoryRole(ctx context.Context, subject, role string) error {
	if subject == "" || len(subject) > 512 || strings.TrimSpace(subject) != subject || (role != "admin" && role != "user") {
		return fmt.Errorf("exact directory subject and role admin or user required")
	}
	tx, err := u.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRowContext(ctx, u.store.rebind(`SELECT COUNT(*) FROM users WHERE sso_subject=? AND sso_provider IN ('scim','kysignon') AND status='active'`), subject).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("expected exactly one active directory account; found %d", count)
	}
	var id, oldRole string
	if err = tx.QueryRowContext(ctx, u.store.rebind(`SELECT id,role FROM users WHERE sso_subject=? AND sso_provider IN ('scim','kysignon') AND status='active'`), subject).Scan(&id, &oldRole); err != nil {
		return err
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, u.store.rebind(`UPDATE users SET role=?,updated_at=? WHERE id=?`), role, now, id); err != nil {
		return err
	}
	if err = u.store.revokeGrants(ctx, tx, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, u.store.rebind(`INSERT INTO audit_records(user_id,action,resource,details,ip_address,created_at) VALUES(?,?,?,?,?,?)`), id, "auth.directory_role_changed", id, oldRole+" -> "+role+"; sessions revoked", "operator-cli", now); err != nil {
		return err
	}
	return tx.Commit()
}

// RenameLocalAdmin frees a directory username without linking or elevating identities.
func (u *userStore) RenameLocalAdmin(ctx context.Context, oldName, newName string) error {
	tx, err := u.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, u.store.rebind(`SELECT EXISTS(SELECT 1 FROM users WHERE LOWER(username)=LOWER(?))`), newName).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrAlreadyExists
	}
	var id string
	if err = tx.QueryRowContext(ctx, u.store.rebind(`SELECT id FROM users WHERE LOWER(username)=LOWER(?) AND sso_provider='local' AND role='admin'`), oldName).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	now := time.Now().UTC()
	if _, err = tx.ExecContext(ctx, u.store.rebind(`UPDATE users SET username=?, updated_at=? WHERE id=? AND sso_provider='local' AND role='admin'`), newName, now, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, u.store.rebind(`INSERT INTO audit_records(user_id,action,resource,details,ip_address,created_at) VALUES(?,?,?,?,?,?)`), id, "auth.local_admin_renamed", id, oldName+" -> "+newName, "operator-cli", now); err != nil {
		return err
	}
	return tx.Commit()
}

func (u *userStore) CreateUser(ctx context.Context, user *User) error {
	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if user.UpdatedAt.IsZero() {
		user.UpdatedAt = now
	}
	if user.RecoveryCodesHash == "" {
		user.RecoveryCodesHash = "[]"
	}

	q := u.store.rebind(`
INSERT INTO users (
    id, username, email, display_name, password_hash, role, status,
    sso_provider, sso_subject, totp_secret_enc, totp_enabled,
    recovery_codes_hash, must_change_password,
    created_at, updated_at, last_login_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`)

	var lastLogin sql.NullTime
	if user.LastLoginAt != nil {
		lastLogin = sql.NullTime{Time: *user.LastLoginAt, Valid: true}
	}

	_, err := u.store.db.ExecContext(ctx, q,
		user.ID, user.Username, user.Email, user.DisplayName, user.PasswordHash,
		user.Role, user.Status, user.SSOProvider, user.SSOSubject,
		user.TOTPSecretEnc, user.TOTPEnabled, user.RecoveryCodesHash,
		user.MustChangePassword,
		user.CreatedAt, user.UpdatedAt, lastLogin,
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate key") {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (u *userStore) scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var user User
	var lastLogin sql.NullTime

	err := row.Scan(
		&user.ID, &user.Username, &user.Email, &user.DisplayName, &user.PasswordHash,
		&user.Role, &user.Status, &user.SSOProvider, &user.SSOSubject,
		&user.TOTPSecretEnc, &user.TOTPEnabled, &user.RecoveryCodesHash,
		&user.MustChangePassword, &user.TOTPLastCounter,
		&user.CreatedAt, &user.UpdatedAt, &lastLogin,
	)
	if err != nil {
		if errorsIs(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if lastLogin.Valid {
		user.LastLoginAt = &lastLogin.Time
	}
	return &user, nil
}

func (u *userStore) GetUserByID(ctx context.Context, id string) (*User, error) {
	q := u.store.rebind(`
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, totp_secret_enc, totp_enabled,
       recovery_codes_hash, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at
FROM users WHERE id = ?
`)
	return u.scanUser(u.store.db.QueryRowContext(ctx, q, id))
}

func (u *userStore) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	q := u.store.rebind(`
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, totp_secret_enc, totp_enabled,
       recovery_codes_hash, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at
FROM users WHERE LOWER(username) = LOWER(?)
`)
	return u.scanUser(u.store.db.QueryRowContext(ctx, q, username))
}

func (u *userStore) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	q := u.store.rebind(`
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, totp_secret_enc, totp_enabled,
       recovery_codes_hash, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at
FROM users WHERE LOWER(email) = LOWER(?)
`)
	return u.scanUser(u.store.db.QueryRowContext(ctx, q, email))
}

func (u *userStore) GetUserBySSO(ctx context.Context, provider, subject string) (*User, error) {
	q := u.store.rebind(`
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, totp_secret_enc, totp_enabled,
       recovery_codes_hash, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at
FROM users WHERE sso_provider = ? AND sso_subject = ?
`)
	return u.scanUser(u.store.db.QueryRowContext(ctx, q, provider, subject))
}

func (u *userStore) UpdateUser(ctx context.Context, user *User) error {
	user.UpdatedAt = time.Now().UTC()
	var lastLogin sql.NullTime
	if user.LastLoginAt != nil {
		lastLogin = sql.NullTime{Time: *user.LastLoginAt, Valid: true}
	}

	q := u.store.rebind(`
UPDATE users SET
    username = ?, email = ?, display_name = ?, password_hash = ?,
    role = ?, status = ?, sso_provider = ?, sso_subject = ?,
    totp_secret_enc = ?, totp_enabled = ?, recovery_codes_hash = ?,
    must_change_password = ?, updated_at = ?,
    last_login_at = ?
WHERE id = ?
`)

	res, err := u.store.db.ExecContext(ctx, q,
		user.Username, user.Email, user.DisplayName, user.PasswordHash,
		user.Role, user.Status, user.SSOProvider, user.SSOSubject,
		user.TOTPSecretEnc, user.TOTPEnabled, user.RecoveryCodesHash,
		user.MustChangePassword, user.UpdatedAt,
		lastLogin, user.ID,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

var directoryUsername = regexp.MustCompile(`^[A-Za-z0-9._@+\-]{1,128}$`)

func (u *userStore) ApplyDirectoryUser(ctx context.Context, d *DirectoryUser) (*User, error) {
	// Disabling an existing account changes only its status, so it works whatever its username.
	if (d.ID == "" || d.Active) && !directoryUsername.MatchString(d.Username) {
		return nil, ErrInvalidUsername
	}
	status := "inactive"
	if d.Active {
		status = "active"
	}
	now := time.Now().UTC()
	tx, err := u.store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	id := d.ID
	if id == "" {
		if d.Provider != "scim" && d.Provider != "kysignon" {
			return nil, fmt.Errorf("unknown directory provider %q", d.Provider)
		}
		id = "usr_" + crypto.RandomHex(12)
		_, err = tx.ExecContext(ctx, u.store.rebind(`INSERT INTO users (id, username, email, display_name, role, status, sso_provider, sso_subject, recovery_codes_hash, directory_ts, created_at, updated_at) VALUES (?, ?, ?, ?, 'user', ?, ?, ?, '[]', ?, ?, ?)`),
			id, d.Username, d.Email, d.DisplayName, status, d.Provider, d.Subject, d.EventTime, now, now)
	} else {
		set, args := "username = ?, email = ?, display_name = ?, status = ?", []any{d.Username, d.Email, d.DisplayName, status}
		if !d.Active {
			set, args = "status = ?", []any{status}
		}
		set, args = set+", updated_at = ?", append(args, now)
		if d.EventTime != 0 {
			set, args = set+", directory_ts = ?", append(args, d.EventTime)
		}
		q := "UPDATE users SET " + set + " WHERE id = ? AND sso_provider IN ('scim', 'kysignon')"
		args = append(args, id)
		if d.EventTime != 0 {
			q, args = q+" AND directory_ts "+newerThan(d.Active)+" ?", append(args, d.EventTime)
		}
		var res sql.Result
		if res, err = tx.ExecContext(ctx, u.store.rebind(q), args...); err == nil {
			err = u.directoryRowMissing(ctx, tx, res, id, d.EventTime)
		}
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate key") {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	if !d.Active {
		if err := u.store.revokeGrants(ctx, tx, id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return u.GetUserByID(ctx, id)
}

func (u *userStore) DeleteDirectoryUser(ctx context.Context, id string, eventTime int64) error {
	tx, err := u.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q, args := `DELETE FROM users WHERE id = ? AND sso_provider IN ('scim', 'kysignon')`, []any{id}
	if eventTime != 0 {
		q, args = q+" AND directory_ts "+newerThan(false)+" ?", append(args, eventTime)
	}
	res, err := tx.ExecContext(ctx, u.store.rebind(q), args...)
	if err != nil {
		return err
	}
	if err := u.directoryRowMissing(ctx, tx, res, id, eventTime); err != nil {
		return err
	}
	if err := u.store.drive.RevokeUserEditors(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// newerThan compares an event's timestamp to the account's last one. Same-second events cannot
// be ordered, so a tie goes to the restrictive event: disabling or deleting applies, enabling does not.
func newerThan(enables bool) string {
	if enables {
		return "<"
	}
	return "<="
}

// directoryRowMissing explains a directory write that touched no row: stale or absent.
func (u *userStore) directoryRowMissing(ctx context.Context, tx *sql.Tx, res sql.Result, id string, eventTime int64) error {
	n, err := res.RowsAffected()
	if err != nil || n == 1 {
		return err
	}
	var exists int
	if err := tx.QueryRowContext(ctx, u.store.rebind(`SELECT COUNT(1) FROM users WHERE id = ? AND sso_provider IN ('scim', 'kysignon')`), id).Scan(&exists); err != nil {
		return err
	}
	if exists == 1 && eventTime != 0 {
		return ErrStale
	}
	return ErrNotFound
}

func (u *userStore) UpdateRecoveryCodes(ctx context.Context, userID, oldHashes, newHashes string) error {
	q := u.store.rebind("UPDATE users SET recovery_codes_hash = ?, updated_at = ? WHERE id = ? AND recovery_codes_hash = ?")
	res, err := u.store.db.ExecContext(ctx, q, newHashes, time.Now().UTC(), userID, oldHashes)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrAlreadyExists
	}
	return nil
}

func (u *userStore) OpenInNewTab(ctx context.Context, userID string) (bool, error) {
	var v bool
	err := u.store.db.QueryRowContext(ctx, u.store.rebind("SELECT open_in_new_tab FROM users WHERE id = ?"), userID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return v, err
}

func (u *userStore) SetOpenInNewTab(ctx context.Context, userID string, v bool) error {
	res, err := u.store.db.ExecContext(ctx, u.store.rebind("UPDATE users SET open_in_new_tab = ? WHERE id = ?"), v, userID)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return ErrNotFound
	}
	return nil
}

func (u *userStore) SpendTOTPCounter(ctx context.Context, userID string, counter int64) error {
	q := u.store.rebind("UPDATE users SET totp_last_counter = ?, updated_at = ? WHERE id = ? AND totp_last_counter < ?")
	res, err := u.store.db.ExecContext(ctx, q, counter, time.Now().UTC(), userID, counter)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return ErrAlreadyExists
	}
	return nil
}

func (u *userStore) SetTOTPSecret(ctx context.Context, userID, oldEnc, newEnc string, enabled bool) error {
	q := u.store.rebind("UPDATE users SET totp_secret_enc = ?, updated_at = ? WHERE id = ? AND totp_secret_enc = ? AND totp_enabled = ?")
	res, err := u.store.db.ExecContext(ctx, q, newEnc, time.Now().UTC(), userID, oldEnc, enabled)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return ErrAlreadyExists
	}
	return nil
}

func (u *userStore) EnableTOTP(ctx context.Context, userID, secretEnc, recoveryHashes, keepSession, ip string) error {
	return u.totpTransition(ctx, userID, keepSession, ip, "auth.mfa_enabled",
		"UPDATE users SET totp_enabled = ?, recovery_codes_hash = ?, updated_at = ? WHERE id = ? AND totp_enabled = ? AND totp_secret_enc = ? AND totp_secret_enc <> ''",
		true, recoveryHashes, time.Now().UTC(), userID, false, secretEnc)
}

func (u *userStore) DisableTOTP(ctx context.Context, userID, keepSession, ip string) error {
	return u.totpTransition(ctx, userID, keepSession, ip, "auth.mfa_disabled",
		"UPDATE users SET totp_enabled = ?, totp_secret_enc = '', recovery_codes_hash = '[]', updated_at = ? WHERE id = ? AND totp_enabled = ?",
		false, time.Now().UTC(), userID, true)
}

func (u *userStore) totpTransition(ctx context.Context, userID, keepSession, ip, action, update string, args ...any) error {
	tx, err := u.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, u.store.rebind(update), args...)
	if err != nil {
		return err
	}
	if rows, err := res.RowsAffected(); err != nil {
		return err
	} else if rows != 1 {
		return ErrAlreadyExists
	}
	if _, err = tx.ExecContext(ctx, u.store.rebind("DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?"), userID, keepSession); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, u.store.rebind("DELETE FROM mfa_challenges WHERE user_id = ?"), userID); err != nil {
		return err
	}
	if err = u.store.drive.RevokeUserEditors(ctx, tx, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, u.store.rebind(`INSERT INTO audit_records (user_id, action, resource, details, ip_address, created_at) VALUES (?, ?, ?, ?, ?, ?)`), userID, action, "user", "other sessions revoked", ip, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

func (u *userStore) DeleteUser(ctx context.Context, id string) error {
	return u.store.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, u.store.rebind("DELETE FROM users WHERE id = ?"), id)
		if err != nil {
			return err
		}
		if rows, err := res.RowsAffected(); err != nil || rows == 0 {
			return cmp.Or(err, ErrNotFound)
		}
		return u.store.drive.RevokeUserEditors(ctx, tx, id)
	})
}

func (u *userStore) ListUsers(ctx context.Context, offset, limit int, search string) ([]*User, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var countQuery, listQuery string
	var countArgs, listArgs []any

	if strings.TrimSpace(search) != "" {
		like := "%" + strings.ToLower(strings.TrimSpace(search)) + "%"
		countQuery = "SELECT COUNT(1) FROM users WHERE LOWER(username) LIKE ? OR LOWER(display_name) LIKE ? OR LOWER(email) LIKE ?"
		countArgs = []any{like, like, like}

		listQuery = `
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, totp_secret_enc, totp_enabled,
       recovery_codes_hash, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at
FROM users
WHERE LOWER(username) LIKE ? OR LOWER(display_name) LIKE ? OR LOWER(email) LIKE ?
ORDER BY created_at DESC LIMIT ? OFFSET ?`
		listArgs = []any{like, like, like, limit, offset}
	} else {
		countQuery = "SELECT COUNT(1) FROM users"
		listQuery = `
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, totp_secret_enc, totp_enabled,
       recovery_codes_hash, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at
FROM users
ORDER BY created_at DESC LIMIT ? OFFSET ?`
		listArgs = []any{limit, offset}
	}

	var total int
	err := u.store.db.QueryRowContext(ctx, u.store.rebind(countQuery), countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := u.store.db.QueryContext(ctx, u.store.rebind(listQuery), listArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []*User
	for rows.Next() {
		user, err := u.scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, user)
	}

	return users, total, rows.Err()
}

func (u *userStore) ListDirectoryUsers(ctx context.Context, offset, limit int) ([]*User, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := u.store.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM users WHERE sso_provider = 'scim'").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := u.store.db.QueryContext(ctx, u.store.rebind(`
SELECT id, username, email, display_name, password_hash, role, status,
       sso_provider, sso_subject, totp_secret_enc, totp_enabled,
       recovery_codes_hash, must_change_password,
       totp_last_counter, created_at, updated_at, last_login_at
FROM users WHERE sso_provider = 'scim'
ORDER BY created_at, id LIMIT ? OFFSET ?`), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var users []*User
	for rows.Next() {
		user, err := u.scanUser(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, user)
	}
	return users, total, rows.Err()
}

func (u *userStore) CountUsers(ctx context.Context) (int, error) {
	var count int
	err := u.store.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM users").Scan(&count)
	return count, err
}

// ---------------------------------------------------------------------
// Session Store
// ---------------------------------------------------------------------

type sessionStore struct {
	store *SQLStore
}

// withPassword serializes credential-derived grants with password replacement.
// Updating the same user row takes a write lock on both supported databases.
func (s *SQLStore) withPassword(ctx context.Context, userID, expectedHash string, apply func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, s.rebind("UPDATE users SET id = id WHERE id = ? AND password_hash = ? AND status = 'active'"), userID, expectedHash)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	if err := apply(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) inTx(ctx context.Context, apply func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := apply(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// revokeGrants ends every credential-derived grant of an account: sessions, MFA challenges
// and editor sessions.
func (s *SQLStore) revokeGrants(ctx context.Context, tx *sql.Tx, userID string) error {
	for _, table := range []string{"sessions", "mfa_challenges"} {
		if _, err := tx.ExecContext(ctx, s.rebind("DELETE FROM "+table+" WHERE user_id = ?"), userID); err != nil {
			return err
		}
	}
	return s.drive.RevokeUserEditors(ctx, tx, userID)
}

func (s *sessionStore) CreateSession(ctx context.Context, sess *Session, expectedPasswordHash string, mfaEnabled bool) error {
	return s.store.withPassword(ctx, sess.UserID, expectedPasswordHash, func(tx *sql.Tx) error {
		// withPassword holds the user row, so an MFA change either lands before this read or waits.
		var enabled bool
		if err := tx.QueryRowContext(ctx, s.store.rebind(`SELECT totp_enabled FROM users WHERE id = ?`), sess.UserID).Scan(&enabled); err != nil {
			return err
		}
		if enabled != mfaEnabled {
			return ErrNotFound
		}
		_, err := tx.ExecContext(ctx, s.store.rebind(`INSERT INTO sessions (token_hash, user_id, user_agent, ip_address, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?)`), sess.TokenHash, sess.UserID, sess.UserAgent, sess.IPAddress, sess.CreatedAt, sess.ExpiresAt)
		return err
	})
}

func (u *userStore) CompletePasswordChange(ctx context.Context, userID, oldHash, newHash, ip string) error {
	return u.store.withPassword(ctx, userID, oldHash, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		result, err := tx.ExecContext(ctx, u.store.rebind(`UPDATE users SET password_hash = ?, must_change_password = ?, updated_at = ? WHERE id = ? AND must_change_password = ? AND sso_provider = 'local'`), newHash, false, now, userID, true)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrNotFound
		}
		return u.revokePasswordGrants(ctx, tx, userID, "forced replacement; sessions revoked", ip, now)
	})
}

// ResetAdminPassword is the operator recovery path, including disabled local accounts.
func (u *userStore) ResetAdminPassword(ctx context.Context, userID, newHash string) error {
	tx, err := u.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, u.store.rebind(`UPDATE users SET password_hash = ?, must_change_password = ?, status = 'active', role = 'admin', updated_at = ? WHERE id = ? AND sso_provider = 'local'`), newHash, true, now, userID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	if err := u.revokePasswordGrants(ctx, tx, userID, "operator reset; sessions revoked", "", now); err != nil {
		return err
	}
	return tx.Commit()
}

func (u *userStore) revokePasswordGrants(ctx context.Context, tx *sql.Tx, userID, details, ip string, now time.Time) error {
	if err := u.store.revokeGrants(ctx, tx, userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, u.store.rebind(`INSERT INTO audit_records (user_id, action, resource, details, ip_address, created_at) VALUES (?, ?, ?, ?, ?, ?)`), userID, "auth.password_changed", "user", details, ip, now)
	return err
}

func (s *sessionStore) GetSession(ctx context.Context, tokenHash string) (*Session, error) {
	q := s.store.rebind(`
SELECT token_hash, user_id, user_agent, ip_address, created_at, expires_at
FROM sessions WHERE token_hash = ?
`)
	var sess Session
	err := s.store.db.QueryRowContext(ctx, q, tokenHash).Scan(
		&sess.TokenHash, &sess.UserID, &sess.UserAgent, &sess.IPAddress,
		&sess.CreatedAt, &sess.ExpiresAt,
	)
	if err != nil {
		if errorsIs(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		// The hourly sweep removes a row this delete misses.
		return nil, errors.Join(ErrSessionExpired, s.DeleteSession(ctx, tokenHash))
	}
	return &sess, nil
}

func (s *sessionStore) DeleteSession(ctx context.Context, tokenHash string) error {
	q := s.store.rebind("DELETE FROM sessions WHERE token_hash = ?")
	_, err := s.store.db.ExecContext(ctx, q, tokenHash)
	return err
}

// EndSession is sign-out: it deletes the session and revokes the account's editor sessions.
func (s *sessionStore) EndSession(ctx context.Context, tokenHash string) error {
	return s.store.inTx(ctx, func(tx *sql.Tx) error {
		var userID string
		err := tx.QueryRowContext(ctx, s.store.rebind("SELECT user_id FROM sessions WHERE token_hash = ?"), tokenHash).Scan(&userID)
		if errorsIs(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, s.store.rebind("DELETE FROM sessions WHERE token_hash = ?"), tokenHash); err != nil {
			return err
		}
		return s.store.drive.RevokeUserEditors(ctx, tx, userID)
	})
}

func (s *sessionStore) DeleteUserSessions(ctx context.Context, userID string) error {
	return s.store.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, s.store.rebind("DELETE FROM sessions WHERE user_id = ?"), userID); err != nil {
			return err
		}
		return s.store.drive.RevokeUserEditors(ctx, tx, userID)
	})
}

func (s *sessionStore) CleanExpiredSessions(ctx context.Context) error {
	q := s.store.rebind("DELETE FROM sessions WHERE expires_at < ?")
	_, err := s.store.db.ExecContext(ctx, q, time.Now().UTC())
	return err
}

func (s *sessionStore) CleanExpiredMFAChallenges(ctx context.Context) error {
	_, err := s.store.db.ExecContext(ctx, s.store.rebind("DELETE FROM mfa_challenges WHERE expires_at < ?"), time.Now().UTC())
	return err
}

func (s *sessionStore) CreateMFAChallenge(ctx context.Context, challenge *MFAChallenge, expectedPasswordHash string) error {
	return s.store.withPassword(ctx, challenge.UserID, expectedPasswordHash, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, s.store.rebind("INSERT INTO mfa_challenges (token_hash, user_id, expires_at, password_hash) VALUES (?, ?, ?, ?)"), challenge.TokenHash, challenge.UserID, challenge.ExpiresAt, expectedPasswordHash)
		return err
	})
}

func (s *sessionStore) ConsumeMFAChallenge(ctx context.Context, tokenHash string) (string, string, error) {
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	q := s.store.rebind("SELECT user_id, expires_at, password_hash FROM mfa_challenges WHERE token_hash = ?")
	var userID, passwordHash string
	var expiresAt time.Time
	if err := tx.QueryRowContext(ctx, q, tokenHash).Scan(&userID, &expiresAt, &passwordHash); err != nil {
		if errorsIs(err, sql.ErrNoRows) {
			return "", "", ErrNotFound
		}
		return "", "", err
	}
	if !time.Now().UTC().Before(expiresAt) {
		return "", "", ErrSessionExpired
	}
	deleteQ := s.store.rebind("DELETE FROM mfa_challenges WHERE token_hash = ?")
	res, err := tx.ExecContext(ctx, deleteQ, tokenHash)
	if err != nil {
		return "", "", err
	}
	rows, err := res.RowsAffected()
	if err != nil || rows != 1 {
		return "", "", ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return "", "", err
	}
	return userID, passwordHash, nil
}

// ---------------------------------------------------------------------
// Group Store
// ---------------------------------------------------------------------

type groupStore struct {
	store *SQLStore
}

func (g *groupStore) CreateGroup(ctx context.Context, group *Group) error {
	now := time.Now().UTC()
	if group.CreatedAt.IsZero() {
		group.CreatedAt = now
	}
	if group.UpdatedAt.IsZero() {
		group.UpdatedAt = now
	}

	q := g.store.rebind(`
INSERT INTO groups (id, display_name, external_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
`)
	_, err := g.store.db.ExecContext(ctx, q, group.ID, group.DisplayName, group.ExternalID, group.CreatedAt, group.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate key") {
			return ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (g *groupStore) GetGroupByID(ctx context.Context, id string) (*Group, error) {
	q := g.store.rebind("SELECT id, display_name, external_id, created_at, updated_at FROM groups WHERE id = ?")
	var grp Group
	err := g.store.db.QueryRowContext(ctx, q, id).Scan(&grp.ID, &grp.DisplayName, &grp.ExternalID, &grp.CreatedAt, &grp.UpdatedAt)
	if err != nil {
		if errorsIs(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	members, err := g.getMembers(ctx, grp.ID)
	if err != nil {
		return nil, err
	}
	grp.Members = members
	return &grp, nil
}

func (g *groupStore) GetGroupByName(ctx context.Context, name string) (*Group, error) {
	q := g.store.rebind("SELECT id, display_name, external_id, created_at, updated_at FROM groups WHERE LOWER(display_name) = LOWER(?)")
	var grp Group
	err := g.store.db.QueryRowContext(ctx, q, name).Scan(&grp.ID, &grp.DisplayName, &grp.ExternalID, &grp.CreatedAt, &grp.UpdatedAt)
	if err != nil {
		if errorsIs(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	members, err := g.getMembers(ctx, grp.ID)
	if err != nil {
		return nil, err
	}
	grp.Members = members
	return &grp, nil
}

func (g *groupStore) getMembers(ctx context.Context, groupID string) ([]string, error) {
	q := g.store.rebind("SELECT user_id FROM group_members WHERE group_id = ?")
	rows, err := g.store.db.QueryContext(ctx, q, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		members = append(members, uid)
	}
	return members, rows.Err()
}

func (g *groupStore) UpdateGroup(ctx context.Context, group *Group) error {
	group.UpdatedAt = time.Now().UTC()
	q := g.store.rebind("UPDATE groups SET display_name = ?, external_id = ?, updated_at = ? WHERE id = ?")
	res, err := g.store.db.ExecContext(ctx, q, group.DisplayName, group.ExternalID, group.UpdatedAt, group.ID)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (g *groupStore) DeleteGroup(ctx context.Context, id string) error {
	q := g.store.rebind("DELETE FROM groups WHERE id = ?")
	res, err := g.store.db.ExecContext(ctx, q, id)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (g *groupStore) ListGroups(ctx context.Context, offset, limit int) ([]*Group, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var count int
	err := g.store.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM groups").Scan(&count)
	if err != nil {
		return nil, 0, err
	}

	q := g.store.rebind("SELECT id, display_name, external_id, created_at, updated_at FROM groups ORDER BY display_name ASC LIMIT ? OFFSET ?")
	rows, err := g.store.db.QueryContext(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var groups []*Group
	for rows.Next() {
		var grp Group
		if err := rows.Scan(&grp.ID, &grp.DisplayName, &grp.ExternalID, &grp.CreatedAt, &grp.UpdatedAt); err != nil {
			return nil, 0, err
		}
		groups = append(groups, &grp)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	for _, grp := range groups {
		if grp.Members, err = g.getMembers(ctx, grp.ID); err != nil {
			return nil, 0, err
		}
	}

	return groups, count, rows.Err()
}

func (g *groupStore) AddGroupMember(ctx context.Context, groupID, userID string) error {
	q := g.store.rebind("INSERT INTO group_members (group_id, user_id) VALUES (?, ?) ON CONFLICT DO NOTHING")
	_, err := g.store.db.ExecContext(ctx, q, groupID, userID)
	return err
}

func (g *groupStore) RemoveGroupMember(ctx context.Context, groupID, userID string) error {
	q := g.store.rebind("DELETE FROM group_members WHERE group_id = ? AND user_id = ?")
	_, err := g.store.db.ExecContext(ctx, q, groupID, userID)
	return err
}

func (g *groupStore) GetUserGroups(ctx context.Context, userID string) ([]*Group, error) {
	q := g.store.rebind(`
SELECT g.id, g.display_name, g.external_id, g.created_at, g.updated_at
FROM groups g
JOIN group_members gm ON g.id = gm.group_id
WHERE gm.user_id = ?
ORDER BY g.display_name ASC
`)
	rows, err := g.store.db.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []*Group
	for rows.Next() {
		var grp Group
		if err := rows.Scan(&grp.ID, &grp.DisplayName, &grp.ExternalID, &grp.CreatedAt, &grp.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, &grp)
	}
	return groups, rows.Err()
}

// ---------------------------------------------------------------------
// Audit Store
// ---------------------------------------------------------------------

type auditStore struct {
	store *SQLStore
}

func (a *auditStore) LogAudit(ctx context.Context, r *AuditRecord) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	q := a.store.rebind(`
INSERT INTO audit_records (user_id, action, resource, details, ip_address, created_at)
VALUES (?, ?, ?, ?, ?, ?)
`)
	_, err := a.store.db.ExecContext(ctx, q, r.UserID, r.Action, r.Resource, r.Details, r.IPAddress, r.CreatedAt)
	return err
}

func (a *auditStore) ListAuditRecords(ctx context.Context, offset, limit int) ([]*AuditRecord, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var count int
	err := a.store.db.QueryRowContext(ctx, "SELECT COUNT(1) FROM audit_records").Scan(&count)
	if err != nil {
		return nil, 0, err
	}

	q := a.store.rebind(`
SELECT id, user_id, action, resource, details, ip_address, created_at
FROM audit_records
ORDER BY created_at DESC LIMIT ? OFFSET ?
`)
	rows, err := a.store.db.QueryContext(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var records []*AuditRecord
	for rows.Next() {
		var r AuditRecord
		if err := rows.Scan(&r.ID, &r.UserID, &r.Action, &r.Resource, &r.Details, &r.IPAddress, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		records = append(records, &r)
	}
	return records, count, rows.Err()
}

// ---------------------------------------------------------------------
// Settings Store
// ---------------------------------------------------------------------

type settingsStore struct {
	store *SQLStore
}

func (s *settingsStore) GetSetting(ctx context.Context, key string) (string, error) {
	q := s.store.rebind("SELECT value FROM server_settings WHERE key = ?")
	var val string
	err := s.store.db.QueryRowContext(ctx, q, key).Scan(&val)
	if err != nil {
		if errorsIs(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return val, nil
}

func (s *settingsStore) SetSetting(ctx context.Context, key, val string) error {
	now := time.Now().UTC()
	q := s.store.rebind(`
INSERT INTO server_settings (key, value, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
`)
	_, err := s.store.db.ExecContext(ctx, q, key, val, now)
	return err
}

func (s *settingsStore) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.store.db.ExecContext(ctx, s.store.rebind(`DELETE FROM server_settings WHERE key = ?`), key)
	return err
}

func (s *settingsStore) GetAllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.store.db.QueryContext(ctx, "SELECT key, value FROM server_settings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		settings[k] = v
	}
	return settings, rows.Err()
}

func errorsIs(err, target error) bool {
	if err == nil || target == nil {
		return err == target
	}
	return err == target || strings.Contains(err.Error(), target.Error())
}

// ReplaceGroup applies directory attributes and the entire membership set atomically.
func (g *groupStore) ReplaceGroup(ctx context.Context, group *Group, create bool) error {
	tx, err := g.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if create {
		group.CreatedAt = now
		_, err = tx.ExecContext(ctx, g.store.rebind("INSERT INTO groups(id,display_name,external_id,created_at,updated_at) VALUES(?,?,?,?,?)"), group.ID, group.DisplayName, group.ExternalID, now, now)
	} else {
		var res sql.Result
		res, err = tx.ExecContext(ctx, g.store.rebind("UPDATE groups SET display_name=?,updated_at=? WHERE id=? AND external_id=?"), group.DisplayName, now, group.ID, group.ExternalID)
		if err == nil {
			var n int64
			if n, err = res.RowsAffected(); err == nil && n != 1 {
				return ErrNotFound
			}
		}
	}
	if err != nil {
		return err
	}
	group.UpdatedAt = now
	if _, err = tx.ExecContext(ctx, g.store.rebind("DELETE FROM group_members WHERE group_id=?"), group.ID); err != nil {
		return err
	}
	for _, id := range group.Members {
		if _, err = tx.ExecContext(ctx, g.store.rebind("INSERT INTO group_members(group_id,user_id) VALUES(?,?) ON CONFLICT DO NOTHING"), group.ID, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, g.store.rebind(`INSERT INTO audit_records(user_id,action,resource,details,ip_address,created_at) VALUES(?,?,?,?,?,?)`), "scim", "scim.group.replaced", group.ID, "membership applied atomically", "", now); err != nil {
		return err
	}
	return tx.Commit()
}
func (g *groupStore) GetGroupByExternalID(ctx context.Context, id string) (*Group, error) {
	var groupID string
	err := g.store.db.QueryRowContext(ctx, g.store.rebind("SELECT id FROM groups WHERE external_id=?"), id).Scan(&groupID)
	if errorsIs(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return g.GetGroupByID(ctx, groupID)
}
