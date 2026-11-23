package api_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/objects"
)

func exportStatus(t *testing.T, m *multiAPI, status string) {
	t.Helper()
	if _, err := m.db.Exec(`UPDATE drive_orgs SET status=?,enabled=1 WHERE id='org-a'`, status); err != nil {
		t.Fatal(err)
	}
}

func TestExportOnlySessionAndPersonalArchive(t *testing.T) {
	m, f := multiSSO(t)
	ctx := context.Background()
	f.org = "org-a"
	normal := sessionCookie(ssoLogin(t, m.srv, f, "alice", "normal"))
	if normal == nil {
		t.Fatal("ordinary login failed")
	}
	alice, err := m.st.Users().GetUserBySSO(ctx, "kysignon", "alice")
	if err != nil {
		t.Fatal(err)
	}
	d := m.st.Drive()
	for _, restricted := range []context.Context{
		drive.WithDevice(ctx), drive.WithPeer(ctx, drive.Peer{}), drive.WithServiceAccess(ctx, drive.ServiceAccess{}),
	} {
		if _, err := d.PersonalExport(restricted, alice.ID, 1024); !errors.Is(err, drive.ErrDenied) {
			t.Fatalf("narrowed context reached personal export: %v", err)
		}
	}
	ws, err := d.EnsurePersonalWorkspace(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	folder, err := d.AddFolder(ctx, alice.ID, ws, "", "Reports")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.AddFolder(ctx, alice.ID, ws, folder.ID, "Empty"); err != nil {
		t.Fatal(err)
	}
	publish := func(file drive.File, text string, revision int64) drive.File {
		t.Helper()
		v, err := drive.WriteBlob(objects.Dir(filepath.Join(m.cfg.Database.DataDir, "blobs")), m.cfg.Security.EncryptionKey, strings.NewReader(text), 1024)
		if err != nil {
			t.Fatal(err)
		}
		out, err := d.Publish(ctx, alice.ID, file, v, revision)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	file := publish(drive.File{Workspace: ws, Parent: folder.ID, Name: "résumé.txt"}, "old own bytes", 0)
	file = publish(file, "new own bytes", 1)
	trash := publish(drive.File{Workspace: ws, Name: "résumé.txt"}, "trashed own bytes", 0)
	if err := d.SetTrash(ctx, alice.ID, trash.ID, trash.Revision, true); err != nil {
		t.Fatal(err)
	}
	// Granting the member another person's file must not put it in their personal export.
	if _, err := d.Share(ctx, "a1", "file", m.ids["org-a"]["file"], "user", alice.ID, "reader"); err != nil {
		t.Fatal(err)
	}
	if w := do(t, m.srv, "GET", "/api/export", normal); w.Code != 200 {
		t.Fatalf("active member's export: %d", w.Code)
	}
	// A normal session is ended by scheduled deletion; the verified claim is required.
	exportStatus(t, m, "deletion_scheduled")
	if w := do(t, m.srv, "GET", "/api/drive/workspaces", normal); w.Code != 401 {
		t.Fatalf("ordinary session survived: %d", w.Code)
	}
	if w := ssoLogin(t, m.srv, f, "alice", "ordinary-scheduled"); w.Code != 403 || sessionCookie(w) != nil {
		t.Fatalf("ordinary sign-in during deletion: %d", w.Code)
	}
	f.exportOnly = true
	w := ssoLogin(t, m.srv, f, "alice", "export")
	cookie := sessionCookie(w)
	if w.Code != 302 || cookie == nil || w.Header().Get("Location") != "/api/export" {
		t.Fatalf("export login: %d %s", w.Code, w.Body)
	}
	sess, err := m.st.Sessions().GetSession(ctx, crypto.SHA256Hex([]byte(cookie.Value)))
	if err != nil || !sess.ExportOnly || sess.SSOSID != "export" || !sess.StepUpAt.IsZero() || time.Until(sess.ExpiresAt) > time.Hour || time.Until(sess.ExpiresAt) < 59*time.Minute {
		t.Fatalf("export session: %+v, %v", sess, err)
	}
	if w := do(t, m.srv, "GET", "/api/auth/me", cookie); !bytes.Contains(w.Body.Bytes(), []byte(`"export_only":true`)) {
		t.Fatalf("me: %d %s", w.Code, w.Body)
	}
	// Client-supplied org/workspace IDs never influence this selection.
	w = send(t, m.srv, "GET", "/api/export?org_id=org-b&workspace="+m.ids["org-b"]["workspace"], "", cookie, "")
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("export: %d %s", w.Code, w.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, entry := range zr.File {
		in, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(in)
		in.Close()
		if err != nil {
			t.Fatal(err)
		}
		got[entry.Name] = string(b)
	}
	want := map[string]string{
		fmt.Sprintf("files/%s/1/résumé.txt", file.ID):  "old own bytes",
		fmt.Sprintf("files/%s/2/résumé.txt", file.ID):  "new own bytes",
		fmt.Sprintf("files/%s/1/résumé.txt", trash.ID): "trashed own bytes",
	}
	for path, text := range want {
		if got[path] != text {
			t.Errorf("%s = %q, want %q", path, got[path], text)
		}
	}
	var manifest drive.PersonalExport
	if err := json.Unmarshal([]byte(got["manifest.json"]), &manifest); err != nil || len(manifest.Folders) != 2 || len(manifest.Files) != 2 || len(manifest.Versions) != 3 || len(got) != 4 {
		t.Fatalf("manifest: %+v, %v; entries %v", manifest, err, got)
	}
	for _, file := range manifest.Files {
		if file.Workspace != ws || file.Trashed != (file.ID == trash.ID) {
			t.Fatalf("wrong file: %+v", file)
		}
	}
	// Authentication refuses every ordinary read and write even with otherwise valid IDs.
	for _, path := range []string{
		"/api/drive/workspaces", "/api/drive/search", "/api/suite/apps", "/api/settings",
		"/api/drive/files/" + file.ID + "/download", "/api/drive/files/" + m.ids["org-b"]["file"] + "/download", "/api/drive/files/missing/download",
		"/api/sso/kysignon/login?step_up=1", "/editor.html", "/dav/", "/mcp",
	} {
		if w := do(t, m.srv, "GET", path, cookie); w.Code < 400 && path != "/api/settings" {
			t.Errorf("GET %s: %d %s", path, w.Code, w.Body)
		} else if path == "/api/settings" && strings.Contains(w.Body.String(), "db_driver") {
			t.Fatal("private settings exposed")
		}
	}
	for _, path := range []string{
		"/api/drive/workspaces/" + ws + "/uploads", "/api/drive/workspaces/" + ws + "/tokens",
		"/api/drive/files/" + file.ID + "/editor", "/api/auth/app-passwords", "/api/devices/approve", "/api/auth/step-up", "/api/export",
	} {
		if w := send(t, m.srv, "POST", path, `{}`, cookie, ""); w.Code < 400 {
			t.Errorf("POST %s: %d %s", path, w.Code, w.Body)
		}
	}
	missing := do(t, m.srv, "GET", "/api/drive/files/missing/download", cookie)
	foreign := do(t, m.srv, "GET", "/api/drive/files/"+m.ids["org-b"]["file"]+"/download", cookie)
	if missing.Code != foreign.Code || missing.Body.String() != foreign.Body.String() {
		t.Fatal("wrong-org ID answered differently from missing")
	}
	m.cfg.Drive.MaxZipSize = 1
	if w := do(t, m.srv, "GET", "/api/export", cookie); w.Code != 413 {
		t.Fatalf("export cap: %d", w.Code)
	}
	m.cfg.Drive.MaxZipSize = 1 << 30
	if w := send(t, m.srv, "POST", "/api/auth/logout", `{}`, cookie, ""); w.Code != 200 {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	if w := do(t, m.srv, "GET", "/api/export", cookie); w.Code != 401 {
		t.Fatalf("logged-out export: %d", w.Code)
	}
	// A missing version must cut the response, never finish a valid partial archive.
	cookie = sessionCookie(ssoLogin(t, m.srv, f, "alice", "broken-export"))
	if cookie == nil {
		t.Fatal("no export session")
	}
	var blob string
	if err := m.db.QueryRow(`SELECT blob FROM drive_versions WHERE file_id=? AND revision=1`, file.ID).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(m.cfg.Database.DataDir, "blobs", blob)); err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if err, _ := recover().(error); !errors.Is(err, http.ErrAbortHandler) {
				t.Fatalf("broken export did not abort: %v", err)
			}
		}()
		do(t, m.srv, "GET", "/api/export", cookie)
	}()
}

func TestExportOnlyAdmissionAndLifecycle(t *testing.T) {
	m, f := multiSSO(t)
	f.org = "org-a"
	if sessionCookie(ssoLogin(t, m.srv, f, "alice", "ordinary")) == nil {
		t.Fatal("ordinary login failed")
	}
	f.exportOnly = true
	for _, status := range []string{"active", "suspended", "deleted"} {
		exportStatus(t, m, status)
		if w := ssoLogin(t, m.srv, f, "alice", status); w.Code != 403 || sessionCookie(w) != nil {
			t.Fatalf("export claim at %s: %d", status, w.Code)
		}
	}
	exportStatus(t, m, "deletion_scheduled")
	for _, c := range [][2]string{{"", "alice"}, {"org-b", "alice"}, {"org-a", "new-person"}} {
		f.org = c[0]
		if w := ssoLogin(t, m.srv, f, c[1], "refused"); w.Code != 403 || sessionCookie(w) != nil {
			t.Fatalf("%v: %d %s", c, w.Code, w.Body)
		}
	}
	if n := m.count(t, `SELECT COUNT(*) FROM users WHERE sso_subject='new-person'`); n != 0 {
		t.Fatal("export login provisioned an account")
	}
	f.org = "org-a"
	for _, state := range []string{"active", "suspended", "deleted", "disabled", "stale", "expired", "inactive", "logout", "erased"} {
		t.Run(state, func(t *testing.T) {
			exportStatus(t, m, "deletion_scheduled")
			if _, err := m.db.Exec(`UPDATE server_settings SET value='99999999999' WHERE key='orgs_fresh_until'`); err != nil {
				t.Fatal(err)
			}
			w := ssoLogin(t, m.srv, f, "alice", state)
			cookie := sessionCookie(w)
			if w.Code != 302 || cookie == nil {
				t.Fatalf("export sign-in: %d %s", w.Code, w.Body)
			}
			// No My files yet: the archive is empty and exporting creates no workspace.
			if w := do(t, m.srv, "GET", "/api/export", cookie); w.Code != 200 {
				t.Fatalf("empty export: %d", w.Code)
			}
			if n := m.count(t, `SELECT COUNT(*) FROM drive_personal_workspaces p JOIN users u ON u.id=p.owner WHERE u.sso_subject='alice'`); n != 0 {
				t.Fatal("export created a personal workspace")
			}
			var err error
			switch state {
			case "active", "suspended", "deleted":
				exportStatus(t, m, state)
			case "disabled":
				_, err = m.db.Exec(`UPDATE drive_orgs SET enabled=0 WHERE id='org-a'`)
			case "stale":
				_, err = m.db.Exec(`DELETE FROM server_settings WHERE key='orgs_fresh_until'`)
				defer m.st.Settings().SetSetting(context.Background(), "orgs_fresh_until", "99999999999")
			case "expired":
				_, err = m.db.Exec(`UPDATE sessions SET expires_at=? WHERE token_hash=?`, time.Now().Add(-time.Hour), crypto.SHA256Hex([]byte(cookie.Value)))
			case "inactive":
				w = hook(t, m.srv, "user.deleted", scimBody("alice", false, 1, "org-a"))
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
				defer func() {
					_, err := m.db.Exec(`UPDATE users SET status='active' WHERE sso_subject='alice'`)
					if err != nil {
						t.Error(err)
					}
				}()
			case "logout":
				if w := postLogout(m.srv, f.logoutToken(t, "alice", state)); w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
			case "erased":
				w = hook(t, m.srv, "user.erased", scimBody("alice", false, 2, "org-a"))
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if w := do(t, m.srv, "GET", "/api/export", cookie); w.Code != 401 {
				t.Fatalf("%s kept export access: %d %s", state, w.Code, w.Body)
			}
			if state != "expired" && state != "logout" {
				if w := ssoLogin(t, m.srv, f, "alice", "refused-"+state); w.Code != 403 || sessionCookie(w) != nil {
					t.Fatalf("%s admitted a new export session: %d %s", state, w.Code, w.Body)
				}
			}
		})
	}
}

func TestSingleTenantIgnoresExportOnlyClaim(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	f := newFakeKySignOn(t)
	f.org, f.exportOnly = "irrelevant-org", true
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.AutoProvision = f.issuer, "client", true
	srv := api.NewServer(cfg, st)
	w := ssoLogin(t, srv, f, "alice", "single")
	cookie := sessionCookie(w)
	if w.Code != 302 || cookie == nil || w.Header().Get("Location") != "/" {
		t.Fatalf("single-tenant sign-in: %d %s", w.Code, w.Body)
	}
	if w := do(t, srv, "GET", "/api/auth/me", cookie); bytes.Contains(w.Body.Bytes(), []byte("export_only")) || !bytes.Contains(w.Body.Bytes(), []byte(`"authenticated":true`)) {
		t.Fatalf("single me changed: %s", w.Body)
	}
	if w := do(t, srv, "GET", "/api/export", cookie); w.Code != 404 {
		t.Fatalf("single-tenant export: %d", w.Code)
	}
}
