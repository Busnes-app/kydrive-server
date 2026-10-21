package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

const uploadLimit int64 = 128 << 20

func (s *Server) driveRoutes() {
	s.mux.HandleFunc("GET /api/drive/status", s.requireAdmin(s.driveStatus))
	s.mux.HandleFunc("GET /api/drive/workspaces/{id}/directory", s.requireAuthenticated(s.driveWorkspaceDirectory))
	s.mux.HandleFunc("GET /editor.html", s.requireAuthenticated(s.editorPage))
	s.mux.HandleFunc("GET /editor-bootstrap.js", s.editorScript)
	s.mux.HandleFunc("POST /api/drive/workspaces/{id}/tokens", s.requireAuthenticated(s.driveCreateToken))
	s.mux.HandleFunc("DELETE /api/drive/tokens/{id}", s.requireAuthenticated(s.driveRevokeToken))
	s.mux.HandleFunc("POST /api/drive/personal-workspace", s.requireAuthenticated(s.drivePersonalWorkspace))
	s.mux.HandleFunc("POST /api/drive/workspaces/{id}/documents", s.requireAuthenticated(s.driveNewDocument))
	s.mux.HandleFunc("GET /api/drive/workspaces", s.requireAuthenticated(s.driveWorkspaces))
	s.mux.HandleFunc("POST /api/drive/workspaces", s.requireAdmin(s.driveCreateWorkspace))
	s.mux.HandleFunc("GET /api/drive/directory", s.requireAdmin(s.driveDirectory))
	s.mux.HandleFunc("GET /api/drive/audit", s.requireAdmin(s.driveAudit))
	s.mux.HandleFunc("GET /api/drive/workspaces/{id}/grants", s.requireAuthenticated(s.driveGrants))
	s.mux.HandleFunc("PUT /api/drive/workspaces/{id}/grants/{group}", s.requireAuthenticated(s.driveGrant))
	s.mux.HandleFunc("PUT /api/drive/workspaces/{id}/quota", s.requireAdmin(s.driveQuota))
	s.mux.HandleFunc("GET /api/drive/workspaces/{id}/folders", s.requireAuthenticated(s.driveFolders))
	s.mux.HandleFunc("POST /api/drive/workspaces/{id}/folders", s.requireAuthenticated(s.driveAddFolder))
	s.mux.HandleFunc("GET /api/drive/workspaces/{id}/files", s.requireAuthenticated(s.driveFiles))
	s.mux.HandleFunc("POST /api/drive/workspaces/{id}/uploads", s.requireAuthenticated(s.driveUpload))
	s.mux.HandleFunc("GET /api/drive/search", s.requireAuthenticated(s.driveSearch))
	s.shareRoutes()
	s.changeRoutes()
	s.guestRoutes()
	s.deviceRoutes()
	s.mux.HandleFunc("POST /api/drive/workspaces/{id}/upload-sessions", s.requireAuthenticated(s.driveStartUpload))
	s.mux.HandleFunc("GET /api/drive/upload-sessions/{sid}", s.requireAuthenticated(s.driveUploadStatus))
	s.mux.HandleFunc("PUT /api/drive/upload-sessions/{sid}", s.requireAuthenticated(s.driveAppendUpload))
	s.mux.HandleFunc("POST /api/drive/upload-sessions/{sid}/complete", s.requireAuthenticated(s.driveCompleteUpload))
	s.mux.HandleFunc("DELETE /api/drive/upload-sessions/{sid}", s.requireAuthenticated(s.driveCancelUpload))
	s.mux.HandleFunc("GET /api/drive/files/{id}", s.requireAuthenticated(s.driveFileInfo))
	s.mux.HandleFunc("GET /api/drive/files/{id}/download", s.requireAuthenticated(s.driveDownload))
	s.mux.HandleFunc("GET /api/drive/files/{id}/preview", s.requireAuthenticated(s.drivePreview))
	s.mux.HandleFunc("GET /api/drive/files/{id}/thumbnail", s.requireAuthenticated(s.driveThumbnail))
	s.mux.HandleFunc("GET /api/drive/files/{id}/versions", s.requireAuthenticated(s.driveVersions))
	s.mux.HandleFunc("PUT /api/drive/files/{id}/trash", s.requireAuthenticated(s.driveTrash))
	s.mux.HandleFunc("POST /api/drive/files/{id}/restore", s.requireAuthenticated(s.driveRestore))
	s.mux.HandleFunc("PUT /api/drive/files/{id}/location", s.requireAuthenticated(s.driveMoveFile))
	s.mux.HandleFunc("POST /api/drive/files/{id}/copy", s.requireAuthenticated(s.driveCopyFile))
	s.mux.HandleFunc("DELETE /api/drive/files/{id}", s.requireAuthenticated(s.drivePurgeFile))
	s.mux.HandleFunc("DELETE /api/drive/files/{id}/versions/{revision}", s.requireAuthenticated(s.drivePurgeVersion))
	s.mux.HandleFunc("PUT /api/drive/folders/{id}/location", s.requireAuthenticated(s.driveMoveFolder))
	s.mux.HandleFunc("PUT /api/drive/folders/{id}/trash", s.requireAuthenticated(s.driveFolderTrash))
	s.mux.HandleFunc("GET /api/drive/folders/{id}/zip", s.requireAuthenticated(s.driveFolderZip))
	s.mux.HandleFunc("DELETE /api/drive/folders/{id}", s.requireAuthenticated(s.drivePurgeFolder))
	s.mux.HandleFunc("PUT /api/drive/workspaces/{id}/retention", s.requireAdmin(s.driveRetention))
	s.mux.HandleFunc("GET /api/drive/trash-retention", s.requireAdmin(s.driveTrashRetention))
	s.mux.HandleFunc("PUT /api/drive/trash-retention", s.requireAdmin(s.driveTrashRetention))
	s.mux.HandleFunc("GET /api/drive/leavers", s.requireAdmin(s.driveLeavers))
	s.mux.HandleFunc("POST /api/drive/leavers/{workspace}/handover", s.requireAdmin(s.driveHandover))
	s.mux.HandleFunc("POST /api/drive/files/{id}/editor", s.requireAuthenticated(s.driveEditor))
	// Only editor-scoped credentials reach these routes; browser sessions grant no access here.
	s.mux.HandleFunc("GET /api/editor/download/{id}", s.editorDownload)
	s.mux.HandleFunc("GET /api/editor/convert/{ticket}", s.editorConvertSource)
	s.mux.HandleFunc("POST /api/editor/callback", s.editorCallback)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.Ping(r.Context()); err != nil {
			s.writeError(w, 503, "Database unavailable")
			return
		}
		clientIP, trusted, scheme := auth.RequestNetwork(r, s.config.Security.TrustedProxies)
		s.writeJSON(w, 200, map[string]any{"service": "kydrive", "status": "ok", "clientIp": clientIP, "proxyHeadersTrusted": trusted, "scheme": scheme})
	})
}

