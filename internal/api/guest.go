package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// Guest links are off until an administrator turns them on; links last at most max_days.
const (
	guestLinksSetting   = "drive_guest_links"
	guestMaxDaysSetting = "drive_guest_max_days"
)

func (s *Server) guestRoutes() {
	for _, kind := range []string{"files", "folders"} {
		s.mux.HandleFunc("GET /api/drive/"+kind+"/{id}/guest-links", s.requireAuthenticated(s.driveGuestLinks))
		s.mux.HandleFunc("POST /api/drive/"+kind+"/{id}/guest-links", s.requireAuthenticated(s.driveCreateGuestLink))
	}
	s.mux.HandleFunc("GET /api/drive/guest-links-policy", s.requireAuthenticated(s.driveGuestPolicy))
	s.mux.HandleFunc("PUT /api/drive/guest-links-policy", s.requireAdmin(s.driveSetGuestPolicy))
	// Public: no session. The token rides in the body (or a header for raw uploads), never a URL.
	s.mux.HandleFunc("POST /api/public/links/view", s.guestView)
	s.mux.HandleFunc("POST /api/public/links/download", s.guestDownload)
	s.mux.HandleFunc("POST /api/public/links/upload", s.guestUpload)
}

func (s *Server) guestPolicy(ctx context.Context) (bool, int, error) {
	on, err := s.store.Settings().GetSetting(ctx, guestLinksSetting)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return false, 0, err
	}
	days := 30
	if v, err := s.store.Settings().GetSetting(ctx, guestMaxDaysSetting); err == nil {
		if days, err = strconv.Atoi(v); err != nil {
			return false, 0, err
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return false, 0, err
	}
	return on == "on", days, nil
}

func (s *Server) driveGuestPolicy(w http.ResponseWriter, r *http.Request) {
	on, days, err := s.guestPolicy(r.Context())
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]any{"enabled": on, "max_days": days})
}

func (s *Server) driveSetGuestPolicy(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
		MaxDays int   `json:"max_days"`
	}
	if decodeDrive(r, &in) != nil || in.Enabled == nil || in.MaxDays < 1 || in.MaxDays > 365 {
		s.writeError(w, 400, "enabled and max_days (1 to 365) are required")
		return
	}
	value := "off"
	if *in.Enabled {
		value = "on"
	}
	for k, v := range map[string]string{guestLinksSetting: value, guestMaxDaysSetting: strconv.Itoa(in.MaxDays)} {
		if err := s.store.Settings().SetSetting(r.Context(), k, v); err != nil {
			s.driveError(w, err)
			return
		}
	}
	if err := s.store.Audit().LogAudit(r.Context(), &store.AuditRecord{UserID: s.driveUser(r).ID, Action: "admin.guest_links", Resource: "drive", Details: fmt.Sprintf("enabled=%v max_days=%d", *in.Enabled, in.MaxDays), IPAddress: s.requestIP(r)}); err != nil {
		log.Printf("guest link policy audit not recorded: %v", err)
	}
	s.writeJSON(w, 200, map[string]any{"enabled": *in.Enabled, "max_days": in.MaxDays})
}

