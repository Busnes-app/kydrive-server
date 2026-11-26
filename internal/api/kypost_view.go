package api

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// Viewing copies (docs/KYPOST.md "Viewing copies"): KyPost reserves one with a signed ticket
// request, PUTs the attachment's bytes once, and the person opens it in Euro-Office's view
// mode at /view.html. Only its owner's sign-in opens or closes it; Euro-Office fetches it
// with its own signature, and its callback can close it but never save it.
const (
	kypostEventView = "kypost.drive.view"
	viewPath        = "/api/suite/view/"
)

func (s *Server) viewRoot() objects.Local {
	return objects.Dir(filepath.Join(s.config.Database.DataDir, "view"))
}

func (s *Server) kypostViewTicket(r *http.Request, user *store.User, _ *kypostPeer, body []byte) kypostReply {
	if s.editor == nil {
		return refuse(409, "editor_off")
	}
	var in struct {
		Name string `json:"name"`
		Size *int64 `json:"size"`
	}
	if json.Unmarshal(body, &in) != nil || in.Size == nil {
		return refuse(400, "bad_body")
	}
	token, _, err := s.store.Drive().IssueViewTicket(r.Context(), user.ID, in.Name, *in.Size, s.config.Drive.MaxFileSize)
	switch {
	case errors.Is(err, drive.ErrPeerOff):
		return refuse(409, "sharing_off")
	case errors.Is(err, drive.ErrPeerGone):
		return refuse(404, "kypost_off")
	case errors.Is(err, drive.ErrInvalid):
		return refuse(400, "bad_body")
	case errors.Is(err, drive.ErrTooLarge):
		return refuse(413, "too_large")
	case errors.Is(err, drive.ErrQuota):
		return refuse(507, "quota")
	case errors.Is(err, drive.ErrTicketLimit):
		return refuse(429, "too_many_tickets")
	case errors.Is(err, drive.ErrDenied):
		return refuse(404, "no_user")
	case err != nil:
		log.Printf("kypost view ticket: %v", err)
		return refuse(503, "unavailable")
	}
	return kypostReply{status: 201, body: map[string]any{
		"upload_url": s.config.Server.AppURL + viewPath + token,
		"expires":    time.Now().Add(drive.TicketLife).UTC().Format(time.RFC3339),
	}}
}

