package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/vault"
)

const (
	vaultManifestHeader   = "Kydrive-Vault-Revision"
	vaultCheckpointHeader = "Kydrive-Vault-Checkpoint"
	vaultHeaderLimit      = 16 << 10
)

func decodeVaultHeader(r *http.Request, name string, out any) error {
	values := r.Header.Values(name)
	if len(values) != 1 || len(values[0]) > vaultHeaderLimit {
		return vault.ErrInvalid
	}
	body, err := base64.RawURLEncoding.DecodeString(values[0])
	if err != nil {
		return vault.ErrInvalid
	}
	return vault.Decode(strings.NewReader(string(body)), out)
}

func (s *Server) writeVaultVersion(w http.ResponseWriter, r *http.Request, user string) {
	var rev vault.Revision
	var cp vault.Checkpoint
	number, err := strconv.ParseInt(r.PathValue("revision"), 10, 64)
	if err != nil || decodeVaultHeader(r, vaultManifestHeader, &rev) != nil ||
		decodeVaultHeader(r, vaultCheckpointHeader, &cp) != nil || rev.Vault != r.PathValue("id") ||
		rev.File != r.PathValue("file") || rev.Number != number || rev.Size != r.ContentLength ||
		rev.Digest != r.Header.Get("KyDrive-Content-SHA256") {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	err = s.store.Drive().WriteVaultVersion(r.Context(), s.blobRoot(), user, rev, cp, s.config.Vault.Witness, r.Body, s.now)
	if err != nil {
		s.vaultError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) readVaultVersion(w http.ResponseWriter, r *http.Request, user string) {
	id, file := r.PathValue("id"), r.PathValue("file")
	var number int64
	var err error
	if value := r.PathValue("revision"); value != "latest" {
		number, err = strconv.ParseInt(value, 10, 64)
		if number < 1 {
			err = vault.ErrInvalid
		}
	}
	if err != nil || !vault.ID(id) || !vault.ID(file) {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	src, manifest, err := s.store.Drive().ReadVaultVersion(r.Context(), s.blobRoot(), user, id, file, number)
	if err != nil {
		s.vaultError(w, err)
		return
	}
	defer src.Close()
	data, _ := json.Marshal(manifest)
	w.Header().Set(vaultManifestHeader, base64.RawURLEncoding.EncodeToString(data))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	http.ServeContent(s.steady(w), r, "", time.Time{}, src)
}

func (s *Server) bindVaultEditor(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		Binding    vault.Binding    `json:"binding"`
		Checkpoint vault.Checkpoint `json:"checkpoint"`
	}
	if vault.Decode(r.Body, &in) != nil || in.Binding.Vault != r.PathValue("id") || in.Binding.File != r.PathValue("file") {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	if err := s.store.Drive().BindVaultEditor(r.Context(), user, in.Binding, in.Checkpoint, s.config.Vault.Witness, s.now); err != nil {
		s.vaultError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) readVaultEditor(w http.ResponseWriter, r *http.Request, user string) {
	binding, err := s.store.Drive().VaultEditorBinding(r.Context(), user, r.PathValue("id"), r.PathValue("file"))
	if err != nil {
		s.vaultError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, binding)
}

type vaultRelaySession struct {
	Org, Vault, File, User, Device, Binding string
	Expires                                 int64
	Edit                                    bool
}

func (s *Server) vaultSessionMAC(data []byte) []byte {
	mac := hmac.New(sha256.New, []byte(s.config.Security.SessionSecret))
	mac.Write([]byte("kydrive-vault-relay-session-v1\x00"))
	mac.Write(data)
	return mac.Sum(nil)
}

func (s *Server) vaultEditorSession(w http.ResponseWriter, r *http.Request, user string) {
	var in struct {
		Checkpoint vault.Checkpoint `json:"checkpoint"`
		Edit       bool             `json:"edit"`
	}
	id, file := r.PathValue("id"), r.PathValue("file")
	if !vault.ID(id) || !vault.ID(file) || vault.Decode(r.Body, &in) != nil {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	d := s.store.Drive()
	if err := d.CheckVaultAccess(r.Context(), user, id, in.Checkpoint, s.config.Vault.Witness, in.Edit, s.now); err != nil {
		s.vaultError(w, err)
		return
	}
	b, err := d.VaultEditorBinding(r.Context(), user, id, file)
	if err != nil {
		s.vaultError(w, err)
		return
	}
	if b.Head != in.Checkpoint.Head {
		s.vaultError(w, vault.ErrConflict)
		return
	}
	device, _ := r.Context().Value(vaultDeviceContextKey{}).(string)
	expires := min(time.UnixMilli(in.Checkpoint.IssuedMS).Add(vault.MaxCheckpointAge).UnixMilli(), s.now().Add(time.Minute).UnixMilli())
	data, _ := json.Marshal(vaultRelaySession{b.Org, id, file, user, device, b.ID(), expires, in.Edit})
	enc := base64.RawURLEncoding
	token := enc.EncodeToString(data) + "." + enc.EncodeToString(s.vaultSessionMAC(data))
	s.writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_ms": expires})
}

func (s *Server) vaultRelayCheck(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Room string `json:"room"`
		Blob string `json:"blob"`
		Edit bool   `json:"edit"`
	}
	bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if r.Header.Get("Origin") != "" || len(bearer) > 2048 || !s.allowAttempt("vault-relay:"+s.requestIP(r), 6000, time.Minute) {
		s.vaultError(w, vault.ErrDenied)
		return
	}
	payload, mac, _ := strings.Cut(bearer, ".")
	data, err := base64.RawURLEncoding.DecodeString(payload)
	got, macErr := base64.RawURLEncoding.DecodeString(mac)
	var session vaultRelaySession
	if err != nil || macErr != nil || !hmac.Equal(got, s.vaultSessionMAC(data)) ||
		vault.Decode(strings.NewReader(string(data)), &session) != nil || s.now().UnixMilli() >= session.Expires {
		s.vaultError(w, vault.ErrDenied)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256)
	if vault.Decode(r.Body, &in) != nil || ((in.Room == "") == (in.Blob == "")) ||
		(in.Room != "" && !vault.ValidRoom(in.Room)) ||
		(in.Blob != "" && !vault.ValidEditorBlob(in.Blob)) {
		s.vaultError(w, vault.ErrInvalid)
		return
	}
	if in.Edit && !session.Edit {
		s.vaultError(w, vault.ErrDenied)
		return
	}
	if err := s.store.Drive().CheckVaultRelay(r.Context(), session.Org, session.Vault, session.File,
		session.User, session.Device, session.Binding, in.Room, in.Blob); err != nil {
		s.vaultError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
