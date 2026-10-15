package dav

import (
	"errors"
	"net/http"
	"strings"

	"golang.org/x/net/webdav"

	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// Serve answers one WebDAV request under prefix with f, the requesting user's file system.
// Callers authenticate first; f carries the user.
func Serve(w http.ResponseWriter, r *http.Request, prefix string, f *FS, locks *Locks) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		// Stored files are user content on the app's origin: never render or run them.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
		w.Header().Set("Content-Disposition", "attachment")
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if r.Method == "LOCK" {
		// x/net reports the timeout it parsed, so cap the request itself (view.Create caps too).
		r.Header.Set("Timeout", lockTimeout(r.Header.Get("Timeout")))
	}
	if r.Body != nil {
		r.Body = f.Body(r.Body)
	}
	if m := r.Header.Get("If-Match"); m != "" && r.Method == http.MethodPut {
		if !matches(r, prefix, f, m) {
			http.Error(w, "Precondition Failed", http.StatusPreconditionFailed)
			return
		}
		f.IfMatch = true
	}
	h := &webdav.Handler{Prefix: prefix, FileSystem: f, LockSystem: locks.view(r.Context(), f, r.Method)}
	h.ServeHTTP(&statusWriter{ResponseWriter: w, fs: f, method: r.Method}, r)
}

// matches reports whether the target's current ETag is one of the If-Match values ("*": any).
func matches(r *http.Request, prefix string, f *FS, header string) bool {
	fi, err := f.Stat(r.Context(), strings.TrimPrefix(r.URL.Path, prefix))
	if err != nil || fi.IsDir() {
		return false
	}
	current := fi.(*fileInfo).etag
	for _, v := range strings.Split(header, ",") {
		if v = strings.TrimSpace(v); v == "*" || v == current {
			return true
		}
	}
	return false
}

// x/net answers most failures with a fixed status (405 for every failed PUT, 404 for a
// refused open); only those are replaced, and only when the drive said why.
type statusWriter struct {
	http.ResponseWriter
	fs     *FS
	method string
}

func (s *statusWriter) WriteHeader(code int) {
	if s.fs.Err != nil && code >= 400 {
		code = status(s.fs.Err, s.method, s.fs.IfMatch, code)
	}
	s.ResponseWriter.WriteHeader(code)
}

func status(err error, method string, ifMatch bool, fallback int) int {
	switch {
	case errors.As(err, new(*http.MaxBytesError)), errors.Is(err, drive.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	case errors.Is(err, drive.ErrQuota):
		return http.StatusInsufficientStorage
	case errors.Is(err, drive.ErrConflict) && method == "MKCOL":
		return http.StatusMethodNotAllowed // RFC 4918: the collection already exists
	case errors.Is(err, drive.ErrConflict) && ifMatch:
		return http.StatusPreconditionFailed
	case errors.Is(err, drive.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, drive.ErrDenied):
		return http.StatusForbidden
	case errors.Is(err, drive.ErrInvalid):
		return http.StatusBadRequest
	}
	return fallback // includes ErrNotFound, which x/net already answers as 404 or 409
}
