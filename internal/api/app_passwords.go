package api

import (
	"cmp"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// App password routes sit under /api/auth, where requireAuthenticated accepts only a cookie
// session: a service token, an app password or an agent token can never mint or list them.

// credentialActor refuses dedicated installer administrators, who never reach drive content,
// on the routes managing a user's own app passwords and linked computers.
func (s *Server) credentialActor(w http.ResponseWriter, r *http.Request) (string, bool) {
	user := s.driveUser(r)
	dedicated, err := s.store.InstallerAdministrator(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, 500, "Administrator separation unavailable")
		return "", false
	}
	if dedicated {
		s.writeError(w, 403, "Installer administrators cannot hold drive credentials")
		return "", false
	}
	return user.ID, true
}

// appPasswordView is a listed credential; an agent token limited to a folder carries its path
// as the user sees it now, or root_missing when that folder is in the trash or gone.
type appPasswordView struct {
	drive.AppPassword
	Root        string `json:"root,omitempty"`
	RootMissing bool   `json:"root_missing,omitempty"`
}

func (s *Server) handleAppPasswords(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	list, err := s.store.Drive().AppPasswords(r.Context(), user)
	if err != nil {
		s.driveError(w, err)
		return
	}
	paths := agentPaths{d: s.store.Drive(), user: user}
	out := make([]appPasswordView, 0, len(list))
	for _, p := range list {
		v := appPasswordView{AppPassword: p}
		if p.Workspace != "" {
			v.Root, err = paths.display(r, p.Workspace, p.Folder)
			v.RootMissing = err != nil
		}
		out = append(out, v)
	}
	s.writeJSON(w, 200, out)
}

// display is the path of folder in workspace from the unscoped top, as an agent would name it.
func (p *agentPaths) display(r *http.Request, workspace, folder string) (string, error) {
	names, err := p.spaces(r.Context())
	if err != nil {
		return "", err
	}
	parts, err := p.d.FolderPath(r.Context(), p.user, workspace, folder)
	if err != nil {
		return "", err
	}
	for name, id := range names {
		if id == workspace {
			return "/" + strings.Join(append([]string{name}, parts...), "/"), nil
		}
	}
	return "", drive.ErrNotFound
}

func (s *Server) handleCreateAppPassword(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
		// Scope is dav (the default: a WebDAV password), mcp:read or mcp:write (an AI agent).
		Scope string `json:"scope"`
		// Folder limits an agent token to one folder, by its path as the agent sees it.
		Folder string `json:"folder"`
	}
	if decodeDrive(r, &in) != nil {
		s.driveError(w, drive.ErrInvalid)
		return
	}
	scope := cmp.Or(in.Scope, drive.ScopeDAV)
	if scope == drive.ScopeDAV {
		if in.Folder != "" {
			s.writeError(w, 400, "Only AI agent tokens can be limited to a folder")
			return
		}
		p, secret, err := s.store.Drive().CreateAppPassword(r.Context(), user, in.Name)
		if err != nil {
			s.driveError(w, err)
			return
		}
		s.writeJSON(w, 201, map[string]any{"id": p.ID, "name": p.Name, "created": p.Created, "scope": p.Scope, "password": secret, "username": s.driveUser(r).Username, "url": s.config.Server.AppURL + "/dav/"})
		return
	}
	if !drive.IsAgentScope(scope) {
		s.writeError(w, 400, "scope must be dav, mcp:read or mcp:write")
		return
	}
	paths := agentPaths{d: s.store.Drive(), user: user}
	var workspace, folder string
	if in.Folder != "" && in.Folder != "/" {
		n, err := paths.resolve(r.Context(), in.Folder)
		if err != nil || n.file != nil || n.workspace == "" {
			s.writeError(w, 400, "Folder not found: give a folder's path, such as /My files/Projects")
			return
		}
		workspace, folder = n.workspace, n.folder
	}
	p, token, err := s.store.Drive().CreateAgentToken(r.Context(), user, in.Name, scope, workspace, folder)
	if err != nil {
		s.driveError(w, err)
		return
	}
	root := ""
	if workspace != "" {
		root, _ = paths.display(r, workspace, folder)
	}
	s.audit(r.Context(), &store.AuditRecord{UserID: user, Action: "app_password.create", Resource: "app_password:" + p.ID, Details: "scope=" + scope + " limited=" + strconv.FormatBool(workspace != ""), IPAddress: s.requestIP(r)})
	s.writeJSON(w, 201, map[string]any{"id": p.ID, "name": p.Name, "created": p.Created, "scope": p.Scope, "password": token, "root": root,
		"revoke_after": p.RevokeAfter, "url": s.config.Server.AppURL + "/mcp", "skill": s.config.Server.AppURL + "/agent/skills/kydrive/SKILL.md"})
}

// handleSetAgentScope moves an agent token between read and write. A WebDAV password never
// becomes an agent token, nor the reverse.
func (s *Server) handleSetAgentScope(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	var in struct {
		Scope string `json:"scope"`
	}
	if decodeDrive(r, &in) != nil || !drive.IsAgentScope(in.Scope) {
		s.writeError(w, 400, "scope must be mcp:read or mcp:write")
		return
	}
	id := r.PathValue("id")
	list, err := s.store.Drive().AppPasswords(r.Context(), user)
	if err != nil {
		s.driveError(w, err)
		return
	}
	i := slices.IndexFunc(list, func(p drive.AppPassword) bool { return p.ID == id })
	if i < 0 {
		s.driveError(w, drive.ErrDenied) // as RevokeAppPassword answers another user's ID
		return
	}
	switch err := s.store.Drive().SetAgentScope(r.Context(), user, id, in.Scope); {
	case errors.Is(err, drive.ErrInvalid):
		s.writeError(w, 400, "Only AI agent tokens have an access level")
		return
	case err != nil:
		s.driveError(w, err)
		return
	}
	p := list[i]
	if p.Scope != in.Scope {
		s.audit(r.Context(), &store.AuditRecord{UserID: user, Action: "app_password.scope_change", Resource: "app_password:" + id, Details: "from=" + p.Scope + " to=" + in.Scope, IPAddress: s.requestIP(r)})
	}
	p.Scope = in.Scope
	s.writeJSON(w, 200, p)
}

func (s *Server) handleRevokeAppPassword(w http.ResponseWriter, r *http.Request) {
	user, ok := s.credentialActor(w, r)
	if !ok {
		return
	}
	if err := s.store.Drive().RevokeAppPassword(r.Context(), user, r.PathValue("id")); err != nil {
		s.driveError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