// handleViewUpload stores a viewing copy's bytes: exactly its size, in one request. The
// ticket is the only credential and it is spent here. Browsers never call it.
func (s *Server) handleViewUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		s.ticketRefuse(w, http.StatusMethodNotAllowed, "bad_method")
		return
	}
	if !s.allowPerIP(r, "view-upload", 240, time.Minute) {
		w.Header().Set("Retry-After", "60")
		s.ticketRefuse(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	if r.Header.Get("Origin") != "" {
		s.ticketRefuse(w, http.StatusForbidden, "bad_origin")
		return
	}
	p := s.kypost.peer.Load()
	if p == nil {
		s.ticketRefuse(w, http.StatusNotFound, "kypost_off")
		return
	}
	token := strings.TrimPrefix(r.URL.Path, viewPath)
	if len(token) != 43 || strings.Trim(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		s.ticketRefuse(w, http.StatusNotFound, "not_found")
		return
	}
	d := s.store.Drive()
	c, err := d.ViewTicket(r.Context(), token)
	if errors.Is(err, drive.ErrNotFound) {
		s.ticketRefuse(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		log.Printf("view upload: lookup: %v", err)
		s.ticketRefuse(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if !s.allowAttempt("view-upload:"+c.Owner, 60, time.Minute) {
		w.Header().Set("Retry-After", "60")
		s.ticketRefuse(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	if on, err := s.kypostEnabled(r.Context(), c.Owner, p); err != nil || !on {
		s.ticketRefuse(w, http.StatusConflict, "sharing_off")
		return
	}
	if r.ContentLength != c.Size {
		s.ticketRefuse(w, http.StatusBadRequest, "size_mismatch")
		return
	}
	err = d.StoreViewCopy(s.peerContext(r.Context(), p), s.viewRoot(), c, token, http.MaxBytesReader(w, r.Body, c.Size+1))
	switch {
	case errors.Is(err, drive.ErrPeerOff):
		s.ticketRefuse(w, http.StatusConflict, "sharing_off")
	case errors.Is(err, drive.ErrPeerGone):
		s.ticketRefuse(w, http.StatusNotFound, "kypost_off")
	case errors.Is(err, drive.ErrUnsupported):
		s.ticketRefuse(w, http.StatusUnsupportedMediaType, "bad_type")
	case errors.Is(err, drive.ErrInvalid):
		s.ticketRefuse(w, http.StatusBadRequest, "size_mismatch")
	case errors.Is(err, drive.ErrNotFound):
		s.ticketRefuse(w, http.StatusNotFound, "not_found")
	case err != nil:
		log.Printf("view upload %s: %v", c.ID, err)
		s.ticketRefuse(w, http.StatusServiceUnavailable, "unavailable")
	default:
		s.audit(r.Context(), &store.AuditRecord{UserID: c.Owner, Action: "kypost.view_stored", Resource: "view:" + c.ID, Details: "peer=kypost", IPAddress: s.requestIP(r)})
		s.writeJSON(w, http.StatusCreated, map[string]any{"view_url": s.config.Server.AppURL + "/view.html?copy=" + c.ID, "expires": rfc3339(c.Expires)})
	}
}

// viewEditor is the view-mode editor config for the signed-in owner's copy. The signed config
// fixes mode and permissions, so the browser holding it cannot turn it into an editor.
func (s *Server) viewEditor(w http.ResponseWriter, r *http.Request) {
	if s.editor == nil {
		s.writeError(w, 503, "Euro-Office is not configured")
		return
	}
	user := s.driveUser(r)
	c, err := s.store.Drive().ViewCopy(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.writeError(w, 404, "This viewing copy has closed. Open the attachment again from KyPost.")
		return
	}
	ext, kind := drive.ViewKind(c.Name)
	off := map[string]bool{"edit": false, "download": false, "print": false, "comment": false, "review": false, "fillForms": false, "modifyFilter": false, "modifyContentControl": false}
	cfg := map[string]any{
		"documentType": kind,
		"document":     map[string]any{"fileType": ext, "key": drive.ViewKeyPrefix + c.ID, "title": c.Name, "url": s.editor.DriveOrigin + "/api/editor/view/" + c.ID, "permissions": off},
		"editorConfig": map[string]any{"mode": "view", "callbackUrl": s.editor.DriveOrigin + "/api/editor/callback", "user": map[string]string{"id": user.ID, "name": user.DisplayName}},
		"exp":          time.Now().Add(editorConfigLifetime).Unix(),
	}
	signed, err := s.editor.Sign(cfg)
	if err != nil {
		s.driveError(w, err)
		return
	}
	cfg["token"] = signed
	s.audit(r.Context(), &store.AuditRecord{UserID: user.ID, Action: "kypost.view_opened", Resource: "view:" + c.ID, IPAddress: s.requestIP(r)})
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, 200, map[string]any{"origin": s.editor.Origin, "config": cfg})
}

// viewClose deletes the signed-in owner's copy; the viewer page calls it when it closes.
func (s *Server) viewClose(w http.ResponseWriter, r *http.Request) {
	blob, err := s.store.Drive().CloseViewCopy(r.Context(), s.driveUser(r).ID, r.PathValue("id"))
	if err != nil {
		s.driveError(w, err)
		return
	}
	s.removeViewBlob(blob)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeViewBlob(blob string) {
	if blob == "" {
		return
	}
	if err := drive.RemoveBlobs(s.viewRoot(), []string{blob}); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("viewing copy cleanup: %v", err)
	}
}

// viewDownload serves Euro-Office only, as editorDownload does.
func (s *Server) viewDownload(w http.ResponseWriter, r *http.Request) {
	if s.editor == nil {
		s.writeError(w, 503, "Euro-Office unavailable")
		return
	}
	id := r.PathValue("id")
	if s.editor.Download(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), s.editor.DriveOrigin+"/api/editor/view/"+id) != nil {
		s.writeError(w, 401, "Invalid editor signature")
		return
	}
	c, err := s.store.Drive().ViewDownload(r.Context(), id)
	if err != nil {
		s.driveError(w, drive.ErrNotFound)
		return
	}
	key, err := s.store.Drive().UserKeys(r.Context(), c.Owner)
	var file io.ReadSeekCloser
	if err == nil {
		file, err = drive.OpenBlob(s.viewRoot(), c.Blob, key, true, c.Size)
	}
	if err != nil {
		log.Printf("viewing copy %s: %v", c.ID, err)
		s.writeError(w, 503, "Viewing copy unavailable")
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(s.steady(w), r, "", time.Time{}, file)
}

// viewCallback answers Euro-Office for a viewing copy's key: closed deletes it, and a save
// is refused, since a viewing copy has nowhere to be saved.
func (s *Server) viewCallback(w http.ResponseWriter, r *http.Request, key string, status int) {
	switch status {
	case 2, 6:
		log.Printf("editor callback: save refused for viewing copy %s", strings.TrimPrefix(key, drive.ViewKeyPrefix))
		s.writeJSON(w, http.StatusForbidden, map[string]int{"error": 1})
		return
	case 4:
		blob, err := s.store.Drive().ViewClosed(r.Context(), strings.TrimPrefix(key, drive.ViewKeyPrefix))
		if err != nil {
			s.driveError(w, err)
			return
		}
		s.removeViewBlob(blob)
	}
	s.writeJSON(w, 200, map[string]int{"error": 0})
}

func (s *Server) viewPage(w http.ResponseWriter, r *http.Request) {
	s.documentPage(w, "/view-bootstrap.js")
}

// viewScript opens the copy named by ?copy= read-only and closes it when the page goes away.
func (s *Server) viewScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, `(async()=>{const message=document.getElementById('message');try{const id=new URLSearchParams(location.search).get('copy');if(!id)throw Error('No document selected');const c=document.cookie.split('; ');const csrf=decodeURIComponent((c.find(x=>x.startsWith('__Host-ky_csrf='))||c.find(x=>x.startsWith('ky_csrf='))||'').replace(/^(__Host-)?ky_csrf=/,''));const base='/api/drive/view-copies/'+encodeURIComponent(id);const resp=await fetch(base+'/editor',{method:'POST',headers:{'X-CSRF-Token':csrf}});const data=await resp.json();if(!resp.ok)throw Error(data.error||'Viewer unavailable');addEventListener('pagehide',()=>fetch(base+'/close',{method:'POST',keepalive:true,headers:{'X-CSRF-Token':csrf}}),{once:true});const script=document.createElement('script');script.src=data.origin+'/web-apps/apps/api/documents/api.js';await new Promise((resolve,reject)=>{script.onload=resolve;script.onerror=()=>reject(Error('Euro-Office could not be reached'));document.head.append(script)});data.config.events={onError:e=>{message.hidden=false;message.textContent='Euro-Office error: '+JSON.stringify(e.data)}};message.hidden=true;new DocsAPI.DocEditor('editor',data.config);}catch(e){message.textContent=e instanceof Error?e.message:'Viewer unavailable';}})();`)
}