type driveUserKey struct{}

func (s *Server) driveUser(r *http.Request) *store.User {
	return r.Context().Value(driveUserKey{}).(*store.User)
}
func (s *Server) driveError(w http.ResponseWriter, err error) {
	status := 500
	message := "Drive operation failed"
	switch {
	case errors.Is(err, drive.ErrDenied):
		status = 403
		message = "Document access denied"
	case errors.Is(err, drive.ErrNotFound):
		status = 404
		message = "Not found"
	case errors.Is(err, drive.ErrPassword):
		status = 401
		message = "Password required"
	case errors.Is(err, drive.ErrGone):
		status = 410
		message = "Change cursor expired; take a new snapshot"
	case errors.Is(err, drive.ErrInvalid):
		status = 400
		message = "Invalid drive input"
	case errors.Is(err, drive.ErrConflict):
		status = 409
		message = err.Error()
	case errors.Is(err, drive.ErrQuota), errors.As(err, new(*http.MaxBytesError)):
		status = 413
		message = "Upload or workspace quota exceeded"
	}
	s.writeError(w, status, message)
}
func decodeDrive(r *http.Request, out any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return drive.ErrInvalid
	}
	return nil
}
func (s *Server) drivePersonalWorkspace(w http.ResponseWriter, r *http.Request) {
	id, err := s.store.Drive().EnsurePersonalWorkspace(r.Context(), s.driveUser(r).ID)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]string{"id": id})
}
func (s *Server) driveWorkspaces(w http.ResponseWriter, r *http.Request) {
	u := s.driveUser(r)
	out, err := s.store.Drive().Workspaces(r.Context(), u.ID, r.URL.Query().Get("admin") == "true")
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
func (s *Server) driveCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name  string `json:"name"`
		Quota int64  `json:"quota"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().CreateWorkspace(r.Context(), s.driveUser(r).ID, in.Name, in.Quota)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, out)
}
func (s *Server) driveDirectory(w http.ResponseWriter, r *http.Request) {
	groups, _, err := s.store.Groups().ListGroups(r.Context(), 0, 200)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]any{"groups": append([]*store.Group{}, groups...), "identity_url": identityURL(), "authority": "KyIdentity"})
}
func (s *Server) driveAudit(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().Events(r.Context(), s.driveUser(r).ID)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
func (s *Server) driveGrants(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().Grants(r.Context(), s.driveUser(r).ID, r.PathValue("id"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
func (s *Server) driveGrant(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role string `json:"role"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	err := s.store.Drive().Grant(r.Context(), s.driveUser(r).ID, r.PathValue("id"), r.PathValue("group"), in.Role)
	if err != nil {
		s.driveError(w, err)
		return
	}
	if _, err := s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"applied": true})
}
func (s *Server) driveQuota(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Quota int64 `json:"quota"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	if err := s.store.Drive().Quota(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Quota); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"applied": true})
}
func (s *Server) driveFolders(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().Folders(r.Context(), s.driveUser(r).ID, r.PathValue("id"), r.URL.Query().Get("trash") == "true")
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
func (s *Server) driveAddFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().AddFolder(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Parent, in.Name)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, out)
}
func (s *Server) driveFiles(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().Files(r.Context(), s.driveUser(r).ID, r.PathValue("id"), r.URL.Query().Get("trash") == "true")
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}

// driveSearch matches names and, unless content search is off or contents=0, file contents.
// contents reports whether contents were searched and how many of the caller's files are not
// yet indexed.
func (s *Server) driveSearch(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r).ID
	var m drive.ContentMatcher
	cm := s.matcher()
	if cm != nil && r.URL.Query().Get("contents") != "0" {
		m = cm
	}
	hits, truncated, err := s.store.Drive().SearchContent(r.Context(), user, r.URL.Query().Get("q"), 200, m)
	if err != nil {
		s.driveError(w, err)
		return
	}
	contents := map[string]any{"searched": m != nil, "pending": 0}
	var terms []string
	if m != nil {
		terms = cm.terms
		contents["pending"] = cm.pending
	}
	s.writeJSON(w, 200, map[string]any{"results": s.withSnippets(r.Context(), user, hits, terms, snippetCount), "truncated": truncated, "contents": contents})
}
func (s *Server) blobRoot() string { return filepath.Join(s.config.Database.DataDir, "blobs") }

// discardBlob removes a blob that never became a version. A failure is logged; the orphan
// sweep retries it.
func (s *Server) discardBlob(id string) {
	path, err := drive.BlobPath(s.blobRoot(), id)
	if err == nil {
		err = os.Remove(path)
	}
	if err != nil {
		log.Printf("discard blob %s: %v", id, err)
	}
}

// editorConfigLifetime bounds the signed editor configuration, which Euro-Office checks when the
// editor opens; the session row outlives it.
const editorConfigLifetime = 10 * time.Minute

func (s *Server) driveUpload(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r).ID
	f := drive.File{Workspace: r.PathValue("id"), Parent: r.URL.Query().Get("parent"), Name: r.URL.Query().Get("name"), ID: r.URL.Query().Get("file")}
	expected, err := strconv.ParseInt(r.URL.Query().Get("revision"), 10, 64)
	if err != nil || expected < 0 || !drive.ValidName(f.Name) {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	// Refuse a bad mtime before writing the blob; Publish checks again.
	mtime, err := drive.Mtime(r.URL.Query().Get("mtime"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	// Check before writing the blob; Publish checks again. A new version is checked on its file.
	if f.ID == "" {
		err = s.store.Drive().AuthorizeItem(r.Context(), user, f.Workspace, f.Parent, "", 2)
	}
	if err != nil {
		s.driveError(w, err)
		return
	}
	requestedWorkspace := f.Workspace
	if f.ID != "" {
		f, err = s.store.Drive().File(r.Context(), user, f.ID, 2)
		if err != nil {
			s.driveError(w, err)
			return
		}
	}
	if f.Workspace != requestedWorkspace {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	v, err := drive.WriteBlob(s.blobRoot(), s.config.Security.EncryptionKey, r.Body, uploadLimit)
	if err != nil {
		s.driveError(w, err)
		return
	}
	v.Mtime = mtime
	out, err := s.store.Drive().Publish(r.Context(), user, f, v, expected)
	if err != nil {
		s.discardBlob(v.Blob)
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, out)
}

// openBlob opens f's current bytes.
func (s *Server) openBlob(f drive.File) (io.ReadSeekCloser, error) {
	path, err := drive.BlobPath(s.blobRoot(), f.Blob)
	if err != nil {
		return nil, err
	}
	return drive.OpenBlob(path, s.config.Security.EncryptionKey, f.Sealed, f.Size)
}

// openFile opens f's current bytes, answering the request itself when it cannot.
func (s *Server) openFile(w http.ResponseWriter, f drive.File) (io.ReadSeekCloser, bool) {
	file, err := s.openBlob(f)
	if err != nil {
		log.Printf("serve blob %s: %v", f.Blob, err)
		s.writeError(w, 503, "Document bytes unavailable")
		return nil, false
	}
	return file, true
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, f drive.File) {
	file, ok := s.openFile(w, f)
	if !ok {
		return
	}
	defer file.Close()
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Name}))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", `"`+f.Digest+`"`)
	http.ServeContent(w, r, f.Name, time.Time{}, file)
}
func (s *Server) driveFileInfo(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r).ID
	f, err := s.store.Drive().File(r.Context(), user, r.PathValue("id"), 1)
	if err != nil || f.Trashed {
		s.driveError(w, drive.ErrDenied)
		return
	}
	canEdit := s.store.Drive().AuthorizeItem(r.Context(), user, f.Workspace, f.Parent, f.ID, 2) == nil
	s.writeJSON(w, 200, map[string]any{
		"id":        f.ID,
		"workspace": f.Workspace,
		"parent":    f.Parent,
		"name":      f.Name,
		"size":      f.Size,
		"revision":  f.Revision,
		"mtime":     f.Mtime,
		"editable":  canEdit,
	})
}
func (s *Server) driveDownload(w http.ResponseWriter, r *http.Request) {
	f, err := s.store.Drive().File(r.Context(), s.driveUser(r).ID, r.PathValue("id"), 1)
	if err != nil || f.Trashed {
		s.driveError(w, drive.ErrDenied)
		return
	}
	s.serveFile(w, r, f)
}
func (s *Server) driveVersions(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().Versions(r.Context(), s.driveUser(r).ID, r.PathValue("id"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
func (s *Server) driveTrash(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
		Trashed  bool  `json:"trashed"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	err := s.store.Drive().SetTrash(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Revision, in.Trashed)
	if err != nil {
		s.driveError(w, err)
		return
	}
	if _, err := s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"applied": true})
}
func (s *Server) driveRestore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int64 `json:"revision"`
		Expected int64 `json:"expected"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().RestoreVersion(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Revision, in.Expected)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, out)
}
func (s *Server) driveEditor(w http.ResponseWriter, r *http.Request) {
	if s.editor == nil {
		s.writeError(w, 503, "Euro-Office is not configured")
		return
	}
	user := s.driveUser(r)
	f, err := s.store.Drive().File(r.Context(), user.ID, r.PathValue("id"), 1)
	if err != nil || f.Trashed {
		s.driveError(w, drive.ErrDenied)
		return
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(f.Name), "."))
	kind := ""
	switch ext {
	case "docx", "odt", "txt", "rtf", "md":
		kind = "word"
	case "xlsx", "ods", "csv":
		kind = "cell"
	case "pptx", "odp":
		kind = "slide"
	case "pdf":
		kind = "pdf"
	}
	if kind == "" {
		s.writeError(w, 400, "This file format is download-only")
		return
	}
	session, err := s.store.Drive().EditorSession(r.Context(), user.ID, f.ID)
	if err != nil {
		s.driveError(w, err)
		return
	}
	editable := s.store.Drive().AuthorizeItem(r.Context(), user.ID, f.Workspace, f.Parent, f.ID, 2) == nil
	mode := "view"
	if editable {
		mode = "edit"
	}
	cfg := map[string]any{"documentType": kind, "document": map[string]any{"fileType": ext, "key": session.Key, "title": f.Name, "url": s.editor.DriveOrigin + "/api/editor/download/" + session.ID, "permissions": map[string]bool{"edit": editable, "download": true}}, "editorConfig": map[string]any{"mode": mode, "callbackUrl": s.editor.DriveOrigin + "/api/editor/callback", "user": map[string]string{"id": user.ID, "name": user.DisplayName}}, "exp": time.Now().Add(editorConfigLifetime).Unix()}
	signed, err := s.editor.Sign(cfg)
	if err != nil {
		s.driveError(w, err)
		return
	}
	cfg["token"] = signed
	s.writeJSON(w, 200, map[string]any{"origin": s.editor.Origin, "config": cfg})
}

// editorDownload serves Euro-Office only: its outbox JWT must name this exact URL. The URL
// carries no credential, so the browser holding the editor config cannot fetch it.
func (s *Server) editorDownload(w http.ResponseWriter, r *http.Request) {
	if s.editor == nil {
		s.writeError(w, 503, "Euro-Office unavailable")
		return
	}
	id := r.PathValue("id")
	if s.editor.Download(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), s.editor.DriveOrigin+"/api/editor/download/"+id) != nil {
		s.writeError(w, 401, "Invalid editor signature")
		return
	}
	f, err := s.store.Drive().EditorDownload(r.Context(), id)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.serveFile(w, r, f)
}
func (s *Server) editorCallback(w http.ResponseWriter, r *http.Request) {
	if s.editor == nil {
		s.writeError(w, 503, "Euro-Office unavailable")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil {
		s.writeError(w, 400, "Invalid callback")
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = body.Token
	}
	cb, err := s.editor.Callback(token)
	if err != nil {
		s.writeError(w, 401, "Invalid editor signature")
		return
	}
	if cb.Status == 4 {
		if err = s.store.Drive().CloseEditor(r.Context(), cb.Key); err != nil {
			s.driveError(w, err)
			return
		}
	}
	if cb.Status != 2 && cb.Status != 6 {
		s.writeJSON(w, 200, map[string]int{"error": 0})
		return
	}
	id, revision, epoch, err := drive.ParseDocumentKey(cb.Key)
	if err != nil {
		s.driveError(w, err)
		return
	}
	user, err := s.store.Drive().CallbackActor(r.Context(), id, revision, epoch)
	if err != nil {
		s.driveError(w, err)
		return
	}
	f, err := s.store.Drive().File(r.Context(), user, id, 2)
	if err != nil {
		s.driveError(w, err)
		return
	}

	output, err := s.editor.Fetch(r.Context(), cb.URL)
	if err != nil {
		s.writeError(w, 502, "Editor output unavailable")
		return
	}
	defer output.Close()
	v, err := drive.WriteBlob(s.blobRoot(), s.config.Security.EncryptionKey, output, uploadLimit)
	if err != nil {
		s.driveError(w, err)
		return
	}
	duplicate, err := s.store.Drive().PublishEditor(r.Context(), user, f, v, cb.Key, cb.Status == 2)
	if err != nil || duplicate {
		s.discardBlob(v.Blob)
	}
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]int{"error": 0})
}

func (s *Server) driveCreateToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	token := crypto.RandomHex(32)
	id, err := s.store.Drive().CreateServiceToken(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Name, in.Role, token)
	if err != nil {
		s.driveError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, 201, map[string]string{"id": id, "token": token})
}
func (s *Server) driveRevokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Drive().RevokeServiceToken(r.Context(), s.driveUser(r).ID, r.PathValue("id")); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"revoked": true})
}

func identityURL() string {
	if value := os.Getenv("KYDRIVE_IDENTITY_URL"); strings.HasPrefix(value, "https://") {
		return value
	}
	return ""
}

func (s *Server) driveWorkspaceDirectory(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Drive().AuthorizeSharedManager(r.Context(), s.driveUser(r).ID, r.PathValue("id")); err != nil {
		s.driveError(w, err)
		return
	}
	s.driveDirectory(w, r)
}
func (s *Server) driveStatus(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.Drive().RevocationStatus(r.Context())
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]any{"editor_configured": s.editor != nil, "editor_revocations": out, "scim_enabled": s.config.SCIM.Enabled, "identity_authority": "KyIdentity", "bulk_repository_configured": os.Getenv("KYDRIVE_BULK_BACKUP_REPOSITORY") != ""})
}

func (s *Server) driveMoveFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision  int64  `json:"revision"`
		Workspace string `json:"workspace"`
		Parent    string `json:"parent"`
		Name      string `json:"name"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().MoveFile(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Revision, drive.Location{Workspace: in.Workspace, Parent: in.Parent, Name: in.Name})
	if err != nil {
		s.driveError(w, err)
		return
	}
	// A move to another workspace can take away someone's access to an open document.
	if _, err := s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
