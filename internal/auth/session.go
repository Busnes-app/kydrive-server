package auth

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// Base cookie names. Secure host-only deployments add the __Host- prefix (see cookieName),
// so a sibling subdomain cannot plant them.
const (
	SessionCookieName = "ky_session"
	CSRFCookieName    = "ky_csrf"
	HeaderCSRF        = "X-CSRF-Token"
)

var ErrPasswordChangeRequired = errors.New("password change required")

type SessionManager struct {
	store  store.Store
	config config.SecurityConfig
}

func NewSessionManager(st store.Store, cfg config.SecurityConfig) *SessionManager {
	return &SessionManager{
		store:  st,
		config: cfg,
	}
}

// IssueSession creates an active session in the database and writes HttpOnly cookie + CSRF token.
func (sm *SessionManager) IssueSession(ctx context.Context, w http.ResponseWriter, r *http.Request, user *store.User) (*store.Session, string, error) {
	rawToken := crypto.RandomHex(32)
	tokenHash := crypto.SHA256Hex([]byte(rawToken))

	ttl := sm.config.SessionTTL
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}

	sess := &store.Session{
		TokenHash: tokenHash,
		UserID:    user.ID,
		UserAgent: r.UserAgent(),
		IPAddress: ClientIP(r, sm.config.TrustedProxies),
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(ttl),
	}

	if err := sm.store.Sessions().CreateSession(ctx, sess, user.PasswordHash); err != nil {
		return nil, "", err
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sm.cookieName(SessionCookieName),
		Value:    rawToken,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   sm.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   sm.config.CookieDomain,
	})
	sm.writeCSRFCookie(w, rawToken, sess.ExpiresAt)

	return sess, rawToken, nil
}

// AuthenticateRequest reads the session cookie and returns the user. Sessions are cookie-only so
// the CSRF check always applies; Bearer is reserved for drive service tokens.
func (sm *SessionManager) AuthenticateRequest(r *http.Request) (*store.User, *store.Session, error) {
	rawToken := sm.SessionToken(r)
	if rawToken == "" {
		return nil, nil, store.ErrNotFound
	}

	tokenHash := crypto.SHA256Hex([]byte(rawToken))
	sess, err := sm.store.Sessions().GetSession(r.Context(), tokenHash)
	if err != nil {
		return nil, nil, err
	}

	user, err := sm.store.Users().GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	if user.Status != "active" {
		if err := sm.store.Sessions().DeleteSession(r.Context(), tokenHash); err != nil {
			log.Printf("session of inactive account not deleted: %v", err)
		}
		return nil, nil, store.ErrNotFound
	}

	// The password transaction may revoke the session between the two reads.
	// Recheck after loading the user so an old session cannot inherit a cleared flag.
	if _, err := sm.store.Sessions().GetSession(r.Context(), tokenHash); err != nil {
		return nil, nil, err
	}

	// A restricted session never authorizes product operations or private settings.
	if user.MustChangePassword && !((r.Method == http.MethodGet && r.URL.Path == "/api/auth/me") || (r.Method == http.MethodPost && (r.URL.Path == "/api/auth/change-password" || r.URL.Path == "/api/auth/logout"))) {
		return nil, nil, ErrPasswordChangeRequired
	}
	return user, sess, nil
}

func (sm *SessionManager) cookieName(base string) string {
	if sm.config.CookieSecure && sm.config.CookieDomain == "" {
		return "__Host-" + base
	}
	return base
}

// SessionToken is the raw session token the request presents, or "".
func (sm *SessionManager) SessionToken(r *http.Request) string {
	if cookie, err := r.Cookie(sm.cookieName(SessionCookieName)); err == nil {
		return cookie.Value
	}
	return ""
}

// CSRFToken is derived from the session token, so only the session's own page can know it.
func (sm *SessionManager) CSRFToken(rawSession string) string {
	return crypto.ComputeHMACSHA256([]byte("ky-csrf:"+rawSession), sm.config.SessionSecret)
}

// ValidateCSRF compares the header with the token recomputed from the session cookie. The CSRF
// cookie only carries the value to the SPA; a planted one proves nothing.
func (sm *SessionManager) ValidateCSRF(r *http.Request) bool {
	raw, header := sm.SessionToken(r), r.Header.Get(HeaderCSRF)
	return raw != "" && header != "" && crypto.VerifyHMACSHA256([]byte("ky-csrf:"+raw), sm.config.SessionSecret, header)
}

// RefreshCSRF rewrites the CSRF cookie for the presented session (lost or pre-upgrade cookie).
func (sm *SessionManager) RefreshCSRF(w http.ResponseWriter, r *http.Request, sess *store.Session) {
	sm.writeCSRFCookie(w, sm.SessionToken(r), sess.ExpiresAt)
}

func (sm *SessionManager) writeCSRFCookie(w http.ResponseWriter, rawSession string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sm.cookieName(CSRFCookieName),
		Value:    sm.CSRFToken(rawSession),
		Path:     "/",
		Expires:  expires,
		HttpOnly: false, // read by the SPA into the X-CSRF-Token header
		Secure:   sm.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   sm.config.CookieDomain,
	})
}

// RevokeSession ends the active session, with the account's editor sessions, and clears the browser cookies.
func (sm *SessionManager) RevokeSession(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	var err error
	if raw := sm.SessionToken(r); raw != "" {
		err = sm.store.Sessions().EndSession(ctx, crypto.SHA256Hex([]byte(raw)))
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sm.cookieName(SessionCookieName),
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   sm.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   sm.config.CookieDomain,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     sm.cookieName(CSRFCookieName),
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		HttpOnly: false,
		Secure:   sm.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Domain:   sm.config.CookieDomain,
	})

	return err
}
