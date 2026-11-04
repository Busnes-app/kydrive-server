package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// handleChangePassword changes a local account's own password, whether a replacement is
// mandatory or not, after verifying the current one. All sessions and credentials are revoked
// atomically with the password update; the user signs in again afterward.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user, _, err := s.sessions.AuthenticateRequest(r)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "Authentication required")
		return
	}
	if user.SSOProvider != "local" || user.PasswordHash == "" {
		s.writeError(w, http.StatusConflict, "This account's password is managed by KyIdentity")
		return
	}
	// Checking the current password is a sign-in guess: it spends the login window too, so a
	// left-open session cannot guess faster here than at the sign-in page.
	if !s.allowAttempt("password-change:"+s.requestIP(r), 10, time.Minute) || !s.allowAttempt("password-change-user:"+user.ID, 5, time.Minute) ||
		!s.allowAttempt(s.accountKey("login-user", user.Username, r), 10, 15*time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many password change attempts")
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if len(req.CurrentPassword) > 1024 || len(req.NewPassword) > 1024 {
		s.writeError(w, http.StatusBadRequest, "Password is too long")
		return
	}
	if err := auth.ValidatePassword(req.NewPassword); err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.CurrentPassword == req.NewPassword {
		s.writeError(w, http.StatusBadRequest, "Choose a different password")
		return
	}
	ok, err := password.Verify(req.CurrentPassword, user.PasswordHash)
	if err != nil || !ok {
		s.writeError(w, http.StatusUnauthorized, "Current password is incorrect")
		return
	}
	hash, err := password.Hash(req.NewPassword)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to secure password")
		return
	}
	if err := s.store.Users().CompletePasswordChange(r.Context(), user.ID, user.PasswordHash, hash, s.requestIP(r)); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, http.StatusConflict, "Account changed; sign in again")
			return
		}
		s.writeError(w, http.StatusInternalServerError, "Failed to change password")
		return
	}
	// The change already deleted every session; this clears the cookies.
	if err := s.sessions.RevokeSession(r.Context(), w, r); err != nil {
		log.Printf("password change: %v", err)
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"password_changed": true, "login_required": true})
}
