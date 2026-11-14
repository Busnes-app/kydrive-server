package api

import (
	"net/http"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// stepUpWindow is how long a re-proof of identity lets a session mint credentials that outlive it.
const stepUpWindow = 5 * time.Minute

type sessionKey struct{}

// stepUpMethod is how this account re-proves itself: its local password, or a fresh KyIdentity sign-in.
func stepUpMethod(u *store.User) string {
	if u.SSOProvider == "local" && u.PasswordHash != "" {
		return "password"
	}
	return "sso"
}

// requireStepUp guards creation of credentials that outlive the session (WebDAV passwords, agent
// and service tokens, linked computers, guest links): the session must have re-proved its user within
// stepUpWindow. Bearer callers have no session and are refused the same way.
func (s *Server) requireStepUp(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, _ := r.Context().Value(sessionKey{}).(*store.Session)
		if sess == nil || sess.StepUpAt.IsZero() || s.now().Sub(sess.StepUpAt) > stepUpWindow {
			s.writeJSON(w, http.StatusForbidden, map[string]string{"error": "Confirm it's you to continue", "code": "step_up_required", "method": stepUpMethod(s.driveUser(r))})
			return
		}
		h(w, r)
	}
}

// handleStepUp re-proves a local account by its password, under the login limits.
func (s *Server) handleStepUp(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r)
	sess, _ := r.Context().Value(sessionKey{}).(*store.Session)
	if sess == nil {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if stepUpMethod(user) != "password" {
		s.writeJSON(w, http.StatusConflict, map[string]string{"error": "Confirm it's you with KyIdentity", "code": "step_up_sso"})
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if decodeDrive(r, &in) != nil || in.Password == "" {
		s.writeError(w, http.StatusBadRequest, "Password is required")
		return
	}
	accountKey := s.accountKey("login-user", user.Username, r)
	if !s.allowPerIP(r, "login", 20, time.Minute) || !s.allowAttempt(accountKey, 10, 15*time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts. Wait a few minutes and try again.")
		return
	}
	if ok, err := password.Verify(in.Password, user.PasswordHash); err != nil || !ok {
		s.writeError(w, http.StatusForbidden, "Incorrect password")
		return
	}
	s.resetAttempts(accountKey)
	if err := s.store.Sessions().StepUp(r.Context(), sess.TokenHash, s.now()); err != nil {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	s.audit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "auth.step_up", Resource: "session", Details: "method=password", IPAddress: s.requestIP(r)})
	s.writeJSON(w, http.StatusOK, map[string]bool{"confirmed": true})
}
