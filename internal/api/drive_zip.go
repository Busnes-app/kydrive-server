package api

import (
	"archive/zip"
	"io"
	"log"
	"mime"
	"net/http"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// driveFolderZip streams a folder as an uncompressed zip after checking its total size. A read
// failure mid-stream aborts the connection, so the client never gets a short but valid archive.
func (s *Server) driveFolderZip(w http.ResponseWriter, r *http.Request) {
	name, entries, err := s.store.Drive().FolderTree(r.Context(), s.driveUser(r).ID, r.PathValue("id"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	var total int64
	for _, e := range entries {
		total += e.Size
	}
	if total > s.config.Drive.MaxZipSize {
		s.writeError(w, http.StatusRequestEntityTooLarge, "Folder is larger than the zip download limit")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".zip"}))
	w.Header().Set("Cache-Control", "no-store")
	z := zip.NewWriter(w)
	for _, e := range entries {
		if err = s.zipEntry(z, e); err != nil {
			log.Printf("folder zip %s: %v", r.PathValue("id"), err)
			panic(http.ErrAbortHandler)
		}
	}
	if err = z.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
}

func (s *Server) zipEntry(z *zip.Writer, e drive.ZipEntry) error {
	out, err := z.CreateHeader(&zip.FileHeader{Name: e.Path, Method: zip.Store, Modified: e.Modified})
	if err != nil || e.Blob == "" {
		return err
	}
	path, err := drive.BlobPath(s.blobRoot(), e.Blob)
	if err != nil {
		return err
	}
	in, err := drive.OpenBlob(path, s.config.Security.EncryptionKey, e.Sealed, e.Size)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(out, in)
	return err
}
