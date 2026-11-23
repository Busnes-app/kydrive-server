package store

import (
	"context"
	"errors"
	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"time"
)

var (
	ErrNotFound       = errors.New("record not found")
	ErrAlreadyExists  = errors.New("record already exists")
	ErrSessionExpired = errors.New("session expired")
	// ErrSSOSessionEnded means the identity provider already ended the login being signed in.
	ErrSSOSessionEnded = errors.New("SSO session already ended")
	// ErrLogoutTokenSpent is a back-channel logout token already acted on.
	ErrLogoutTokenSpent = errors.New("logout token already spent")
	// ErrStale reports a directory event no newer than the last one applied to the account.
	ErrStale           = errors.New("directory event is not newer than the applied state")
	ErrInvalidUsername = errors.New("directory username must be 1-128 letters, digits or ._@+-")
	// ErrErased refuses an account KyIdentity erased (Delete everything): never created or
	// reactivated again.
	ErrErased = errors.New("directory subject was erased")
	// ErrOtherOrg is an erasure naming an account that is in another org.
	ErrOtherOrg = errors.New("account is in another org")
)

// ErasedSince is the server_settings key holding the revision the next read of KyIdentity's
// erased list starts after. A restore deletes it, so the whole list is read again.
const ErasedSince = "erased_since"

// Store defines the unified storage contract implemented across SQLite, PostgreSQL, and MySQL.
type Store interface {
	Drive() *drive.Store
	Users() UserStore
	Sessions() SessionStore
	Groups() GroupStore
	Audit() AuditStore
	Settings() SettingsStore
	ApplyInstaller(context.Context, applysetup.Common, bool) (applysetup.Report, error)
	InstallerAdministrator(context.Context, string) (bool, error)

	Driver() string
	Ping(ctx context.Context) error
	Close() error
	// ResetAfterRestore invalidates restored bearer grants without changing accounts or content.
	ResetAfterRestore(context.Context) error
}

// LogoutToken is a back-channel logout token's jti and expiry. The zero value spends nothing.
type LogoutToken struct {
	JTI     string
	Expires time.Time
}

// UserStore defines repository operations for accounts.
type UserStore interface {
	CreateUser(ctx context.Context, u *User) error
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserBySSO(ctx context.Context, provider, subject string) (*User, error)
	UpdateUser(ctx context.Context, u *User) error
	// ApplyDirectoryUser is the one directory (SCIM, KySignOn) write path; see DirectoryUser.
	ApplyDirectoryUser(ctx context.Context, d *DirectoryUser) (*User, error)
	// DeleteDirectoryUser deletes a directory account; revision orders it like ApplyDirectoryUser.
	DeleteDirectoryUser(ctx context.Context, id string, revision int64) error
	// EraseDirectoryUser records that KyIdentity erased subject in org (Delete everything),
	// for good, and disables its account if there is one. Repeating it changes nothing.
	EraseDirectoryUser(ctx context.Context, subject, org string, revision int64) error
	// RevokeDirectoryCredentials ends a directory account's sessions and every credential it
	// issued, as disabling does, but leaves it active. KyIdentity's MFA reset calls it.
	RevokeDirectoryCredentials(ctx context.Context, id string) error
	ResetAdminPassword(ctx context.Context, userID, newHash string) error
	RenameLocalAdmin(ctx context.Context, oldName, newName string) error
	SetDirectoryRole(ctx context.Context, subject, role string) error
	CompletePasswordChange(ctx context.Context, userID, oldHash, newHash, ip string) error
	DeleteUser(ctx context.Context, id string) error
	// OpenInNewTab is the user's choice to open files in a new browser tab (default true).
	// It lives outside User so profile and directory updates never overwrite it.
	OpenInNewTab(ctx context.Context, userID string) (bool, error)
	SetOpenInNewTab(ctx context.Context, userID string, v bool) error
	ListUsers(ctx context.Context, offset, limit int, search string) ([]*User, int, error)
	// ListDirectoryUsers pages SCIM-provisioned accounts only.
	ListDirectoryUsers(ctx context.Context, offset, limit int) ([]*User, int, error)
	CountUsers(ctx context.Context) (int, error)
}

