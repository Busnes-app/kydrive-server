package api

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/sso"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"golang.org/x/oauth2"
)

func (s *Server) handleKySignOnLogin(w http.ResponseWriter, r *http.Request) {
	if !s.config.SSO.Enabled {
		s.writeError(w, http.StatusNotFound, "Sign-on is disabled")
		return
	}
	state := crypto.RandomHex(16)
	nonce := crypto.RandomHex(16)
	verifier := oauth2.GenerateVerifier()
	stepUp := r.URL.Query().Get("step_up") == "1"
	if stepUp {
		// A step-up re-proves the signed-in directory account; the callback returns to the page.
		user, _, err := s.sessions.AuthenticateRequest(r)
		if err != nil {
			s.writeError(w, http.StatusUnauthorized, "Authentication required")
			return
		}
		if stepUpMethod(user) != "sso" {
			s.writeError(w, http.StatusConflict, "Confirm it's you with your password")
			return
		}
		back := r.URL.Query().Get("return")
		if !localPath(back) {
			back = "/"
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "ky_stepup_" + state,
			Value:    s.stepUpCookie(state, strconv.FormatInt(s.now().Unix(), 10)+"|"+back),
			Path:     "/api/sso/kysignon/callback",
			MaxAge:   300,
			HttpOnly: true,
			Secure:   s.config.Security.CookieSecure,
			SameSite: http.SameSiteLaxMode,
		})
	}

	redirectURI := fmt.Sprintf("%s/api/sso/kysignon/callback", s.config.Server.AppURL)
	authURL, err := s.kysignon.BuildAuthURL(r.Context(), redirectURI, state, verifier, nonce, stepUp)
	if err != nil {
		log.Printf("sso: authorization URL: %v", err)
		s.writeError(w, http.StatusBadGateway, "Sign-on provider unavailable")
		return
	}

	// Store verifier in short-lived cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "ky_pkce_" + state,
		Value:    verifier,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   s.config.Security.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     "ky_nonce_" + state,
		Value:    nonce,
		Path:     "/api/sso/kysignon/callback",
		MaxAge:   300,
		HttpOnly: true,
		Secure:   s.config.Security.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) handleKySignOnCallback(w http.ResponseWriter, r *http.Request) {
	// KY_SSO_ENABLED=false closes sign-in, not just the button; back-channel logout stays open.
	if !s.config.SSO.Enabled {
		s.writeError(w, http.StatusNotFound, "Sign-on is disabled")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	cookie, err := r.Cookie("ky_pkce_" + state)
	if err != nil || cookie.Value == "" {
		s.writeError(w, http.StatusBadRequest, "Invalid or expired SSO state")
		return
	}
	verifier := cookie.Value
	nonceCookie, err := r.Cookie("ky_nonce_" + state)
	if err != nil || nonceCookie.Value == "" {
		s.writeError(w, http.StatusBadRequest, "Invalid or expired SSO nonce")
		return
	}

	redirectURI := fmt.Sprintf("%s/api/sso/kysignon/callback", s.config.Server.AppURL)
	claims, err := s.kysignon.ExchangeCode(r.Context(), code, verifier, redirectURI, nonceCookie.Value)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "SSO verification failed")
		return
	}
	if c, err := r.Cookie("ky_stepup_" + state); err == nil {
		s.finishSSOStepUp(w, r, state, c.Value, claims)
		return
	}

	user, err := store.DirectorySubject(r.Context(), s.store.Users(), claims.Subject)
	if errors.Is(err, store.ErrNotFound) && s.config.SSO.AutoProvision {
		user, err = s.store.Users().ApplyDirectoryUser(r.Context(), &store.DirectoryUser{
			Provider: "kysignon", Subject: claims.Subject, Username: claims.PreferredUsername,
			Email: claims.Email, DisplayName: claims.Name, Active: true,
		})
		if errors.Is(err, store.ErrInvalidUsername) || errors.Is(err, store.ErrAlreadyExists) {
			s.writeError(w, http.StatusForbidden, "User account not provisioned")
			return
		}
		if err != nil {
			log.Printf("sso: auto-provision: %v", err)
			s.writeError(w, http.StatusInternalServerError, "Failed to provision SSO user")
			return
		}
	}
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, http.StatusForbidden, "User account not provisioned")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Authentication error")
		return
	}

	if user.Status != "active" {
		s.writeError(w, 403, "Account is inactive")
		return
	}
	_, _, err = s.sessions.IssueSSOSession(r.Context(), w, r, user, claims.SessionID, claims.IssuedAt)
	if errors.Is(err, store.ErrSSOSessionEnded) {
		s.writeError(w, http.StatusUnauthorized, "SSO session already ended")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Session creation failed")
		return
	}

	http.Redirect(w, r, "/", http.StatusFound)
}

// localPath reports whether back is a path on this site: no scheme, host, backslash or control
// character (browsers drop tabs and newlines, so "/\t/evil" would read as "//evil").
func localPath(back string) bool {
	u, err := url.Parse(back)
	return err == nil && u.Scheme == "" && u.Host == "" && strings.HasPrefix(back, "/") && !strings.HasPrefix(back, "//") &&
		!strings.ContainsFunc(back, func(r rune) bool { return r == '\\' || r < 0x20 || r == 0x7f })
}

