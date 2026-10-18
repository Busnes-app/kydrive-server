package api

import (
	"cmp"
	"net/http"
	"strconv"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

const changePage = 1000

func (s *Server) changeRoutes() {
	s.mux.HandleFunc("GET /api/drive/workspaces/{id}/changes", s.requireAuthenticated(s.driveChanges))
	s.mux.HandleFunc("GET /api/drive/workspaces/{id}/snapshot", s.requireAuthenticated(s.driveSnapshot))
}

// pageLimit reads limit, 1 to changePage, defaulting to changePage.
func pageLimit(r *http.Request) (int, bool) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return changePage, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil && n >= 1 && n <= changePage
}

func (s *Server) driveChanges(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(r)
	after, err := strconv.ParseInt(cmp.Or(r.URL.Query().Get("after"), "0"), 10, 64)
	if !ok || err != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	cs, next, more, err := s.store.Drive().Changes(r.Context(), s.driveUser(r).ID, r.PathValue("id"), after, limit)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]any{"changes": cs, "next": next, "more": more})
}

func (s *Server) driveSnapshot(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(r)
	if !ok {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	folders, files, head, next, err := s.store.Drive().Snapshot(r.Context(), s.driveUser(r).ID, r.PathValue("id"), r.URL.Query().Get("after"), limit)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]any{"folders": folders, "files": files, "head": head, "next": next})
}