// SessionStore defines repository operations for active login sessions.
type SessionStore interface {
	// CreateSession refuses when the password or the MFA state changed since the caller checked them.
	CreateSession(ctx context.Context, s *Session, expectedPasswordHash string, mfaEnabled bool) error
	GetSession(ctx context.Context, tokenHash string) (*Session, error)
	// StepUp records at as the session's latest step-up; ErrNotFound when the session is gone.
	StepUp(ctx context.Context, tokenHash string, at time.Time) error
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	// EndSSOSubject ends the provider subject's sessions whose credentials were verified
	// before cutoff and fences that subject's earlier logins until fenceUntil.
	// A non-zero token is spent in the transaction that ends the sessions (ErrLogoutTokenSpent
	// if it already was), so a failed revocation leaves it unspent for the retry.
	EndSSOSubject(ctx context.Context, provider, subject string, cutoff, fenceUntil time.Time, token LogoutToken) error
	// EndSSOSession ends every session of an SSO login and fences its sid until fenceUntil.
	// A non-empty org limits it to accounts in that org, and returns how many of the sid's
	// sessions it left because their account is elsewhere. token is spent as EndSSOSubject's.
	EndSSOSession(ctx context.Context, sid, org string, fenceUntil time.Time, token LogoutToken) (otherOrg int, err error)
	EndSession(ctx context.Context, tokenHash string) error
	CleanExpiredSessions(ctx context.Context) error
	CleanExpiredMFAChallenges(ctx context.Context) error
	CreateMFAChallenge(ctx context.Context, challenge *MFAChallenge, expectedPasswordHash string) error
	ConsumeMFAChallenge(ctx context.Context, tokenHash string) (userID, passwordHash string, err error)
}

// GroupStore defines repository operations for SCIM and RBAC groups.
type GroupStore interface {
	CreateGroup(ctx context.Context, g *Group) error
	ReplaceGroup(ctx context.Context, g *Group, create bool) error
	GetGroupByExternalID(ctx context.Context, id string) (*Group, error)
	GetGroupByID(ctx context.Context, id string) (*Group, error)
	GetGroupByName(ctx context.Context, name string) (*Group, error)
	UpdateGroup(ctx context.Context, g *Group) error
	DeleteGroup(ctx context.Context, id string) error
	ListGroups(ctx context.Context, offset, limit int) ([]*Group, int, error)
	AddGroupMember(ctx context.Context, groupID, userID string) error
	RemoveGroupMember(ctx context.Context, groupID, userID string) error
	GetUserGroups(ctx context.Context, userID string) ([]*Group, error)
}

// AuditStore logs security events.
type AuditStore interface {
	LogAudit(ctx context.Context, r *AuditRecord) error
	ListAuditRecords(ctx context.Context, offset, limit int) ([]*AuditRecord, int, error)
}

// SettingsStore handles persistent key-value configuration.
type SettingsStore interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, val string) error
	DeleteSetting(ctx context.Context, key string) error
	GetAllSettings(ctx context.Context) (map[string]string, error)
}

// DirectorySubject finds a directory account by OIDC subject / SCIM externalId: the KySignOn
// row first, then the SCIM row, so sign-on and webhooks resolve the same account.
func DirectorySubject(ctx context.Context, users UserStore, subject string) (*User, error) {
	user, err := users.GetUserBySSO(ctx, "kysignon", subject)
	if errors.Is(err, ErrNotFound) {
		user, err = users.GetUserBySSO(ctx, "scim", subject)
	}
	return user, err
}

// ActivePrincipal reports whether u may act: active, and in an active, enabled org. A store
// without a drive (PostgreSQL) has no orgs.
func ActivePrincipal(ctx context.Context, st Store, u *User) (bool, error) {
	if u.Status != "active" {
		return false, nil
	}
	if st.Drive() == nil {
		return true, nil
	}
	return st.Drive().ActivePrincipal(ctx, u.ID)
}

// Administrator reports whether u may administer: role admin, an active principal, and on a
// multi-tenant drive in the staff org (D9).
func Administrator(ctx context.Context, st Store, u *User) (bool, error) {
	if u.Role != "admin" {
		return false, nil
	}
	if ok, err := ActivePrincipal(ctx, st, u); err != nil || !ok {
		return false, err
	}
	if st.Drive() == nil {
		return true, nil
	}
	return st.Drive().Staff(ctx, u.ID)
}
