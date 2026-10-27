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
	// ErrStale reports a directory event no newer than the last one applied to the account.
	ErrStale           = errors.New("directory event is not newer than the applied state")
	ErrInvalidUsername = errors.New("directory username must be 1-128 letters, digits or ._@+-")
)

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
	// DeleteDirectoryUser deletes a directory account; eventTime orders it like ApplyDirectoryUser.
	DeleteDirectoryUser(ctx context.Context, id string, eventTime int64) error
	ResetAdminPassword(ctx context.Context, userID, newHash string) error
	RenameLocalAdmin(ctx context.Context, oldName, newName string) error
	SetDirectoryRole(ctx context.Context, subject, role string) error
	CompletePasswordChange(ctx context.Context, userID, oldHash, newHash, ip string) error
	UpdateRecoveryCodes(ctx context.Context, userID, oldHashes, newHashes string) error
	// SpendTOTPCounter records counter as used. It returns ErrAlreadyExists when counter is
	// not greater than the stored one, which is how a replayed code inside the skew window fails.
	SpendTOTPCounter(ctx context.Context, userID string, counter int64) error
	// SetTOTPSecret replaces the sealed secret oldEnc with newEnc while totp_enabled equals
	// enabled: a pending enrolment (false) or resealing a legacy row. ErrAlreadyExists if
	// either changed.
	SetTOTPSecret(ctx context.Context, userID, oldEnc, newEnc string, enabled bool) error
	// EnableTOTP turns on the pending secret secretEnc with recoveryHashes; DisableTOTP clears
	// secret and codes. Both revoke every other session (keepSession is the caller's token
	// hash) and all MFA challenges, audit, and return ErrAlreadyExists if the state changed.
	EnableTOTP(ctx context.Context, userID, secretEnc, recoveryHashes, keepSession, ip string) error
	DisableTOTP(ctx context.Context, userID, keepSession, ip string) error
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
	DeleteSession(ctx context.Context, tokenHash string) error
	DeleteUserSessions(ctx context.Context, userID string) error
	// DeleteUserSessionsBefore ends a user's sessions created before t.
	DeleteUserSessionsBefore(ctx context.Context, userID string, t time.Time) error
	// EndSSOSession ends every session of an SSO login and fences its sid until fenceUntil.
	EndSSOSession(ctx context.Context, sid string, fenceUntil time.Time) error
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
