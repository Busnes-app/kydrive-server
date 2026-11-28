package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

func (s *Server) vaultRoutes() {
	if s.config.Vault.Publisher == nil || s.config.Vault.Witness == nil {
		return
	}
	s.mux.HandleFunc("PUT /api/vault-keys", s.vaultDevice(s.enrollVaultKey))
	s.mux.HandleFunc("GET /api/vault-keys/{user}", s.vaultDevice(s.vaultUserKey))
	s.mux.HandleFunc("POST /api/vaults/{id}/records", s.vaultDevice(s.commitVaultRecord))
	s.mux.HandleFunc("GET /api/vaults/{id}/records", s.vaultDevice(s.vaultRecords))
	s.mux.HandleFunc("POST /api/vaults/{id}/check", s.vaultDevice(s.checkVaultAccess))
	s.mux.HandleFunc("PUT /api/vaults/{id}/files/{file}/versions/{revision}", s.vaultDevice(s.writeVaultVersion))
	s.mux.HandleFunc("GET /api/vaults/{id}/files/{file}/versions/{revision}", s.vaultDevice(s.readVaultVersion))
	s.mux.HandleFunc("PUT /api/vaults/{id}/files/{file}/editor", s.vaultDevice(s.bindVaultEditor))
	s.mux.HandleFunc("GET /api/vaults/{id}/files/{file}/editor", s.vaultDevice(s.readVaultEditor))
	s.mux.HandleFunc("POST /api/vaults/{id}/files/{file}/editor/session", s.vaultDevice(s.vaultEditorSession))
	s.mux.HandleFunc("POST /api/vault-relay/check", s.vaultRelayCheck)
	s.mux.HandleFunc("PUT /api/vault-relay/checkpoints/{blob}", s.vaultRelayCheckpoint)
	s.mux.HandleFunc("GET /api/vault-relay/checkpoints/{blob}", s.vaultRelayCheckpoint)
	if s.config.Vault.BrowserOrigin != "" {
		s.mux.HandleFunc("POST /api/vault-browser/token", s.handleVaultBrowserToken)
	}
}

type vaultDeviceContextKey struct{}

const vaultBrowserTokenPath = "/api/vault-browser/token"

func (s *Server) vaultBrowserOrigin(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	return s.config.Vault.Publisher != nil && s.config.Vault.Witness != nil &&
		s.config.Vault.BrowserOrigin != "" && len(origins) == 1 && origins[0] == s.config.Vault.BrowserOrigin
}

func (s *Server) handleVaultBrowserToken(w http.ResponseWriter, r *http.Request) {
	if !s.vaultBrowserOrigin(r) {
		s.writeError(w, http.StatusForbidden, "Vault browser origin required")
		return
	}
	s.handleDeviceToken(w, r.WithContext(drive.WithVaultBrowserDevice(r.Context())))
}

func (s *Server) vaultDevice(handle func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		browser := s.vaultBrowserOrigin(r)
		if !strings.HasPrefix(token, drive.DevicePrefix) || len(r.Header.Values("Origin")) != 0 && !browser {
			s.writeError(w, http.StatusUnauthorized, "Signed device credential required")
			return
		}
		if !s.allowAttempt("vault-ip:"+s.requestIP(r), 240, time.Minute) {
			s.writeError(w, http.StatusTooManyRequests, "Too many vault requests")
			return
		}
		if browser {
			r = r.WithContext(drive.WithVaultBrowserDevice(r.Context()))
		}
		if user, device, ok := s.devicePrincipal(w, r, token); ok {
			ctx := context.WithValue(r.Context(), vaultDeviceContextKey{}, device)
			handle(w, r.WithContext(ctx), user.ID)
		}
	}
}

func (s *Server) vaultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, vault.ErrInvalid), errors.Is(err, drive.ErrInvalid):
		s.writeError(w, http.StatusBadRequest, "Invalid vault request")
	case errors.Is(err, vault.ErrDenied), errors.Is(err, drive.ErrDenied):
		s.writeError(w, http.StatusForbidden, "Vault access denied")
	case errors.Is(err, vault.ErrMissing):
		s.writeError(w, http.StatusNotFound, "Not found")
	case errors.Is(err, drive.ErrQuota):
		s.writeError(w, http.StatusRequestEntityTooLarge, "Vault size or quota exceeded")
	case errors.Is(err, vault.ErrConflict), errors.Is(err, drive.ErrConflict):
		s.writeError(w, http.StatusConflict, "Vault state changed")
	default:
		s.writeError(w, http.StatusServiceUnavailable, "Vault unavailable")
	}
}

func (s *Server) enrollVaultKey(w http.ResponseWriter, r *http.Request, user string) {
	var key drive.VaultKey
	if vault.Decode(r.Body, &key) != nil {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	if err := s.store.Drive().EnrollVaultKey(r.Context(), user, key); err != nil {
		s.vaultError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) vaultUserKey(w http.ResponseWriter, r *http.Request, user string) {
	target := r.PathValue("user")
	if !vault.ValidUserID(target) {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	key, err := s.store.Drive().VaultUserKey(r.Context(), user, target)
	if err != nil {
		s.vaultError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, key)
}

func (s *Server) commitVaultRecord(w http.ResponseWriter, r *http.Request, user string) {
	var record vault.Record
	if vault.Decode(r.Body, &record) != nil || record.Vault != r.PathValue("id") {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	head, err := s.store.Drive().CommitVaultRecord(r.Context(), user, record)
	if err != nil {
		s.vaultError(w, err)
		return
	}
	receipt, err := vault.SignReceipt(s.config.Vault.Publisher, head.Log, record.Link())
	if err != nil {
		s.vaultError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, receipt)
}

func (s *Server) vaultRecords(w http.ResponseWriter, r *http.Request, user string) {
	id := r.PathValue("id")
	var after int64
	var err error
	if n := r.URL.Query().Get("after"); n != "" {
		after, err = strconv.ParseInt(n, 10, 64)
	}
	if !vault.ID(id) || err != nil || after < 0 {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	history, err := s.store.Drive().VaultRecords(r.Context(), user, id, after)
	if err != nil {
		s.vaultError(w, err)
		return
	}
	receipts := make([]vault.Receipt, 0, len(history.Records))
	for _, record := range history.Records {
		receipt, err := vault.SignReceipt(s.config.Vault.Publisher, history.Head.Log, record.Link())
		if err != nil {
			s.vaultError(w, err)
			return
		}
		receipts = append(receipts, receipt)
	}
	s.writeJSON(w, http.StatusOK, struct {
		drive.VaultHistory
		Receipts []vault.Receipt `json:"receipts"`
	}{history, receipts})
}

func (s *Server) checkVaultAccess(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		Checkpoint vault.Checkpoint `json:"checkpoint"`
		Edit       bool             `json:"edit"`
	}
	id := r.PathValue("id")
	if !vault.ID(id) || vault.Decode(r.Body, &in) != nil {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	err := s.store.Drive().CheckVaultAccess(r.Context(), user, id, in.Checkpoint, s.config.Vault.Witness, in.Edit, s.now)
	if err != nil {
		s.vaultError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
