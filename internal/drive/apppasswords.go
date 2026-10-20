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
// only while its account is active. AI agent tokens share the table with an MCP scope: Bearer
// credentials for /mcp only, never WebDAV, revoked after AgentIdleLimit unused.
const (
	appPasswordsPerUser = 20
	appPasswordTouch    = 5 * time.Minute
	// AgentIdleLimit is how long an agent token may go unused before it is revoked.
	AgentIdleLimit = 90 * 24 * time.Hour
	// AgentTokenPrefix marks an agent token; device tokens are DevicePrefix.
	AgentTokenPrefix = "kya_"
)

// Scopes: a WebDAV password, or an agent token that reads, or reads and writes.
const (
	ScopeDAV      = "dav"
	ScopeMCPRead  = "mcp:read"
	ScopeMCPWrite = "mcp:write"
)

func IsAgentScope(scope string) bool { return scope == ScopeMCPRead || scope == ScopeMCPWrite }

type AppPassword struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Created  string `json:"created"`
	LastUsed int64  `json:"last_used"`
	Scope    string `json:"scope"`
	// Workspace limits an agent token to Folder in it ("" = the workspace root); empty means
	// everything its user can reach.
	Workspace string `json:"workspace,omitempty"`
	Folder    string `json:"folder,omitempty"`
	// RevokeAfter is when an unused agent token is revoked, Unix seconds; 0 for WebDAV.
	RevokeAfter int64 `json:"revoke_after,omitempty"`
}

var appPasswordEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// newSecret is 160 random bits as 32 lowercase base32 characters.
func newSecret() string {
	raw := make([]byte, 20)
	rand.Read(raw)
	return strings.ToLower(appPasswordEncoding.EncodeToString(raw))
}

// CreateAppPassword returns the new password's record and its secret, which is not stored.
func (s *Store) CreateAppPassword(ctx context.Context, user, name string) (AppPassword, string, error) {
	encoded := newSecret()
	var groups []string
	for i := 0; i < len(encoded); i += 4 {
		groups = append(groups, encoded[i:i+4])
	}
	secret := strings.Join(groups, "-")
	p, err := s.createCredential(ctx, user, name, ScopeDAV, "", "", secret)
	return p, secret, err
}

// CreateAgentToken returns a new agent token of scope and its secret, which is not stored. With
// workspace set the token reaches only folder ("" = the workspace root), which must be live and
// readable by user now; the store still authorizes every later operation as user.
func (s *Store) CreateAgentToken(ctx context.Context, user, name, scope, workspace, folder string) (AppPassword, string, error) {
	if !IsAgentScope(scope) || (workspace == "" && folder != "") {
		return AppPassword{}, "", ErrInvalid
	}
	secret := newSecret()
	p, err := s.createCredential(ctx, user, name, scope, workspace, folder, secret)
	return p, AgentTokenPrefix + secret, err
}

