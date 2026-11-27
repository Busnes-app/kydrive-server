package api

import (
	"context"
	"crypto/ecdsa"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// Linked devices. Approval and management need a cookie session (/api/devices is never a
// Bearer path); the token exchange is public, proven by a computer's PKCE verifier or by
// holding a phone's one-time QR code.
const (
	deviceWipeDefaultSetting = "drive_device_wipe_default"
	deviceWipeLockedSetting  = "drive_device_wipe_locked"
)

func (s *Server) deviceRoutes() {
	s.mux.HandleFunc("POST /api/devices/approve", s.requireAuthenticated(s.requireStepUp(s.handleApproveDevice)))
	s.mux.HandleFunc("POST /api/devices/phone-code", s.requireAuthenticated(s.requireStepUp(s.handlePhoneCode)))
	s.mux.HandleFunc("POST /api/devices/token", s.handleDeviceToken)
	s.mux.HandleFunc("GET /api/devices/tls-pin", s.handleTLSPin)
	s.mux.HandleFunc("GET /api/devices", s.requireAuthenticated(s.handleDevices))
	s.mux.HandleFunc("DELETE /api/devices/{id}", s.requireAuthenticated(s.handleRevokeDevice))
	s.mux.HandleFunc("DELETE /api/devices/self", s.handleUnlinkDevice)
	s.mux.HandleFunc("GET /api/drive/device", s.handleDeviceAccount)
	s.mux.HandleFunc("GET /api/drive/device-policy", s.requireAuthenticated(s.driveDevicePolicy))
	s.mux.HandleFunc("PUT /api/drive/device-policy", s.platformAdmin(s.driveSetDevicePolicy))
}

func (s *Server) handleApproveDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	var in struct {
		Redirect  string `json:"redirect"`
		Challenge string `json:"challenge"`
		State     string `json:"state"`
		Name      string `json:"name"`
		Platform  string `json:"platform"`
	}
	if decodeDrive(r, &in) != nil || in.State == "" || len(in.State) > 256 {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	code, err := s.store.Drive().ApproveLink(r.Context(), user, in.Name, in.Platform, in.Challenge, in.Redirect)
	if err != nil {
		s.driveError(w, err)
		return
	}
	// ApproveLink accepts only a redirect without a query.
	s.writeJSON(w, 200, map[string]string{"redirect": in.Redirect + "?" + url.Values{"code": {code}, "state": {in.State}}.Encode()})
}

// handlePhoneCode answers the link a phone scans as a QR code: this server's address and a
// one-time code, valid 120 seconds.
func (s *Server) handlePhoneCode(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	code, err := s.store.Drive().ApprovePhone(r.Context(), user)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]any{"link": "kydrive://link?" + url.Values{"srv": {s.config.Server.AppURL}, "code": {code}}.Encode(), "expires_in": 120})
}

func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if !s.allowAttempt("device-token:"+s.requestIP(r), 20, time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts")
		return
	}
	var token, id string
	// publicKey is the device's P-256 signing key (SPKI DER, std base64), required: every request
	// the token makes must be signed with it (devicesig.go).
	key, err := base64.StdEncoding.DecodeString(r.PostFormValue("publicKey"))
	if err != nil {
		s.writeError(w, 400, "Invalid device key")
		return
	}
	if r.PostFormValue("grant") == "qr" {
		token, id, err = s.store.Drive().RedeemPhone(r.Context(), r.PostFormValue("code"), r.PostFormValue("name"), r.PostFormValue("platform"), key)
	} else {
		token, id, err = s.store.Drive().RedeemLink(r.Context(), r.PostFormValue("code"), r.PostFormValue("verifier"), key)
	}
	if errors.Is(err, drive.ErrDenied) {
		s.writeError(w, 400, "Invalid or expired code")
		return
	}
	if err != nil {
		s.driveError(w, err)
		return
	}
	user, err := s.store.Users().GetUserByID(r.Context(), id)
	if err != nil {
		s.driveError(w, err)
		return
	}
	// user_id is the account's immutable ID: the client keys its pairing on it, since a username can change.
	// identity_key reaches the device over its pinned channel; it verifies GET /api/devices/tls-pin.
	out, err := s.deviceIdentity(r.Context(), user)
	if err != nil {
		s.driveError(w, err)
		return
	}
	out["token"], out["devices_url"] = token, s.config.Server.AppURL+"/"
	s.writeJSON(w, 200, s.withIdentityKey(out))
}

// handleDeviceAccount tells a linked computer which account its own device token belongs to.
// Only a device token answers; a session or any other credential gets 401.
func (s *Server) handleDeviceAccount(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(token, drive.DevicePrefix) {
		s.writeError(w, 401, "Device credential required")
		return
	}
	user, ok := s.deviceUser(w, r, token)
	if !ok {
		return
	}
	out, err := s.deviceIdentity(r.Context(), user)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, s.withIdentityKey(out))
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	out, err := s.store.Drive().Devices(r.Context(), user)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

// handleRevokeDevice takes ?wipe=true|false; a locked policy forces its default.
func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	wipe, locked, err := s.devicePolicy(r.Context())
	if err != nil {
		s.driveError(w, err)
		return
	}
	if v := r.URL.Query().Get("wipe"); v != "" && !locked {
		if wipe, err = strconv.ParseBool(v); err != nil {
			s.driveError(w, drive.ErrInvalid)
			return
		}
	}
	if err = s.store.Drive().RevokeDevice(r.Context(), user, r.PathValue("id"), wipe); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"wipe": wipe})
}