func (s *Server) driveCopyFile(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Workspace string `json:"workspace"`
		Parent    string `json:"parent"`
		Name      string `json:"name"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().CopyFile(r.Context(), s.driveUser(r).ID, r.PathValue("id"), drive.Location{Workspace: in.Workspace, Parent: in.Parent, Name: in.Name})
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 201, out)
}
func (s *Server) driveMoveFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Parent string `json:"parent"`
		Name   string `json:"name"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	out, err := s.store.Drive().MoveFolder(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Parent, in.Name)
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, out)
}
func (s *Server) driveFolderTrash(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Trashed bool `json:"trashed"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	if err := s.store.Drive().SetFolderTrash(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.Trashed); err != nil {
		s.driveError(w, err)
		return
	}
	if _, err := s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"applied": true})
}
func (s *Server) drivePurgeFile(w http.ResponseWriter, r *http.Request) {
	blobs, err := s.store.Drive().PurgeFile(r.Context(), s.driveUser(r).ID, r.PathValue("id"))
	s.purged(w, blobs, err)
}
func (s *Server) drivePurgeFolder(w http.ResponseWriter, r *http.Request) {
	blobs, err := s.store.Drive().PurgeFolder(r.Context(), s.driveUser(r).ID, r.PathValue("id"))
	s.purged(w, blobs, err)
}
func (s *Server) drivePurgeVersion(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseInt(r.PathValue("revision"), 10, 64)
	if err != nil || revision < 1 {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	blobs, err := s.store.Drive().PurgeVersion(r.Context(), s.driveUser(r).ID, r.PathValue("id"), revision)
	s.purged(w, blobs, err)
}

// purged removes blobs only after their metadata is gone; a failed removal costs disk space,
// never a version without bytes.
func (s *Server) purged(w http.ResponseWriter, blobs []string, err error) {
	if err != nil {
		s.driveError(w, err)
		return
	}
	if err = drive.RemoveBlobs(s.blobRoot(), blobs); err != nil {
		log.Printf("purged blob cleanup: %v", err)
	}
	s.writeJSON(w, 200, map[string]bool{"purged": true})
}
func (s *Server) driveRetention(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TrashDays    int `json:"trash_days"`
		KeepVersions int `json:"keep_versions"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	if err := s.store.Drive().SetRetention(r.Context(), s.driveUser(r).ID, r.PathValue("id"), in.TrashDays, in.KeepVersions); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]bool{"applied": true})
}

