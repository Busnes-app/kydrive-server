package api

import (
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// chunkLimit caps one resumable upload request body.
const chunkLimit int64 = 64 << 20

func (s *Server) stagingRoot() string { return filepath.Join(s.config.Database.DataDir, "staging") }

func (s *Server) driveStartUpload(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Parent   string `json:"parent"`
		Name     string `json:"name"`
		File     string `json:"file"`
		Revision int64  `json:"revision"`
		Size     int64  `json:"size"`
		Mtime    string `json:"mtime"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	u := drive.UploadSession{Workspace: r.PathValue("id"), Parent: in.Parent, Name: in.Name, File: in.File, Expected: in.Revision, Size: in.Size, Mtime: in.Mtime}
	u, err := s.store.Drive().StartUpload(r.Context(), s.driveUser(r).ID, u, s.config.Drive.MaxFileSize)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, u)
}

func (s *Server) driveUploadStatus(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.Drive().Upload(r.Context(), s.driveUser(r).ID, r.PathValue("sid"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, u)
}

// driveAppendUpload takes one chunk as "Content-Range: bytes start-end/total". A conflict
// reports the offset to resume from in Upload-Offset.
func (s *Server) driveAppendUpload(w http.ResponseWriter, r *http.Request) {
	user, id := s.driveUser(r).ID, r.PathValue("sid")
	start, end, total, ok := contentRange(r.Header.Get("Content-Range"))
	if !ok {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	if end-start+1 > chunkLimit {
		s.driveError(w, drive.ErrQuota)
		return
	}
	u, err := s.store.Drive().Upload(r.Context(), user, id)
	if err == nil && total != u.Size {
		err = drive.ErrInvalid
	}
	if err == nil {
		u, err = s.store.Drive().AppendUpload(r.Context(), s.stagingRoot(), s.config.Security.EncryptionKey, user, id, start, end-start+1, r.Body)
	}
	if errors.Is(err, drive.ErrConflict) {
		if u, err = s.store.Drive().Upload(r.Context(), user, id); err == nil {
			w.Header().Set("Upload-Offset", strconv.FormatInt(u.Received, 10))
			err = drive.ErrConflict
		}
	}
	if err != nil {
		s.driveError(w, err)
		return
	}
	w.Header().Set("Upload-Offset", strconv.FormatInt(u.Received, 10))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) driveCompleteUpload(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.Drive().CompleteUpload(r.Context(), s.stagingRoot(), s.blobRoot(), s.config.Security.EncryptionKey, s.driveUser(r).ID, r.PathValue("sid"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, f)
}

func (s *Server) driveCancelUpload(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Drive().CancelUpload(r.Context(), s.stagingRoot(), s.driveUser(r).ID, r.PathValue("sid")); err != nil {
		s.driveError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// contentRange parses "bytes start-end/total" with 0 <= start <= end < total.
func contentRange(h string) (start, end, total int64, ok bool) {
	spec, found := strings.CutPrefix(h, "bytes ")
	rng, size, found2 := strings.Cut(spec, "/")
	first, last, found3 := strings.Cut(rng, "-")
	if !found || !found2 || !found3 {
		return 0, 0, 0, false
	}
	var errs [3]error
	start, errs[0] = strconv.ParseInt(first, 10, 64)
	end, errs[1] = strconv.ParseInt(last, 10, 64)
	total, errs[2] = strconv.ParseInt(size, 10, 64)
	return start, end, total, errors.Join(errs[:]...) == nil && start >= 0 && end >= start && total > end
}
