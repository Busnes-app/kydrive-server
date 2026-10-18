package api

import (
	"net/http"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// App password routes sit under /api/auth, where requireAuthenticated accepts only a cookie
// session: a service token or an app password can never mint or list them.

// credentialActor refuses dedicated installer administrators, who never reach drive content,
// on the routes managing a user's own app passwords and linked computers.
func (s *Server) credentialActor(w http.ResponseWriter, r *http.Request) (string, bool) {
	user := s.driveUser(r)
	dedicated, err := s.store.InstallerAdministrator(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, 500, "Administrator separation unavailable")
		return "", false
	}
	if dedicated {
		s.writeError(w, 403, "Installer administrators cannot hold drive credentials")
		return "", false
	}
	return user.ID, true
}

func (s *Server) handleAppPasswords(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	out, err := s.store.Drive().AppPasswords(r.Context(), user)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

func (s *Server) handleCreateAppPassword(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	p, secret, err := s.store.Drive().CreateAppPassword(r.Context(), user, in.Name)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, map[string]any{"id": p.ID, "name": p.Name, "created": p.Created, "password": secret, "username": s.driveUser(r).Username, "url": s.config.Server.AppURL + "/dav/"})
}

func (s *Server) handleRevokeAppPassword(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	if err := s.store.Drive().RevokeAppPassword(r.Context(), user, r.PathValue("id")); err != nil {
		s.driveError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