// driveLeavers lists leavers' workspaces by size and count, never by content, and the colleagues
// who can receive one. Dedicated installer administrators hold no personal files.
func (s *Server) driveLeavers(w http.ResponseWriter, r *http.Request) {
	user := s.driveUser(r).ID
	leavers, err := s.store.Drive().Leavers(r.Context(), user)
	if err != nil {
		s.driveError(w, err)
		return
	}
	all, err := s.store.Drive().Colleagues(r.Context(), user)
	if err != nil {
		s.driveError(w, err)
		return
	}
	colleagues := []drive.Colleague{}
	for _, c := range all {
		dedicated, err := s.store.InstallerAdministrator(r.Context(), c.ID)
		if err != nil {
			s.driveError(w, err)
			return
		}
		if !dedicated {
			colleagues = append(colleagues, c)
		}
	}
	s.writeJSON(w, 200, map[string]any{"leavers": leavers, "colleagues": colleagues})
}
func (s *Server) driveHandover(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Workspace string `json:"workspace"`
		User      string `json:"user"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	if in.User != "" {
		dedicated, err := s.store.InstallerAdministrator(r.Context(), in.User)
		if err == nil && dedicated {
			err = drive.ErrDenied
		}
		if err != nil {
			s.driveError(w, err)
			return
		}
	}
	folder, err := s.store.Drive().Handover(r.Context(), s.driveUser(r).ID, r.PathValue("workspace"), drive.Target{Workspace: in.Workspace, User: in.User})
	if err != nil {
		s.driveError(w, err)
		return
	}
	if _, err := s.store.Drive().PendingRevocations(r.Context()); err != nil {
		s.driveError(w, err)
		return
	}
	s.writeJSON(w, 200, map[string]string{"folder": folder})
}