// handleUnlinkDevice lets a desktop client revoke itself with its own device token, never a
// session or any other credential. The user unlinked on that computer, so it is never told to
// wipe, whatever the request says; a second call gets deviceUser's device_revoked answer.
func (s *Server) handleUnlinkDevice(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(token, drive.DevicePrefix) {
		s.writeError(w, 401, "Device credential required")
		return
	}
	user, ok := s.deviceUser(w, r, token)
	if !ok {
		return
	}
	if err := s.store.Drive().UnlinkDevice(r.Context(), user.ID, token); err != nil {
		s.driveError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// devicePolicy is the drive-wide wipe default (unset: true) and whether it is locked (unset: false).
func (s *Server) devicePolicy(ctx context.Context) (wipeDefault, locked bool, err error) {
	read := func(key string, unset bool) (bool, error) {
		v, err := s.store.Settings().GetSetting(ctx, key)
		if errors.Is(err, store.ErrNotFound) {
			return unset, nil
		}
		if err != nil {
			return false, err
		}
		return strconv.ParseBool(v)
	}
	if wipeDefault, err = read(deviceWipeDefaultSetting, true); err != nil {
		return
	}
	locked, err = read(deviceWipeLockedSetting, false)
	return
}

func (s *Server) driveDevicePolicy(w http.ResponseWriter, r *http.Request) {
	wipe, locked, err := s.devicePolicy(r.Context())
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"wipe_default": wipe, "wipe_locked": locked})
}

func (s *Server) driveSetDevicePolicy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WipeDefault *bool `json:"wipe_default"`
		WipeLocked  *bool `json:"wipe_locked"`
	}
	if decodeDrive(r, &in) != nil || in.WipeDefault == nil || in.WipeLocked == nil {
		s.writeError(w, 400, "wipe_default and wipe_locked are required")
		return
	}
	for k, v := range map[string]bool{deviceWipeDefaultSetting: *in.WipeDefault, deviceWipeLockedSetting: *in.WipeLocked} {
		if err := s.store.Settings().SetSetting(r.Context(), k, strconv.FormatBool(v)); err != nil {
			s.driveError(w, err)
			return
		}
	}
	if err := s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: s.driveUser(r).ID, Action: "admin.device_policy", Resource: "drive", Details: fmt.Sprintf("wipe_default=%v wipe_locked=%v", *in.WipeDefault, *in.WipeLocked), IPAddress: s.requestIP(r)}); err != nil {
		log.Printf("device policy audit not recorded: %v", err)
	}
	s.writeJSON(w, 200, map[string]bool{"wipe_default": *in.WipeDefault, "wipe_locked": *in.WipeLocked})
}

// deviceUser authenticates a device token. A revoked one is told whether to wipe; every
// other failure is a plain 401. Dedicated installer administrators never hold one.
func (s *Server) deviceUser(w http.ResponseWriter, r *http.Request, token string) (*store.User, bool) {
	id, err := s.store.Drive().DeviceUser(r.Context(), token, func(device string, key *ecdsa.PublicKey) error {
		return s.verifyDeviceRequest(r, device, key)
	})
	var revoked drive.ErrDeviceRevoked
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, errDeviceSignature), errors.Is(err, errDeviceBody):
		s.writeError(w, 401, "Invalid device credential")
		return nil, false
	case errors.Is(err, errDeviceKeyRequired):
		s.writeJSON(w, 401, map[string]string{"error": "device_key_required"})
		return nil, false
	case errors.As(err, &tooLarge):
		s.writeError(w, http.StatusRequestEntityTooLarge, "Request body too large")
		return nil, false
	case errors.As(err, &revoked):
		s.writeJSON(w, 401, map[string]any{"error": "device_revoked", "wipe": revoked.Wipe})
		return nil, false
	case errors.Is(err, drive.ErrDenied):
		s.writeError(w, 401, "Invalid device credential")
		return nil, false
	case err != nil:
		log.Printf("device sign-in: %v", err)
		s.writeError(w, 503, "Sign-in unavailable")
		return nil, false
	}
	user, err := s.store.Users().GetUserByID(r.Context(), id)
	if err != nil {
		s.writeError(w, 401, "Invalid device credential")
		return nil, false
	}
	if dedicated, err := s.store.InstallerAdministrator(r.Context(), id); err != nil || dedicated {
		s.writeError(w, 403, "Installer administrators cannot use linked computers")
		return nil, false
	}
	return user, true
}

// deviceIdentity reports the authoritative directory tuple, never a username or local ID
// substituted for sub. Single-tenant local accounts keep their original response shape.
func (s *Server) deviceIdentity(ctx context.Context, u *store.User) (map[string]string, error) {
	out := map[string]string{"user_id": u.ID, "user": u.Username}
	if (u.SSOProvider == "kysignon" || u.SSOProvider == "scim") && u.SSOSubject != "" && s.config.SSO.KySignOnIssuer != "" {
		// oauthFlow verifies against this issuer with trailing slashes removed.
		out["issuer"], out["sub"] = strings.TrimRight(s.config.SSO.KySignOnIssuer, "/"), u.SSOSubject
		if s.multi() {
			org, err := s.store.Drive().UserOrg(ctx, u.ID)
			if err != nil {
				return nil, err
			}
			if org == "" {
				return nil, drive.ErrDenied
			}
			out["org_id"] = org
		}
	}
	return out, nil
}