func (s *Server) driveGuestLinks(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().GuestLinks(r.Context(), s.driveUser(r).ID, resourceKind(r), r.PathValue("id"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

func (s *Server) driveCreateGuestLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Days       int    `json:"days"`
		Password   string `json:"password"`
		UploadOnly bool   `json:"upload_only"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	on, maxDays, err := s.guestPolicy(r.Context())
	if err != nil {
		s.driveError(w, err)
		return
	}
	if !on {
		s.writeError(w, 403, "Guest links are turned off")
		return
	}
	if in.Days < 1 || in.Days > maxDays {
		s.writeError(w, 400, fmt.Sprintf("Guest links last 1 to %d days", maxDays))
		return
	}
	expires := time.Now().Add(time.Duration(in.Days) * 24 * time.Hour).Unix()
	link, token, err := s.store.Drive().CreateGuestLink(r.Context(), s.driveUser(r).ID, resourceKind(r), r.PathValue("id"), expires, in.Password, in.UploadOnly, time.Duration(maxDays)*24*time.Hour)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, map[string]any{"link": link, "url": s.config.Server.AppURL + "/g/#" + token})
}

// guestGate refuses everything while guest links are off, and limits failed attempts: 20 per
// minute per client, and 5 password guesses per 15 minutes per link. Each request reserves its
// attempt before any check and guestSettle refunds it unless it failed, so requests sent at once
// cannot all pass a count that only rises afterwards.
func (s *Server) guestGate(w http.ResponseWriter, r *http.Request, token, secret string) bool {
	on, _, err := s.guestPolicy(r.Context())
	if err != nil || !on {
		http.NotFound(w, r)
		return false
	}
	if !s.allowAttempt("guest:"+s.requestIP(r), 20, time.Minute) || (secret != "" && !s.allowAttempt("guest-pw:"+drive.LinkID(token), 5, 15*time.Minute)) {
		w.Header().Set("Retry-After", "60")
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts")
		return false
	}
	return true
}

// guestSettle refunds what guestGate reserved and err did not spend: an unusable link spends
// the client's attempt, a wrong password the link's as well.
func (s *Server) guestSettle(r *http.Request, token, secret string, err error) {
	if !errors.Is(err, drive.ErrNotFound) && !errors.Is(err, drive.ErrPassword) {
		s.refundAttempt("guest:" + s.requestIP(r))
	}
	if secret != "" && !errors.Is(err, drive.ErrPassword) {
		s.refundAttempt("guest-pw:" + drive.LinkID(token))
	}
}

func (s *Server) guestView(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		Folder   string `json:"folder"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	if !s.guestGate(w, r, in.Token, in.Password) {
		return
	}
	out, err := s.store.Drive().GuestView(r.Context(), in.Token, in.Password, in.Folder)
	s.guestSettle(r, in.Token, in.Password, err)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

// guestDownload is a form POST so the browser downloads natively and the token stays in the body.
func (s *Server) guestDownload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	token, secret := r.PostForm.Get("token"), r.PostForm.Get("password")
	if !s.guestGate(w, r, token, secret) {
		return
	}
	f, err := s.store.Drive().GuestFile(r.Context(), token, secret, r.PostForm.Get("file"))
	s.guestSettle(r, token, secret, err)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.serveFile(w, r, f)
}

// guestUpload takes a raw body like the signed-in upload, with the token and password in headers.
func (s *Server) guestUpload(w http.ResponseWriter, r *http.Request) {
	token, secret := r.Header.Get("X-Link-Token"), r.Header.Get("X-Link-Password")
	if !s.guestGate(w, r, token, secret) {
		return
	}
	// Check the link before taking the bytes; GuestUpload checks it again in its transaction.
	view, err := s.store.Drive().GuestView(r.Context(), token, secret, "")
	if err == nil && !view.UploadOnly {
		err = drive.ErrNotFound
	}
	s.guestSettle(r, token, secret, err)
	if err != nil {
		s.driveError(w, err)
		return
	}
	if !s.allowAttempt("guest-upload:"+s.requestIP(r), 60, time.Hour) {
		s.writeError(w, http.StatusTooManyRequests, "Too many uploads")
		return
	}
	name := r.URL.Query().Get("name")
	if !drive.ValidName(name) {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	v, err := drive.WriteBlob(s.blobRoot(), s.config.Security.EncryptionKey, r.Body, uploadLimit)
	if err != nil {
		s.driveError(w, err)
		return
	}
	if _, err = s.store.Drive().GuestUpload(r.Context(), token, secret, name, v); err != nil {
		s.discardBlob(v.Blob)
		s.driveError(w, err)
		return
	}
	// Not the stored name: a numbered one would tell the guest the folder already held theirs.
	w.WriteHeader(http.StatusCreated)
}
