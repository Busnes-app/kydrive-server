package api

import (
	"context"
	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
	"net/http"
	"strings"
)

// ApplyInstaller compares desired configuration with the server's immutable
// startup snapshot. A committed configuration awaiting restart stays pending.
func (s *Server) ApplyInstaller(ctx context.Context, raw []byte, check bool) (applysetup.Report, error) {
	var b applysetup.Common
	if err := applysetup.DecodeRaw(raw, &b); err != nil {
		return applysetup.Report{}, err
	}
	want := *s.config
	if err := config.ApplyInstallerConfiguration(&want, b); err != nil {
		return applysetup.Report{}, err
	}
	r, err := s.store.ApplyInstaller(ctx, b, check)
	if err != nil {
		return r, err
	}
	if want.SSO != s.config.SSO || want.Backup != s.config.Backup {
		r.Pending = append(r.Pending, applysetup.Object{Kind: "activation", Name: "restart"})
	}
	return r, nil
}

// Dedicated installer administrators can manage shared workspace access, but
// cannot create personal data, read files or obtain an editing/service credential.
func (s *Server) allowInstallerActor(w http.ResponseWriter, r *http.Request, user *store.User) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/drive/") && r.URL.Path != "/editor.html" {
		return true
	}
	dedicated, err := s.store.InstallerAdministrator(r.Context(), user.ID)
	if err != nil {
		s.writeError(w, 500, "Administrator separation unavailable")
		return false
	}
	if !dedicated {
		return true
	}
	switch r.Pattern {
	case "GET /api/drive/workspaces", "GET /api/drive/workspaces/{id}/directory", "GET /api/drive/workspaces/{id}/grants", "PUT /api/drive/workspaces/{id}/grants/{group}", "DELETE /api/drive/tokens/{id}":
		return true
	}
	s.writeError(w, 403, "Use your everyday identity for personal files and editing")
	return false
}