// stepUpCookie carries the step-up start and return path, MACed with the session secret and
// bound to the OAuth state, so the browser cannot back-date the start or reuse it for another state.
func (s *Server) stepUpCookie(state, value string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(value))
	return payload + "." + crypto.ComputeHMACSHA256([]byte("ky-stepup:"+state+"|"+payload), s.config.Security.SessionSecret)
}

// finishSSOStepUp dates the current session's step-up from the KyIdentity auth_time, which
// max_age=0 made fresh. It never signs anyone in: the token must name the session's own account.
func (s *Server) finishSSOStepUp(w http.ResponseWriter, r *http.Request, state, cookie string, claims *sso.IdentityClaims) {
	payload, mac, _ := strings.Cut(cookie, ".")
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	startText, back, found := strings.Cut(string(raw), "|")
	start, perr := strconv.ParseInt(startText, 10, 64)
	if !crypto.VerifyHMACSHA256([]byte("ky-stepup:"+state+"|"+payload), s.config.Security.SessionSecret, mac) || err != nil || !found || perr != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid or expired SSO state")
		return
	}
	user, sess, err := s.sessions.AuthenticateRequest(r)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "Sign in again to continue")
		return
	}
	owner, err := store.DirectorySubject(r.Context(), s.store.Users(), claims.Subject)
	if err != nil || owner.ID != user.ID {
		s.writeError(w, http.StatusForbidden, "That KyIdentity account is not the one signed in here")
		return
	}
	// One minute of clock skew either way; a missing auth_time never passes.
	const skew = time.Minute
	if claims.AuthTime.IsZero() || claims.AuthTime.Before(time.Unix(start, 0).Add(-skew)) || claims.AuthTime.After(s.now().Add(skew)) {
		s.writeError(w, http.StatusForbidden, "KyIdentity did not ask you to sign in again, so this was not confirmed")
		return
	}
	if err := s.store.Sessions().StepUp(r.Context(), sess.TokenHash, claims.AuthTime); err != nil {
		s.writeError(w, http.StatusUnauthorized, "Sign in again to continue")
		return
	}
	s.audit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "auth.step_up", Resource: "session", Details: "method=sso", IPAddress: s.requestIP(r)})
	http.Redirect(w, r, back, http.StatusFound)
}

func (s *Server) handleKySignOnSyncWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	sig := r.Header.Get("X-KySignOn-Signature")
	if sig == "" {
		sig = r.Header.Get("X-Signature-SHA256")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	// 401 only for authentication, so the sender retries outages (500) but not bad input (400).
	if err := s.kysignon.HandleSyncWebhook(r.Context(), body, sig); err != nil {
		switch {
		case errors.Is(err, sso.ErrWebhookUnauthorized):
			s.writeError(w, http.StatusUnauthorized, "Invalid webhook signature or timestamp")
		case errors.Is(err, sso.ErrWebhookMalformed):
			s.writeError(w, http.StatusBadRequest, "Malformed webhook payload")
		default:
			log.Printf("sso: directory webhook: %v", err)
			s.writeError(w, http.StatusInternalServerError, "Directory sync failed")
		}
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]bool{"synced": true})
}

// ssoLogoutFence outlives the 5-minute login callback window, so a logout that lands
// while the browser is still finishing that login also ends it.
const ssoLogoutFence = 10 * time.Minute

// handleKySignOnBackchannelLogout is the OpenID Connect Back-Channel Logout receiver: KySignOn
// POSTs a signed logout_token when a login ends, and every local session of it ends here.
// Not rate-limited: a KyIdentity restore sends a burst, and a 429 spends its few retries.
func (s *Server) handleKySignOnBackchannelLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	claims, err := s.kysignon.VerifyLogoutToken(r.Context(), r.PostFormValue("logout_token"))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	rec := &store.AuditRecord{Action: "auth.sso_backchannel_logout", Resource: "session", IPAddress: s.requestIP(r)}
	fenceUntil := time.Now().Add(ssoLogoutFence)
	if claims.SessionID != "" {
		err = s.store.Sessions().EndSSOSession(r.Context(), claims.SessionID, fenceUntil)
	} else {
		// iat has whole-second precision: round up so a login from that same second ends too.
		err = s.store.Sessions().EndSSOSubject(r.Context(), "kysignon", claims.Subject, claims.IssuedAt.Add(time.Second), fenceUntil)
		if user, lookupErr := store.DirectorySubject(r.Context(), s.store.Users(), claims.Subject); err == nil && lookupErr == nil {
			rec.UserID = user.ID
		}
	}
	if err != nil {
		log.Printf("sso: back-channel logout: %v", err)
		s.writeError(w, http.StatusInternalServerError, "logout failed")
		return
	}
	s.audit(r.Context(), rec)
	w.WriteHeader(http.StatusOK)
}
