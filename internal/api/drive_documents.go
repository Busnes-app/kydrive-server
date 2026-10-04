package api

import (
	"bytes"
	"embed"
	"net/http"
	"os"
	"strings"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// Blank Office Open XML files contain no scripts, macros or external resources.
//
//go:embed templates/blank.*
var documentTemplates embed.FS

func (s *Server) driveNewDocument(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Parent string `json:"parent"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	extension := map[string]string{"document": "docx", "spreadsheet": "xlsx", "presentation": "pptx"}[in.Kind]
	if extension == "" || !drive.ValidName(in.Name) {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	name := in.Name
	if !strings.HasSuffix(strings.ToLower(name), "."+extension) {
		name += "." + extension
	}
	if !drive.ValidName(name) {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	user, workspace := s.driveUser(r).ID, r.PathValue("id")
	if err := s.store.Drive().Authorize(r.Context(), user, workspace, 2); err != nil {
		s.driveError(w, err)
		return
	}
	data, err := documentTemplates.ReadFile("templates/blank." + extension)
	if err != nil {
		s.driveError(w, err)
		return
	}
	version, err := drive.WriteBlob(s.blobRoot(), bytes.NewReader(data), uploadLimit)
	if err != nil {
		s.driveError(w, err)
		return
	}
	file, err := s.store.Drive().Publish(r.Context(), user, drive.File{Workspace: workspace, Parent: in.Parent, Name: name}, version, 0)
	if err != nil {
		path, _ := drive.BlobPath(s.blobRoot(), version.Blob)
		os.Remove(path)
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, file)
}
