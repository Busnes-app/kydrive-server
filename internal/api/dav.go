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

// serveDAV mounts the drive at /dav/ for app passwords over HTTP Basic and linked computers'
// device tokens over Bearer. Cookies authenticate nothing here. Browsers resend cached Basic
// credentials ambiently, so cross-site requests are refused like the API's.
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
	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	ipKey, userKey := "dav:"+s.requestIP(r), s.accountKey("dav-user", username, r)
	// An office network's app-password requests draw on the shared office budget in place of
	// the per-client window; the per-account window below still bounds them. Device tokens have
	// no account window, so they keep the per-client one. The budget is drawn before an account
	// key is made, so it also bounds how many keys one office can mint. A unit stays spent only
	// when the request skipped the per-client window; otherwise it is handed back.
	office := ok && !bearer && s.officeDraw(r, "dav")
	skipped := false
	if s.attemptsExceeded(ipKey, 20) {
		if !office {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "Too many failed sign-ins", http.StatusTooManyRequests)
			return
		}
		skipped = true
		s.countExempt(r)
	}
	unspent := func() {
		if office && !skipped {
			s.refundAttempt("dav:office")
		}
	}
	// The account window, per client, is reserved before the lookup, so concurrent guesses
	// cannot all pass a window none of them has filled yet. An exhausted one answers like a
	// wrong password.
	if ok && !bearer && !s.allowAttempt(userKey, 10, 15*time.Minute) {
		unspent()
		challenge(w)
		return
	}
	var id string
	switch {
	case bearer:
		// A linked computer's device token; deviceUser answers every refusal itself.
		user, ok := s.deviceUser(w, r, token)
		if !ok {
			s.allowAttempt(ipKey, 20, time.Minute)
			return
		}
		id = user.ID
		r = r.WithContext(drive.WithDevice(r.Context()))
	case !ok:
		challenge(w)
		return
	default:
		var err error
		check := s.store.Drive().AppPasswordUser
		if s.appPasswordUser != nil {
			check = s.appPasswordUser
		}
		id, err = check(r.Context(), username, secret)
		if errors.Is(err, drive.ErrDenied) {
			// Only failures count: a mounted client makes hundreds of good requests a minute.
			// The account window already holds this one.
			if !office {
				s.allowAttempt(ipKey, 20, time.Minute)
			} else if !skipped {
				skipped = true
				s.countExempt(r)
			}
			challenge(w)
			return
		}
		s.refundAttempt(userKey)
		unspent()
		if err != nil {
			log.Printf("webdav sign-in: %v", err)
			http.Error(w, "Sign-in unavailable", http.StatusServiceUnavailable)
			return
		}
		if dedicated, err := s.store.InstallerAdministrator(r.Context(), id); err != nil || dedicated {
			http.Error(w, "Installer administrators cannot use WebDAV", http.StatusForbidden)
			return
		}
	}
	limit, mtime := int64(1<<20), ""
	if r.Method == http.MethodPut {
		s.answerAfterBody(w)
		limit = s.config.Drive.MaxFileSize + 1
		var err error
		if mtime, err = dav.Mtime(r.Header.Get("X-OC-Mtime")); err != nil {
			http.Error(w, "Invalid X-OC-Mtime", http.StatusBadRequest)
			return
		}
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
	}
	fs := dav.New(s.store.Drive(), id, s.blobRoot(), s.config.Security.EncryptionKey, s.config.Drive.MaxFileSize, mtime)
	if r.Method == http.MethodGet {
		w = s.steady(w) // a long download is cut only when it stalls
	}
	dav.Serve(w, r, "/dav", fs, s.davLocks)
}

func challenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="KyDrive", charset="UTF-8"`)
	http.Error(w, "App password required", http.StatusUnauthorized)
}
