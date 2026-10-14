package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// totpSecret opens the user's sealed secret and reseals a legacy raw-key row in place.
func (s *Server) totpSecret(ctx context.Context, user *store.User) (string, error) {
	key := s.config.Security.EncryptionKey
	secret, legacy, err := auth.OpenTOTPSecret(key, user.ID, user.TOTPSecretEnc)
	if err != nil || !legacy {
		return secret, err
	}
	sealed, err := auth.SealTOTPSecret(key, user.ID, secret)
	if err == nil {
		// A concurrent change wins; the next sign-in reseals again.
		if err = s.store.Users().SetTOTPSecret(ctx, user.ID, user.TOTPSecretEnc, sealed, user.TOTPEnabled); err == nil {
			user.TOTPSecretEnc = sealed
		}
	}
	if err != nil && !errors.Is(err, store.ErrAlreadyExists) {
		log.Printf("totp: reseal legacy secret for %s: %v", user.ID, err)
	}
	return secret, nil
}

// mfaAccount refuses accounts that do not sign in with a local password: MFA guards that
// sign-in only, and disabling it needs the password.
func (s *Server) mfaAccount(w http.ResponseWriter, user *store.User) bool {
	if user.SSOProvider != "local" || user.PasswordHash == "" {
		s.writeError(w, http.StatusConflict, "Two-factor authentication is managed by your identity provider")
		return false
	}
	return true
}

func (s *Server) handleMFAEnroll(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r)
	if !s.mfaAccount(w, user) {
		return
	}
	if user.TOTPEnabled {
		s.writeError(w, http.StatusConflict, "Two-factor authentication is already on; turn it off first")
		return
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to start enrolment")
		return
	}
	sealed, err := auth.SealTOTPSecret(s.config.Security.EncryptionKey, user.ID, secret)
	if err == nil {
		err = s.store.Users().SetTOTPSecret(r.Context(), user.ID, user.TOTPSecretEnc, sealed, false)
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		s.writeError(w, http.StatusConflict, "Account changed; try again")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to start enrolment")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{
		"secret":      secret,
		"otpauth_uri": auth.GenerateTOTPURL(s.config.Server.AppName, user.Username, secret),
	})
}

func (s *Server) handleMFAEnrollConfirm(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r)
	if !s.mfaAccount(w, user) {
		return
	}
	if !s.allowAttempt("mfa-enroll-user:"+user.ID, 5, 5*time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if decodeDrive(r, &req) != nil {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if user.TOTPEnabled || user.TOTPSecretEnc == "" {
		s.writeError(w, http.StatusConflict, "No enrolment is pending")
		return
	}
	secret, err := s.totpSecret(r.Context(), user)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to decrypt MFA secret")
		return
	}
	counter, ok := auth.ValidateTOTP(secret, req.Code)
	if !ok {
		s.writeError(w, http.StatusUnauthorized, "Invalid TOTP verification code")
		return
	}
	if err := s.store.Users().SpendTOTPCounter(r.Context(), user.ID, counter); err != nil {
		s.writeError(w, http.StatusUnauthorized, "TOTP code already used")
		return
	}
	codes, hashes, err := auth.GenerateRecoveryCodes(10)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to generate recovery codes")
		return
	}
	err = s.store.Users().EnableTOTP(r.Context(), user.ID, user.TOTPSecretEnc, hashes, s.sessionHash(r), s.requestIP(r))
	if errors.Is(err, store.ErrAlreadyExists) {
		s.writeError(w, http.StatusConflict, "Enrolment changed; start again")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to enable two-factor authentication")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"totp_enabled": true, "recovery_codes": codes})
}

func (s *Server) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r)
	if !s.mfaAccount(w, user) {
		return
	}
	if !s.allowAttempt("mfa-disable-user:"+user.ID, 5, 5*time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts")
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if decodeDrive(r, &req) != nil || len(req.Password) > 1024 {
		s.writeError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if !user.TOTPEnabled {
		s.writeError(w, http.StatusConflict, "Two-factor authentication is off")
		return
	}
	if ok, err := password.Verify(req.Password, user.PasswordHash); err != nil || !ok || !s.spendSecondFactor(r.Context(), user, req.Code) {
		s.writeError(w, http.StatusUnauthorized, "Password or code is incorrect")
		return
	}
	err := s.store.Users().DisableTOTP(r.Context(), user.ID, s.sessionHash(r), s.requestIP(r))
	if errors.Is(err, store.ErrAlreadyExists) {
		s.writeError(w, http.StatusConflict, "Two-factor authentication is off")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "Failed to disable two-factor authentication")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]bool{"totp_enabled": false})
}

// spendSecondFactor accepts a current TOTP code or an unused recovery code, spending it.
func (s *Server) spendSecondFactor(ctx context.Context, user *store.User, code string) bool {
	secret, err := s.totpSecret(ctx, user)
	if err != nil {
		log.Printf("totp: secret for %s unreadable: %v", user.ID, err)
	} else if counter, ok := auth.ValidateTOTP(secret, code); ok {
		return spent(s.store.Users().SpendTOTPCounter(ctx, user.ID, counter))
	}
	next, ok := auth.RedeemRecoveryCode(code, user.RecoveryCodesHash)
	return ok && spent(s.store.Users().UpdateRecoveryCodes(ctx, user.ID, user.RecoveryCodesHash, next))
}

// spent reports a successful spend; a replayed code (ErrAlreadyExists) is refused quietly.
func spent(err error) bool {
	if err != nil && !errors.Is(err, store.ErrAlreadyExists) {
		log.Printf("mfa: code not spent: %v", err)
	}
	return err == nil
}

func (s *Server) sessionHash(r *http.Request) string {
	return crypto.SHA256Hex([]byte(s.sessions.SessionToken(r)))
}
