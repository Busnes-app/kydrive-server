package api_test

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// Browser-origin hardening, after Joplin's Web Clipper flaw (GHSA-9728-v7ww-mxjv): a pairing
// endpoint with wildcard CORS and no Origin check let any web page mint a full-access token.

const evilOrigin = "https://evil.example"

func corsHeaders(h http.Header) []string {
	var out []string
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			out = append(out, k)
		}
	}
	return out
}

// Requirement 1: /mcp refuses any request that carries an Origin header, whatever its value or
// method and whether or not the token is valid, before the token is even read.
func TestMCPRefusesEveryBrowserOrigin(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := w.token(t, drive.ScopeMCPWrite, "", "")
	for _, origin := range []string{evilOrigin, w.cfg.Server.AppURL, "null", ""} {
		for _, method := range []string{"POST", "GET", "DELETE", "OPTIONS"} {
			for _, auth := range []string{"Bearer " + tok, ""} {
				req := httptest.NewRequest(method, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json, text/event-stream")
				req.Header["Origin"] = []string{origin} // set even when empty
				if auth != "" {
					req.Header.Set("Authorization", auth)
				}
				rec := httptest.NewRecorder()
				w.srv.ServeHTTP(rec, req)
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s with Origin %q, auth %t: got %d, want 403", method, origin, auth != "", rec.Code)
				}
				if h := corsHeaders(rec.Header()); len(h) > 0 {
					t.Errorf("%s with Origin %q: CORS headers %v on /mcp", method, origin, h)
				}
			}
		}
	}
	// Refused before sign-in: none of those counted as failed sign-ins, so the token still works.
	if got := rawMCP(t, w.srv, "/mcp", map[string]string{"Authorization": "Bearer " + tok}).Code; got != http.StatusOK {
		t.Errorf("valid token after 32 browser requests: got %d, want 200", got)
	}
}

