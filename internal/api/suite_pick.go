package api

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// The picker (docs/SUITE.md, contract I). Another suite app opens /pick in a popup; the page
// asks GET /api/drive/pick whether it may serve that origin, and the confirming click posts the
// chosen files to POST /api/drive/pick, which makes the links (or records the picks) the page
// then posts to the opener. Under /api/drive/, so dedicated administrators are refused
// (allowInstallerActor) and POST needs the CSRF token.

const pickMaxFiles = 20

func (s *Server) pickRoutes() {
	s.mux.HandleFunc("GET /api/drive/pick", s.requireAuthenticated(s.drivePickCheck))
	s.mux.HandleFunc("POST /api/drive/pick", s.requireAuthenticated(s.drivePick))
}

// pickApp is the suite app id whose https origin is exactly origin, for this user; copy mode
// also needs that origin to be the paired KyPost's and the user's KyPost switch on.
func (s *Server) pickApp(r *http.Request, user *store.User, origin, mode string) (string, int, string) {
	if mode != "link" && mode != "copy" {
		return "", 400, "bad_mode"
	}
	app := ""
	if u, err := url.Parse(origin); err == nil && u.Scheme == "https" && u.Host != "" && origin == u.Scheme+"://"+u.Host {
		for _, a := range s.userSuiteApps(r, user) {
			if v, err := url.Parse(a.URL); err == nil && v.Scheme == "https" && v.Scheme+"://"+v.Host == origin {
				app = a.ID
				break
			}
		}
	}
	if app == "" {
		return "", 403, "bad_origin"
	}
	if mode == "copy" {
		p := s.kypost.peer.Load()
		if p == nil || !sameOrigin(origin, p.URL) {
			return "", 403, "copy_not_allowed"
		}
		on, err := s.kypostEnabled(r.Context(), user.ID, p)
		if err != nil {
			log.Printf("pick: kypost switch: %v", err)
			return "", 503, "unavailable"
		}
		if !on {
			return "", 409, "sharing_off"
		}
	}
	return app, 0, ""
}

func (s *Server) pickRefuse(w http.ResponseWriter, status int, code string) {
	s.writeJSON(w, status, map[string]string{"error": http.StatusText(status), "code": code})
}

func (s *Server) drivePickCheck(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mode := q.Get("mode")
	if mode == "" {
		mode = "link"
	}
	app, status, code := s.pickApp(r, s.driveUser(r), q.Get("origin"), mode)
	if code != "" {
		s.pickRefuse(w, status, code)
		return
	}
	name := app
	for _, a := range s.userSuiteApps(r, s.driveUser(r)) {
		if a.ID == app {
			name = a.Name
		}
	}
	s.writeJSON(w, 200, map[string]string{"app": name, "origin": q.Get("origin"), "mode": mode})
}

func (s *Server) drivePick(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r)
	var in struct {
		Origin string   `json:"origin"`
		Mode   string   `json:"mode"`
		Files  []string `json:"files"`
		Days   *int     `json:"days"`
	}
	if decodeDrive(r, &in) != nil || len(in.Files) == 0 || len(in.Files) > pickMaxFiles {
		s.pickRefuse(w, 400, "bad_body")
		return
	}
	if in.Mode == "" {
		in.Mode = "link"
	}
	if !s.allowAttempt("pick:"+user.ID, 30, time.Minute) {
		w.Header().Set("Retry-After", "60")
		s.pickRefuse(w, 429, "rate_limited")
		return
	}
	app, status, code := s.pickApp(r, user, in.Origin, in.Mode)
	if code != "" {
		s.pickRefuse(w, status, code)
		return
	}
	expires, ok := linkExpiry(s.now(), in.Days)
	if !ok || (in.Mode == "copy" && in.Days != nil) {
		s.pickRefuse(w, 400, "bad_body")
		return
	}
	d := s.store.Drive()
	out := make([]map[string]any, 0, len(in.Files))
	seen := map[string]bool{}
	for _, id := range in.Files {
		f, err := d.File(r.Context(), user.ID, id, 1)
		if err != nil || f.Trashed || seen[id] {
			s.pickRefuse(w, 404, "not_found")
			return
		}
		seen[id] = true
		out = append(out, map[string]any{"name": f.Name, "size": f.Size, "file_id": id})
	}
	var made []string
	var err error
	if in.Mode == "copy" {
		err = d.RecordPicks(r.Context(), user.ID, in.Files)
	} else {
		for _, f := range out {
			var link drive.Link
			var token string
			if link, token, err = d.CreateLink(r.Context(), user.ID, "file", f["file_id"].(string), "reader", expires); err != nil {
				break
			}
			made = append(made, link.ID)
			delete(f, "file_id")
			f["url"], f["expires"] = s.config.Server.AppURL+"/l/#"+token, rfc3339(expires)
		}
	}
	if err != nil {
		// All or nothing: links made before the refusal are revoked.
		for _, id := range made {
			if rerr := d.RevokeLink(r.Context(), user.ID, id); rerr != nil {
				log.Printf("pick: link %s not revoked: %v", id, rerr)
			}
		}
		if errors.Is(err, drive.ErrDenied) || errors.Is(err, drive.ErrNotFound) || errors.Is(err, drive.ErrConflict) {
			s.pickRefuse(w, 404, "not_found")
			return
		}
		s.driveError(w, err)
		return
	}
	for i, id := range in.Files {
		detail := "app=" + app + " mode=" + in.Mode
		if in.Mode == "link" {
			detail += " link=" + made[i]
		}
		s.audit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "suite.pick", Resource: "file:" + id, Details: detail, IPAddress: s.requestIP(r)})
	}
	s.writeJSON(w, 200, map[string]any{"files": out})
}

// linkExpiry is an organisation link's expiry for days as contract C allows them: 1, 7, 30 or
// 0 for none (Unix 0); absent means 30.
func linkExpiry(now time.Time, days *int) (int64, bool) {
	n := 30
	if days != nil {
		n = *days
	}
	switch n {
	case 0:
		return 0, true
	case 1, 7, 30:
		return now.Add(time.Duration(n) * 24 * time.Hour).Unix(), true
	}
	return 0, false
}

// rfc3339 is an expiry for the wire: nil for none.
func rfc3339(expires int64) *string {
	if expires == 0 {
		return nil
	}
	v := time.Unix(expires, 0).UTC().Format(time.RFC3339)
	return &v
}
