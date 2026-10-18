package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func davRequest(srv *api.Server, method, target, user, secret string, body io.Reader, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, body)
	if user != "" || secret != "" {
		r.SetBasicAuth(user, secret)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

func TestDAVAuthentication(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "davadmin", "admin")
	cookie := loginAs(t, srv, st, "davuser", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "davgroup", DisplayName: "Dav", Members: []string{"usr_davuser"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_davadmin", "Team", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_davadmin", w.ID, "davgroup", "editor"); err != nil {
		t.Fatal(err)
	}
	p, secret, err := st.Drive().CreateAppPassword(ctx, "usr_davuser", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	expect := func(got *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if got.Code != code {
			t.Fatalf("%s: %d, want %d: %s", what, got.Code, code, got.Body.String())
		}
	}

	none := davRequest(srv, "PROPFIND", "/dav/", "", "", nil, "Depth", "1")
	expect(none, 401, "no credentials")
	if !strings.HasPrefix(none.Header().Get("WWW-Authenticate"), "Basic ") {
		t.Fatalf("no challenge: %q", none.Header().Get("WWW-Authenticate"))
	}
	ok := davRequest(srv, "PROPFIND", "/dav/", "DavUser", secret, nil, "Depth", "1")
	expect(ok, 207, "app password")
	if !strings.Contains(ok.Body.String(), "/dav/Team/") {
		t.Fatalf("workspace not listed:\n%s", ok.Body.String())
	}
	expect(davRequest(srv, "PUT", "/dav/Team/a.txt", "davuser", secret, strings.NewReader("hello")), 201, "put")
	dated := davRequest(srv, "PUT", "/dav/Team/a.txt", "davuser", secret, strings.NewReader("hello"), "X-OC-Mtime", "1577934245")
	expect(dated, 201, "put with mtime")
	if dated.Header().Get("X-OC-Mtime") != "accepted" {
		t.Fatalf("mtime not accepted: %q", dated.Header().Get("X-OC-Mtime"))
	}
	expect(davRequest(srv, "PUT", "/dav/Team/a.txt", "davuser", secret, strings.NewReader("hello"), "X-OC-Mtime", "soon"), 400, "bad mtime")
	expect(davRequest(srv, "PROPFIND", "/dav/", "davuser", "wrong-password", nil, "Depth", "0"), 401, "wrong password")
	expect(davRequest(srv, "PROPFIND", "/dav/", "davadmin", secret, nil, "Depth", "0"), 401, "another user's name")

	// A browser session cookie is not a WebDAV credential, and an app password is not an API one.
	cookieOnly := httptest.NewRequest("PROPFIND", "/dav/", nil)
	cookieOnly.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, cookieOnly)
	expect(rec, 401, "cookie on /dav/")
	expect(davRequest(srv, "GET", "/api/drive/workspaces", "davuser", secret, nil), 401, "app password on the API")

	expect(davRequest(srv, "PROPFIND", "/dav/", "davuser", secret, nil, "Origin", "https://evil.example"), 403, "cross-site origin")
	expect(davRequest(srv, "PROPFIND", "/dav/", "davuser", secret, nil, "Sec-Fetch-Site", "cross-site"), 403, "cross-site fetch")

	// Bodies above the upload limit are refused.
	cfg.Drive.MaxFileSize = 4
	expect(davRequest(srv, "PUT", "/dav/Team/big.txt", "davuser", secret, strings.NewReader("too big")), 413, "over the upload limit")
	cfg.Drive.MaxFileSize = 8 << 20

	// Production refuses plain HTTP unless a trusted proxy says the client used HTTPS.
	cfg.Server.Environment = "production"
	expect(davRequest(srv, "PROPFIND", "/dav/", "davuser", secret, nil, "Depth", "0"), 403, "plain http in production")
	expect(davRequest(srv, "PROPFIND", "https://drive.example/dav/", "davuser", secret, nil, "Depth", "0"), 207, "tls in production")
	expect(davRequest(srv, "PROPFIND", "/dav/", "davuser", secret, nil, "X-Forwarded-Proto", "https"), 403, "untrusted forwarded proto")
	cfg.Security.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")} // httptest's peer
	expect(davRequest(srv, "PROPFIND", "/dav/", "davuser", secret, nil, "X-Forwarded-Proto", "https", "Depth", "0"), 207, "trusted proxy https")
	cfg.Security.TrustedProxies = nil
	cfg.Server.Environment = "development"

	// Deactivation stops the password on the next request.
	u, err := st.Users().GetUserByID(ctx, "usr_davuser")
	if err != nil {
		t.Fatal(err)
	}
	u.Status = "inactive"
	if err = st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	expect(davRequest(srv, "PROPFIND", "/dav/", "davuser", secret, nil, "Depth", "0"), 401, "deactivated user")
	u.Status = "active"
	if err = st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	expect(davRequest(srv, "PROPFIND", "/dav/", "davuser", secret, nil, "Depth", "0"), 207, "reactivated user")
	if err = st.Drive().RevokeAppPassword(ctx, "usr_davuser", p.ID); err != nil {
		t.Fatal(err)
	}
	expect(davRequest(srv, "PROPFIND", "/dav/", "davuser", secret, nil, "Depth", "0"), 401, "revoked password")
}

// Failed sign-ins are limited per username; a correct password does not get through once the
// limit is reached, and good requests never count.
func TestDAVSignInLimit(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	loginAs(t, srv, st, "limited", "user")
	_, secret, err := st.Drive().CreateAppPassword(ctx, "usr_limited", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if got := davRequest(srv, "PROPFIND", "/dav/", "limited", secret, nil, "Depth", "0"); got.Code != 207 {
			t.Fatalf("good request refused: %d", got.Code)
		}
	}
	for range 10 {
		if got := davRequest(srv, "PROPFIND", "/dav/", "limited", "guess", nil, "Depth", "0"); got.Code != 401 {
			t.Fatalf("bad guess: %d", got.Code)
		}
	}
	got := davRequest(srv, "PROPFIND", "/dav/", "limited", secret, nil, "Depth", "0")
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") == "" {
		t.Fatalf("after 10 failures: %d", got.Code)
	}
}
