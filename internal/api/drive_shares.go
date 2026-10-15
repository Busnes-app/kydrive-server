package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

func (s *Server) shareRoutes() {
	for _, kind := range []string{"files", "folders"} {
		s.mux.HandleFunc("GET /api/drive/"+kind+"/{id}/shares", s.requireAuthenticated(s.driveShares))
		s.mux.HandleFunc("PUT /api/drive/"+kind+"/{id}/shares", s.requireAuthenticated(s.driveShare))
		s.mux.HandleFunc("GET /api/drive/"+kind+"/{id}/links", s.requireAuthenticated(s.driveLinks))
		s.mux.HandleFunc("POST /api/drive/"+kind+"/{id}/links", s.requireAuthenticated(s.driveCreateLink))
	}
	s.mux.HandleFunc("DELETE /api/drive/links/{id}", s.requireAuthenticated(s.driveRevokeLink))
	s.mux.HandleFunc("POST /api/drive/links/open", s.requireAuthenticated(s.driveOpenLink))
	s.mux.HandleFunc("DELETE /api/drive/shares/{id}", s.requireAuthenticated(s.driveUnshare))
	s.mux.HandleFunc("GET /api/drive/shared-with-me", s.requireAuthenticated(s.driveSharedWithMe))
	s.mux.HandleFunc("GET /api/drive/people", s.requireAuthenticated(s.drivePeople))
	s.mux.HandleFunc("GET /api/drive/folders/{id}/children", s.requireAuthenticated(s.driveFolderChildren))
	s.mux.HandleFunc("PUT /api/drive/workspaces/{id}/sharing", s.requireAuthenticated(s.driveSharing))
}

// resourceKind maps the route's plural to the store's kind.
func resourceKind(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/api/drive/files/") {
		return "file"
	}
	return "folder"
}

func (s *Server) driveShares(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().Shares(r.Context(), s.driveUser(r).ID, resourceKind(r), r.PathValue("id"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

func (s *Server) driveShare(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PrincipalKind string `json:"principal_kind"`
		PrincipalID   string `json:"principal_id"`
		Role          string `json:"role"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().Share(r.Context(), s.driveUser(r).ID, resourceKind(r), r.PathValue("id"), in.PrincipalKind, in.PrincipalID, in.Role)
	if err != nil {
		s.driveError(w, err)
		return
	}
	// A lowered role can end an open editor's edit rights.
	if _, err = s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

func (s *Server) driveUnshare(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Drive().Unshare(r.Context(), s.driveUser(r).ID, r.PathValue("id")); err != nil {
		s.driveError(w, err)
		return
	}
	if _, err := s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) driveSharedWithMe(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().SharedWithMe(r.Context(), s.driveUser(r).ID)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

func (s *Server) drivePeople(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().People(r.Context(), s.driveUser(r).ID, r.URL.Query().Get("q"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

func (s *Server) driveFolderChildren(w http.ResponseWriter, r *http.Request) {
	folder, folders, files, err := s.store.Drive().FolderChildren(r.Context(), s.driveUser(r).ID, r.PathValue("id"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]any{"folder": folder, "folders": folders, "files": files})
}

func (s *Server) driveSharing(w http.ResponseWriter, r *http.Request) {
	var in struct {
		EditorsShare *bool `json:"editors_share"`
	}
	if decodeDrive(r, &in) != nil || in.EditorsShare == nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	if err := s.store.Drive().SetEditorsShare(r.Context(), s.driveUser(r).ID, r.PathValue("id"), *in.EditorsShare); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"editors_share": *in.EditorsShare})
}

func (s *Server) driveLinks(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().Links(r.Context(), s.driveUser(r).ID, resourceKind(r), r.PathValue("id"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

// driveCreateLink returns the token once, as the fragment of the link's URL: browsers never
// send a fragment to the server, so opening the link cannot leave it in an access log.
func (s *Server) driveCreateLink(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role    string `json:"role"`
		Expires int64  `json:"expires"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	link, token, err := s.store.Drive().CreateLink(r.Context(), s.driveUser(r).ID, resourceKind(r), r.PathValue("id"), in.Role, in.Expires)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, map[string]any{"link": link, "url": s.config.Server.AppURL + "/l/#" + token})
}

func (s *Server) driveRevokeLink(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Drive().RevokeLink(r.Context(), s.driveUser(r).ID, r.PathValue("id")); err != nil {
		s.driveError(w, err)
		return
	}
	if _, err := s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// driveOpenLink takes the token in the body, never the URL. Failed opens are limited per client.
func (s *Server) driveOpenLink(w http.ResponseWriter, r *http.Request) {
	key := "link-open:" + s.requestIP(r)
	if s.attemptsExceeded(key, 30) {
		s.writeError(w, http.StatusTooManyRequests, "Too many invalid links")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	item, err := s.store.Drive().OpenLink(r.Context(), s.driveUser(r).ID, in.Token)
	if errors.Is(err, drive.ErrNotFound) {
		s.allowAttempt(key, 30, time.Minute)
	}
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, item)
}