// Requirement 2: no response ever grants "*" or a foreign origin; /mcp carries no CORS header at
// all; other routes reflect only the app's own origin. WebDAV, Euro-Office and Excalidraw routes
// included, since a header they set would sit beside /mcp on the same origin.
func TestCORSNeverGrantsAnotherOrigin(t *testing.T) {
	w := newMCPWorld(t)
	_, tok := w.token(t, drive.ScopeMCPRead, "", "")
	routes := []struct{ method, path string }{
		{"POST", "/mcp"}, {"OPTIONS", "/mcp"},
		{"PROPFIND", "/dav/"}, {"OPTIONS", "/dav/"}, {"GET", "/dav/Team/x"},
		{"GET", "/editor.html"}, {"GET", "/editor-bootstrap.js"}, {"POST", "/api/editor/callback"},
		{"GET", "/api/editor/download/x"}, {"GET", "/api/editor/convert/x"},
		{"GET", "/excalidraw/fonts/Xiaolai/x.woff2"}, {"GET", "/whiteboard.html"},
		{"GET", "/api/drive/workspaces"}, {"OPTIONS", "/api/auth/app-passwords"}, {"POST", "/api/auth/app-passwords"},
		{"GET", "/agent/skills/kydrive/SKILL.md"}, {"GET", "/"}, {"GET", "/healthz"},
	}
	for _, rt := range routes {
		for _, origin := range []string{"", evilOrigin, "null", w.cfg.Server.AppURL} {
			req := httptest.NewRequest(rt.method, rt.path, strings.NewReader("{}"))
			if origin != "" {
				req.Header.Set("Origin", origin)
			}
			if rt.path == "/mcp" {
				req.Header.Set("Authorization", "Bearer "+tok)
			}
			rec := httptest.NewRecorder()
			w.srv.ServeHTTP(rec, req)
			acao := rec.Header().Get("Access-Control-Allow-Origin")
			switch {
			case rt.path == "/mcp":
				if h := corsHeaders(rec.Header()); len(h) > 0 {
					t.Errorf("%s %s Origin %q: CORS headers %v", rt.method, rt.path, origin, h)
				}
			case acao == "*" || (acao != "" && acao != w.cfg.Server.AppURL):
				t.Errorf("%s %s Origin %q: Access-Control-Allow-Origin %q", rt.method, rt.path, origin, acao)
			case origin == w.cfg.Server.AppURL && acao != origin:
				t.Errorf("%s %s: the app's own origin not reflected (%q)", rt.method, rt.path, acao)
			case rec.Header().Get("Access-Control-Allow-Credentials") != "" && acao == "":
				t.Errorf("%s %s Origin %q: credentials allowed without an origin grant", rt.method, rt.path, origin)
			}
			if rt.path != "/mcp" && !strings.Contains(rec.Header().Get("Vary"), "Origin") {
				t.Errorf("%s %s: no Vary: Origin, so a cache could mix origins", rt.method, rt.path)
			}
		}
	}
	// Only ServeHTTP may set CORS headers: a new source must be reviewed against this test.
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "dist") {
			return skipDir(d, err)
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), "Access-Control-Allow") && filepath.ToSlash(p) != "../../internal/api/server.go" {
			t.Errorf("%s sets CORS headers; only ServeHTTP may", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func skipDir(d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	return fs.SkipDir
}

// Requirement 3: creating, re-scoping and revoking an agent token need a session and its CSRF
// token, and a cross-site request is refused even with both: a forged POST from another site
// mints nothing, widens nothing and revokes nothing.
func TestAgentTokenWritesRefuseForgedCrossSiteRequests(t *testing.T) {
	w := newMCPWorld(t)
	ctx := context.Background()
	id, tok := w.token(t, drive.ScopeMCPRead, "", "")
	_, davSecret, err := w.st.Drive().CreateAppPassword(ctx, "usr_alice", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	writes := []struct{ method, path, body string }{
		{"POST", "/api/auth/app-passwords", `{"name":"Evil","scope":"mcp:write"}`},
		{"PATCH", "/api/auth/app-passwords/" + id, `{"scope":"mcp:write"}`},
		{"DELETE", "/api/auth/app-passwords/" + id, ""},
	}
	forge := func(method, path, body string, set func(*http.Request)) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		set(req)
		rec := httptest.NewRecorder()
		w.srv.ServeHTTP(rec, req)
		return rec.Code
	}
	session := func(r *http.Request) { r.AddCookie(w.alice); api.SignCSRFForTest(w.srv, r) }
	for _, c := range writes {
		for name, attack := range map[string]struct {
			set  func(*http.Request)
			want int
		}{
			"cross-site Origin with session and CSRF": {func(r *http.Request) { session(r); r.Header.Set("Origin", evilOrigin) }, 403},
			"null Origin with session and CSRF":       {func(r *http.Request) { session(r); r.Header.Set("Origin", "null") }, 403},
			"Sec-Fetch-Site cross-site":               {func(r *http.Request) { session(r); r.Header.Set("Sec-Fetch-Site", "cross-site") }, 403},
			"Sec-Fetch-Site same-site":                {func(r *http.Request) { session(r); r.Header.Set("Sec-Fetch-Site", "same-site") }, 403},
			"session without CSRF (a forged form)": {func(r *http.Request) {
				r.AddCookie(w.alice)
				r.Header.Set("Content-Type", "text/plain")
			}, 403},
			"session with a planted CSRF cookie": {func(r *http.Request) {
				r.AddCookie(w.alice)
				r.AddCookie(&http.Cookie{Name: "ky_csrf", Value: "planted"})
				r.Header.Set("X-CSRF-Token", "planted")
			}, 403},
			"agent token, no session":     {func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }, 401},
			"WebDAV password, no session": {func(r *http.Request) { r.SetBasicAuth("alice", davSecret) }, 401},
		} {
			if got := forge(c.method, c.path, c.body, attack.set); got != attack.want {
				t.Errorf("%s %s, %s: got %d, want %d", c.method, c.path, name, got, attack.want)
			}
		}
		// The preflight a page would need first gets no grant.
		req := httptest.NewRequest("OPTIONS", c.path, nil)
		req.Header.Set("Origin", evilOrigin)
		req.Header.Set("Access-Control-Request-Method", c.method)
		rec := httptest.NewRecorder()
		w.srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("preflight for %s %s: %d, ACAO %q", c.method, c.path, rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
	list, err := w.st.Drive().AppPasswords(ctx, "usr_alice")
	if err != nil || len(list) != 2 || list[0].ID != id || list[0].Scope != drive.ScopeMCPRead {
		t.Fatalf("a forged request changed the tokens: %+v %v", list, err)
	}
	// The same requests from the app itself, with its session and CSRF token, succeed.
	for _, c := range writes {
		if got := forge(c.method, c.path, c.body, func(r *http.Request) { session(r); r.Header.Set("Origin", w.cfg.Server.AppURL) }); got/100 != 2 {
			t.Errorf("%s %s from the app: got %d", c.method, c.path, got)
		}
	}
}
