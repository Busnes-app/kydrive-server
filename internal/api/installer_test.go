package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Busnes-app/ky-primitives/applysetup"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/config"
)

func TestInstallerSocketActivationAndAdministratorSeparation(t *testing.T) {
	ctx := context.Background()
	_, st, cfg := setupTestServer(t)
	cfg.Server.AppURL = "https://drive.example.test"
	cfg.SCIM.Enabled = true
	cfg.SCIM.BearerToken = "private-scim-token"
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "client")
	token := filepath.Join(secretDir, "scim")
	if err := os.WriteFile(secret, []byte("private-oidc-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte(cfg.SCIM.BearerToken), 0600); err != nil {
		t.Fatal(err)
	}
	b := applysetup.Common{Schema: applysetup.Schema, Service: "kydrive", SetupID: "271333e2-c221-4601-b249-563b3d828530",
		SSO:      &applysetup.SSO{IssuerURL: "https://identity.example.test", ClientID: "drive", ClientSecretFile: secret, RedirectURI: cfg.Server.AppURL + "/api/sso/kysignon/callback", SCIMTokenFile: token},
		Admins:   []applysetup.Identity{{Issuer: "https://identity.example.test", Subject: "owner-subject", Username: "owner-admin"}},
		Everyday: []applysetup.Identity{{Issuer: "https://identity.example.test", Subject: "daily-subject", Username: "daily"}}}
	raw, _ := json.Marshal(b)
	srv := api.NewServer(cfg, st)
	socket, err := config.InstallerSocket(cfg.Database.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	closeServer, err := applysetup.StartLocal(ctx, socket, srv.ApplyInstaller, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, err := applysetup.CallLocal(ctx, socket, raw, false)
	if err != nil || len(r.Created) != 3 || len(r.Pending) != 1 {
		t.Fatal("staged setup", r, err)
	}
	if cfg.SSO.KySignOnIssuer != "" {
		t.Fatal("runtime snapshot mutated")
	}
	if err = closeServer(); err != nil {
		t.Fatal(err)
	}
	if err = config.ApplyInstallerConfiguration(cfg, b); err != nil {
		t.Fatal(err)
	}
	srv = api.NewServer(cfg, st)
	closeServer, err = applysetup.StartLocal(ctx, socket, srv.ApplyInstaller, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeServer()
	r, err = applysetup.CallLocal(ctx, socket, raw, true)
	if err != nil || len(r.Pending) != 0 || len(r.Present) != 3 {
		t.Fatal("activated check", r, err)
	}
	for _, subject := range []string{"owner-subject", "daily-subject"} {
		u, err := st.Users().GetUserBySSO(ctx, "scim", subject)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		if _, _, err = auth.NewSessionManager(st, cfg.Security).IssueSession(ctx, w, httptest.NewRequest("GET", "/", nil), u); err != nil {
			t.Fatal(err)
		}
		var cookie *http.Cookie
		for _, c := range w.Result().Cookies() {
			if c.Name == auth.SessionCookieName {
				cookie = c
			}
		}
		if cookie == nil {
			t.Fatal("session cookie missing")
		}
		want := http.StatusOK
		if subject == "daily-subject" {
			want = http.StatusForbidden
		}
		if got := do(t, srv, "GET", "/api/drive/status", cookie); got.Code != want {
			t.Fatal("administrator grant", subject, got.Code, got.Body.String())
		}
		if subject == "owner-subject" {
			for _, route := range [][2]string{{"POST", "/api/drive/personal-workspace"}, {"GET", "/api/drive/files/anything/download"}, {"GET", "/editor.html"}, {"POST", "/api/drive/workspaces/anything/tokens"}} {
				if got := do(t, srv, route[0], route[1], cookie); got.Code != http.StatusForbidden {
					t.Fatal("administrator ordinary use", route, got.Code)
				}
			}
			if got := do(t, srv, "GET", "/api/settings", cookie); got.Code != 200 || json.Valid(got.Body.Bytes()) == false {
				t.Fatal("admin settings unavailable")
			}
		}
		// Deprovision through the public SCIM receiver, then re-use the existing
		// session. A dedicated owner is disabled just like an everyday identity.
		body := []byte(`{"schemas":["urn:ietf:params:scim:api:messages:2.0:PatchOp"],"Operations":[{"op":"replace","path":"active","value":false}]}`)
		req := httptest.NewRequest("PATCH", "/scim/v2/Users/"+u.ID, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+cfg.SCIM.BearerToken)
		req.Header.Set("Content-Type", "application/scim+json")
		w = httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatal("SCIM deprovision", w.Code, w.Body.String())
		}
		if got := do(t, srv, "GET", "/api/drive/status", cookie); got.Code != 401 {
			t.Fatal("deprovisioned session survived", subject, got.Code)
		}
	}
}
