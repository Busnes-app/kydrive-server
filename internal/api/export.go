package api

import (
	"archive/zip"
	"encoding/json"
	"log"
	"net/http"
)

// driveExport exports only the signed-in member's personal workspace. This is deliberately
// separate from ordinary Drive routes, whose org-active checks stay closed during deletion.
func (s *Server) driveExport(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().PersonalExport(r.Context(), s.driveUser(r).ID, s.config.Drive.MaxZipSize)
	if err != nil {
		s.driveError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="My-files.zip"`)
	z := zip.NewWriter(s.steady(w))
	manifest, err := z.Create("manifest.json")
	if err == nil {
		err = json.NewEncoder(manifest).Encode(out)
	}
	for _, e := range out.Entries {
		if err != nil {
			break
		}
		// Long exports stop on expiry, logout, erasure or an org lifecycle change too.
		if _, _, err = s.sessions.AuthenticateRequest(r); err == nil {
			err = s.zipEntry(z, e)
		}
	}
	if err != nil {
		log.Printf("personal export interrupted: %v", err)
		panic(http.ErrAbortHandler) // Never return a short but valid archive.
	}
	if err = z.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
}
