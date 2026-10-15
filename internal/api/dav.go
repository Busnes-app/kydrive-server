package api

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/dav"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// serveDAV mounts the drive at /dav/ for app passwords over HTTP Basic. Cookies authenticate
// nothing here. Browsers resend cached Basic credentials ambiently, so cross-site requests
// are refused like the API's.
func (s *Server) serveDAV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.store.Drive() == nil {
		http.NotFound(w, r)
		return
	}
	if !s.sameSiteRequest(r) {
		http.Error(w, "Cross-site request refused", http.StatusForbidden)
		return
	}
	if _, _, scheme := auth.RequestNetwork(r, s.config.Security.TrustedProxies); scheme != "https" && s.config.Server.Environment != "development" {
		http.Error(w, "WebDAV requires HTTPS", http.StatusForbidden)
		return
	}
	username, secret, ok := r.BasicAuth()
	ipKey, userKey := "dav:"+s.requestIP(r), "dav-user:"+strings.ToLower(username)
	if s.attemptsExceeded(ipKey, 20) || (ok && s.attemptsExceeded(userKey, 10)) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too many failed sign-ins", http.StatusTooManyRequests)
		return
	}
	if !ok {
		challenge(w)
		return
	}
	id, err := s.store.Drive().AppPasswordUser(r.Context(), username, secret)
	if errors.Is(err, drive.ErrDenied) {
		// Only failures count: a mounted client makes hundreds of good requests a minute.
		s.allowAttempt(ipKey, 20, time.Minute)
		s.allowAttempt(userKey, 10, 15*time.Minute)
		challenge(w)
		return
	}
	if err != nil {
		log.Printf("webdav sign-in: %v", err)
		http.Error(w, "Sign-in unavailable", http.StatusServiceUnavailable)
		return
	}
	if dedicated, err := s.store.InstallerAdministrator(r.Context(), id); err != nil || dedicated {
		http.Error(w, "Installer administrators cannot use WebDAV", http.StatusForbidden)
		return
	}
	limit := int64(1 << 20)
	if r.Method == http.MethodPut {
		limit = s.config.Drive.MaxFileSize + 1
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	fs := dav.New(s.store.Drive(), id, s.blobRoot(), s.config.Security.EncryptionKey, s.config.Drive.MaxFileSize)
	dav.Serve(w, r, "/dav", fs, s.davLocks)
}

func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="KyDrive", charset="UTF-8"`)
	http.Error(w, "App password required", http.StatusUnauthorized)
}