func (s *Store) createCredential(ctx context.Context, user, name, scope, workspace, folder, secret string) (AppPassword, error) {
	if isDevice(ctx) {
		return AppPassword{}, ErrDenied
	}
	p := AppPassword{ID: uuid.NewString(), Name: name, Created: now(), Scope: scope, Workspace: workspace, Folder: folder}
	if name == "" || len(name) > 100 || strings.TrimSpace(name) != name {
		return p, ErrInvalid
	}
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service {
		return p, ErrDenied
	}
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
		if workspace != "" {
			if err := authorizeItem(ctx, tx, user, workspace, folder, "", 1); err != nil {
				return err
			}
			if err := parentOK(ctx, tx, workspace, folder); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drive_app_passwords(id,user_id,name,hash,created,scope,root_workspace,root_folder) VALUES(?,?,?,?,?,?,?,?)`, p.ID, user, name, appPasswordHash(secret), p.Created, scope, workspace, folder); err != nil {
			return err
		}
		return event(ctx, tx, user, "app_password.created", p.ID)
	})
	p.RevokeAfter = revokeAfter(p)
	return p, err
}

func appPasswordHash(secret string) string {
	return tokenHash(strings.ToLower(strings.ReplaceAll(secret, "-", "")))
}

// lastActive is when p was last used, or created if never used.
func lastActive(p AppPassword) time.Time {
	if p.LastUsed > 0 {
		return time.Unix(p.LastUsed, 0)
	}
	t, _ := time.Parse(time.RFC3339Nano, p.Created)
	return t
}

func revokeAfter(p AppPassword) int64 {
	if !IsAgentScope(p.Scope) {
		return 0
	}
	return lastActive(p).Add(AgentIdleLimit).Unix()
}

func (s *Store) AppPasswords(ctx context.Context, user string) ([]AppPassword, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,created,last_used,scope,root_workspace,root_folder FROM drive_app_passwords WHERE user_id=? ORDER BY created`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppPassword{}
	for rows.Next() {
		var p AppPassword
		if err = rows.Scan(&p.ID, &p.Name, &p.Created, &p.LastUsed, &p.Scope, &p.Workspace, &p.Folder); err != nil {
			return nil, err
		}
		p.RevokeAfter = revokeAfter(p)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAppPassword(ctx context.Context, user, id string) error {
	if isDevice(ctx) {
		return ErrDenied
	}
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

// SetAgentScope moves the user's own agent token between read and write. A WebDAV password never
// becomes an agent token, nor the reverse (ErrInvalid).
func (s *Store) SetAgentScope(ctx context.Context, user, id, scope string) error {
	if _, service := ctx.Value(serviceAccessKey{}).(ServiceAccess); service || isDevice(ctx) {
		return ErrDenied
	}
	if !IsAgentScope(scope) {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		var old string
		err := tx.QueryRowContext(ctx, `SELECT scope FROM drive_app_passwords WHERE id=? AND user_id=?`, id, user).Scan(&old)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDenied
		}
		if err != nil {
			return err
		}
		if !IsAgentScope(old) {
			return ErrInvalid
		}
		if old == scope {
			return nil
		}
		if _, err = tx.ExecContext(ctx, `UPDATE drive_app_passwords SET scope=? WHERE id=?`, scope, id); err != nil {
			return err
		}
		return event(ctx, tx, user, "app_password.scope_changed", id)
	})
}

// AppPasswordUser returns the ID of the active user named username (any case) that owns secret.
// Agent tokens never match.
func (s *Store) AppPasswordUser(ctx context.Context, username, secret string) (string, error) {
	var id, user string
	var lastUsed int64
	err := s.db.QueryRowContext(ctx, `SELECT p.id,u.id,p.last_used FROM drive_app_passwords p JOIN users u ON u.id=p.user_id WHERE p.hash=? AND p.scope='dav' AND lower(u.username)=lower(?) AND u.status='active'`, appPasswordHash(secret), username).Scan(&id, &user, &lastUsed)
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

// AgentTokenUser returns the user and record of an active user's agent token. WebDAV passwords
// never match. A token unused for AgentIdleLimit before at is deleted and refused like a wrong
// one, whether or not the sweep has run.
func (s *Store) AgentTokenUser(ctx context.Context, token string, at time.Time) (string, AppPassword, error) {
	var p AppPassword
	var user string
	secret, ok := strings.CutPrefix(token, AgentTokenPrefix)
	if !ok || secret == "" {
		return "", p, ErrDenied
	}
	err := s.db.QueryRowContext(ctx, `SELECT p.id,p.name,p.created,p.last_used,p.scope,p.root_workspace,p.root_folder,u.id FROM drive_app_passwords p JOIN users u ON u.id=p.user_id WHERE p.hash=? AND p.scope IN ('mcp:read','mcp:write') AND u.status='active'`, appPasswordHash(secret)).
		Scan(&p.ID, &p.Name, &p.Created, &p.LastUsed, &p.Scope, &p.Workspace, &p.Folder, &user)
	if errors.Is(err, sql.ErrNoRows) {
		return "", AppPassword{}, ErrDenied
	}
	if err != nil {
		return "", AppPassword{}, err
	}
	if at.Sub(lastActive(p)) > AgentIdleLimit {
		if _, err := s.expireAgentTokens(ctx, at, p.ID); err != nil {
			return "", AppPassword{}, err
		}
		return "", AppPassword{}, ErrDenied
	}
	if at.Sub(time.Unix(p.LastUsed, 0)) >= appPasswordTouch {
		if _, err = s.db.ExecContext(ctx, `UPDATE drive_app_passwords SET last_used=? WHERE id=?`, at.Unix(), p.ID); err != nil {
			return "", AppPassword{}, err
		}
		p.LastUsed = at.Unix()
	}
	p.RevokeAfter = revokeAfter(p)
	return user, p, nil
}

// ExpireAgentTokens revokes every agent token unused for AgentIdleLimit before at and returns how
// many. AgentTokenUser refuses an idle token on its own; this removes the ones nobody presents.
func (s *Store) ExpireAgentTokens(ctx context.Context, at time.Time) (int, error) {
	return s.expireAgentTokens(ctx, at, "")
}

// expireAgentTokens deletes idle agent tokens, only id when it is set, each with an
// app_password.expired event.
func (s *Store) expireAgentTokens(ctx context.Context, at time.Time, id string) (int, error) {
	n := 0
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id,user_id,created,last_used,scope FROM drive_app_passwords WHERE scope IN ('mcp:read','mcp:write') AND (?='' OR id=?)`, id, id)
		if err != nil {
			return err
		}
		type idle struct{ id, user string }
		var gone []idle
		for rows.Next() {
			var p AppPassword
			var owner string
			if err = rows.Scan(&p.ID, &owner, &p.Created, &p.LastUsed, &p.Scope); err != nil {
				rows.Close()
				return err
			}
			if at.Sub(lastActive(p)) > AgentIdleLimit {
				gone = append(gone, idle{p.ID, owner})
			}
		}
		if err = errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, g := range gone {
			if _, err = tx.ExecContext(ctx, `DELETE FROM drive_app_passwords WHERE id=?`, g.id); err != nil {
				return err
			}
			if err = event(ctx, tx, g.user, "app_password.expired", g.id); err != nil {
				return err
			}
		}
		n = len(gone)
		return nil
	})
	return n, err
}
