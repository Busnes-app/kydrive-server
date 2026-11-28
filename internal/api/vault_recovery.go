package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

// Recovery limits per account: a lost-device recovery needs one fetch and one commit.
const (
	recoveryFetches = 10
	recoveryCommits = 10
	recoveryWindow  = time.Hour
)

// vaultNative admits signed native devices only: no browser origin, staging included.
func (s *Server) vaultNative(handle func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	device := s.vaultDevice(handle)
	return func(w http.ResponseWriter, r *http.Request) {
		if len(r.Header.Values("Origin")) != 0 {
			s.writeError(w, http.StatusUnauthorized, "Signed device credential required")
			return
		}
		device(w, r)
	}
}

func (s *Server) auditRecovery(ctx context.Context, r *http.Request, user, action string, err error) {
	result := "ok"
	if err != nil {
		result = "refused"
	}
	s.audit(ctx, &store.AuditRecord{UserID: user, Action: action, Resource: "vault-recovery", Details: "result=" + result, IPAddress: s.requestIP(r)})
}

var errRecoveryLimit = errors.New("recovery rate limit")

func (s *Server) recoveryError(w http.ResponseWriter, err error) {
	if errors.Is(err, errRecoveryLimit) {
		s.writeError(w, http.StatusTooManyRequests, "Too many recovery attempts. Try again later.")
		return
	}
	s.vaultError(w, err)
}

// commitVaultRecovery registers, rotates, or replaces the account key under a recovery
// signature. Refused attempts are audited and counted like successful ones.
func (s *Server) commitVaultRecovery(w http.ResponseWriter, r *http.Request, user string) {
	var in drive.VaultRecovery
	err := errRecoveryLimit
	if s.allowAttempt("vault-recovery-commit:"+user, recoveryCommits, recoveryWindow) {
		err = vault.ErrInvalid
		if vault.Decode(r.Body, &in) == nil {
			err = s.store.Drive().CommitVaultRecovery(r.Context(), user, in)
		}
	}
	s.auditRecovery(r.Context(), r, user, "vault.recovery_commit", err)
	if err != nil {
		s.recoveryError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// vaultRecoveryCopy returns the caller's own ciphertext copy. A device was linked after
// step-up, and the copy opens only with the offline recovery key, so step-up adds nothing here.
func (s *Server) vaultRecoveryCopy(w http.ResponseWriter, r *http.Request, user string) {
	var out drive.VaultRecovery
	err := errRecoveryLimit
	if s.allowAttempt("vault-recovery-fetch:"+user, recoveryFetches, recoveryWindow) {
		out, err = s.store.Drive().VaultRecoveryCopy(r.Context(), user)
	}
	s.auditRecovery(r.Context(), r, user, "vault.recovery_fetch", err)
	if err != nil {
		s.recoveryError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, out)
}

// vaultRecoveryHistory serves an org member's public chain, never a copy.
func (s *Server) vaultRecoveryHistory(w http.ResponseWriter, r *http.Request, user string) {
	target := r.PathValue("user")
	if !vault.ValidUserID(target) {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	records, err := s.store.Drive().VaultRecoveryHistory(r.Context(), user, target)
	if err != nil {
		s.vaultError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string][]vault.RecoveryRecord{"records": records})
}
