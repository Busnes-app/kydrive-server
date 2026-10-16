package api

import (
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// previewTypes are the sniffed types a browser may render from the app origin. The bytes
// decide, never the name: HTML, SVG and everything unknown stay download-only.
var previewTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
	"application/pdf": true,
	"audio/mpeg":      true, "audio/wave": true, "application/ogg": true,
	"video/mp4": true, "video/webm": true,
	"text/plain; charset=utf-8": true,
}

// The app frames PDFs, so previews may be framed by it alone; sandbox keeps whatever renders
// from acting as the app.
const previewCSP = "sandbox; default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'self'"

func (s *Server) thumbRoot() string { return filepath.Join(s.config.Database.DataDir, "thumbs") }

// previewHeaders marks a response as inline content that renders sandboxed, for this origin only.
func previewHeaders(w http.ResponseWriter, kind, name string) {
	h := w.Header()
	h.Set("Content-Type", kind)
	h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": name}))
	h.Set("Content-Security-Policy", previewCSP)
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
}

// driveThumbnail serves a PNG thumbnail of a raster image, generating and caching it once per blob.
func (s *Server) driveThumbnail(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.Drive().File(r.Context(), s.driveUser(r).ID, r.PathValue("id"), 1)
	if err != nil || f.Trashed {
		s.driveError(w, drive.ErrDenied)
		return
	}
	key := s.config.Security.EncryptionKey
	thumb, err := drive.ReadThumb(s.thumbRoot(), key, f.Blob)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("thumbnail %s unreadable, regenerating: %v", f.Blob, err)
		}
		file, ok := s.openFile(w, f)
		if !ok {
			return
		}
		thumb, err = drive.Thumbnail(r.Context(), file, f.Size)
		file.Close()
		if errors.Is(err, drive.ErrNoThumbnail) {
			s.writeError(w, http.StatusUnsupportedMediaType, "No thumbnail for this file")
			return
		}
		if err != nil {
			s.driveError(w, err)
			return
		}
		if err = drive.WriteThumb(s.thumbRoot(), key, f.Blob, thumb); err != nil {
			log.Printf("thumbnail %s not cached: %v", f.Blob, err)
		}
	}
	previewHeaders(w, "image/png", f.Name+".png")
	w.Write(thumb)
}

// drivePreview serves a file's current bytes inline when they sniff as an allowlisted type.
func (s *Server) drivePreview(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.Drive().File(r.Context(), s.driveUser(r).ID, r.PathValue("id"), 1)
	if err != nil || f.Trashed {
		s.driveError(w, drive.ErrDenied)
		return
	}
	file, ok := s.openFile(w, f)
	if !ok {
		return
	}
	defer file.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(file, head)
	if err == nil || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		s.writeError(w, 503, "Document bytes unavailable")
		return
	}
	kind := http.DetectContentType(head[:n])
	if !previewTypes[kind] {
		s.writeError(w, http.StatusUnsupportedMediaType, "No preview for this file")
		return
	}
	previewHeaders(w, kind, f.Name)
	w.Header().Set("ETag", `"`+f.Digest+`"`)
	http.ServeContent(w, r, "", time.Time{}, file)
}
