package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// Linked computers. Approval and management need a cookie session (/api/devices is never a
// Bearer path); the token exchange is public and proven by the PKCE verifier.
const (
	deviceWipeDefaultSetting = "drive_device_wipe_default"
	deviceWipeLockedSetting  = "drive_device_wipe_locked"
)

func (s *Server) deviceRoutes() {
	s.mux.HandleFunc("POST /api/devices/approve", s.requireAuthenticated(s.handleApproveDevice))
	s.mux.HandleFunc("POST /api/devices/token", s.handleDeviceToken)
	s.mux.HandleFunc("GET /api/devices", s.requireAuthenticated(s.handleDevices))
	s.mux.HandleFunc("DELETE /api/devices/{id}", s.requireAuthenticated(s.handleRevokeDevice))
	s.mux.HandleFunc("GET /api/drive/device-policy", s.requireAuthenticated(s.driveDevicePolicy))
	s.mux.HandleFunc("PUT /api/drive/device-policy", s.requireAdmin(s.driveSetDevicePolicy))
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

func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if !s.allowAttempt("device-token:"+s.requestIP(r), 20, time.Minute) {
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts")
		return
	}
	token, id, err := s.store.Drive().RedeemLink(r.Context(), r.PostFormValue("code"), r.PostFormValue("verifier"))
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
	s.writeJSON(w, 200, map[string]string{"token": token, "user": user.Username, "devices_url": s.config.Server.AppURL + "/"})
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
	id, err := s.store.Drive().DeviceUser(r.Context(), token)
	var revoked drive.ErrDeviceRevoked
	switch {
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
