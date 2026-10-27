package api_test

import (
	"context"
	stdcrypto "crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// fakeKySignOn is a TLS identity provider whose token endpoint returns the next ID token.
type fakeKySignOn struct {
	key     *rsa.PrivateKey
	issuer  string
	idToken string
}

func newFakeKySignOn(t *testing.T) *fakeKySignOn {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeKySignOn{key: key}
	idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer": f.issuer, "authorization_endpoint": f.issuer + "/authorize",
				"token_endpoint": f.issuer + "/token", "jwks_uri": f.issuer + "/keys",
				"id_token_signing_alg_values_supported": []string{"RS256"},
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]any{
				"kty": "RSA", "alg": "RS256", "kid": "k",
				"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": "AQAB",
			}}})
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "token_type": "Bearer", "id_token": f.idToken})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(idp.Close)
	f.issuer = idp.URL
	// The server reaches the provider through the default client; trust the test CA.
	old := http.DefaultTransport
	http.DefaultTransport = idp.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = old })
	return f
}

func (f *fakeKySignOn) sign(t *testing.T, typ string, claims map[string]any) string {
	t.Helper()
	claims["iss"], claims["aud"] = f.issuer, "client"
	claims["exp"] = time.Now().Add(2 * time.Minute).Unix()
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = time.Now().Unix()
	}
	header := map[string]string{"alg": "RS256", "kid": "k"}
	if typ != "" {
		header["typ"] = typ
	}
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, stdcrypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeKySignOn) logoutToken(t *testing.T, sub, sid string) string {
	claims := map[string]any{"sub": sub, "jti": sid + sub, "events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}}
	if sid != "" {
		claims["sid"] = sid
	}
	return f.sign(t, "logout+jwt", claims)
}

// ssoLogin runs the KySignOn login and callback for sub/sid and returns the callback response.
func ssoLogin(t *testing.T, srv *api.Server, f *fakeKySignOn, sub, sid string) *httptest.ResponseRecorder {
	return ssoLoginAt(t, srv, f, sub, sid, time.Now())
}

// ssoLoginAt is ssoLogin with an ID token the provider issued at iat.
func ssoLoginAt(t *testing.T, srv *api.Server, f *fakeKySignOn, sub, sid string, iat time.Time) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/sso/kysignon/login", nil))
	loc, err := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusFound || err != nil {
		t.Fatalf("login redirect: %d %s", w.Code, w.Body.String())
	}
	q := loc.Query()
	f.idToken = f.sign(t, "", map[string]any{"sub": sub, "sid": sid, "nonce": q.Get("nonce"), "preferred_username": sub, "iat": iat.Unix()})
	req := httptest.NewRequest(http.MethodGet, "/api/sso/kysignon/callback?code=c&state="+q.Get("state"), nil)
	for _, c := range w.Result().Cookies() {
		req.AddCookie(c)
	}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func postLogout(srv *api.Server, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/sso/kysignon/backchannel-logout", strings.NewReader(url.Values{"logout_token": {token}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestBackchannelLogoutEndsTheSSOLogin(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	f := newFakeKySignOn(t)
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.AutoProvision = f.issuer, "client", true
	srv = api.NewServer(cfg, st)

	ended := sessionCookie(ssoLogin(t, srv, f, "alice", "sid-a1"))
	kept := sessionCookie(ssoLogin(t, srv, f, "alice", "sid-a2"))
	if ended == nil || kept == nil || !signedIn(t, srv, ended) || !signedIn(t, srv, kept) {
		t.Fatal("SSO login did not issue working sessions")
	}

	// An ID token is not a logout token, even when signed by the provider.
	if w := postLogout(srv, f.idToken); w.Code != http.StatusBadRequest {
		t.Fatalf("ID token as logout token: got %d", w.Code)
	}
	w := postLogout(srv, f.logoutToken(t, "alice", "sid-a1"))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("logout: got %d %q %s", w.Code, w.Header().Get("Cache-Control"), w.Body.String())
	}
	if signedIn(t, srv, ended) {
		t.Fatal("session survived its back-channel logout")
	}
	if !signedIn(t, srv, kept) {
		t.Fatal("logout of one login ended another login of the same user")
	}
	// A body over 16 KiB is refused even when it carries a valid token.
	big := url.Values{"logout_token": {f.logoutToken(t, "alice", "sid-a2")}, "pad": {strings.Repeat("x", 16<<10)}}.Encode()
	req := httptest.NewRequest(http.MethodPost, "/api/sso/kysignon/backchannel-logout", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || !signedIn(t, srv, kept) {
		t.Fatalf("oversized logout body: got %d", w.Code)
	}
	// A replay is harmless and still acknowledged.
	if w := postLogout(srv, f.logoutToken(t, "alice", "sid-a1")); w.Code != http.StatusOK {
		t.Fatalf("replayed logout: got %d", w.Code)
	}

	// A logout that lands before the callback finishes fences that login.
	if w := postLogout(srv, f.logoutToken(t, "alice", "sid-late")); w.Code != http.StatusOK {
		t.Fatalf("early logout: got %d", w.Code)
	}
	if w := ssoLogin(t, srv, f, "alice", "sid-late"); w.Code != http.StatusUnauthorized || sessionCookie(w) != nil {
		t.Fatalf("callback revived an ended login: %d", w.Code)
	}

	// A subject-wide token ends the user's logins issued by then, and nobody else's.
	bob := sessionCookie(ssoLogin(t, srv, f, "bob", "sid-b1"))
	if w := postLogout(srv, f.logoutToken(t, "alice", "")); w.Code != http.StatusOK {
		t.Fatalf("subject-wide logout: got %d", w.Code)
	}
	if signedIn(t, srv, kept) || !signedIn(t, srv, bob) {
		t.Fatal("subject-wide logout ended the wrong sessions")
	}
	// A SCIM-provisioned account signs in through KySignOn too; a subject-wide token finds it.
	if _, err := st.Users().ApplyDirectoryUser(context.Background(), &store.DirectoryUser{Provider: "scim", Subject: "carol", Username: "carol", Active: true}); err != nil {
		t.Fatal(err)
	}
	carol := sessionCookie(ssoLogin(t, srv, f, "carol", "sid-c1"))
	if carol == nil || !signedIn(t, srv, carol) {
		t.Fatal("SCIM account did not sign in")
	}
	if w := postLogout(srv, f.logoutToken(t, "carol", "")); w.Code != http.StatusOK || signedIn(t, srv, carol) {
		t.Fatalf("subject-wide logout of a SCIM account: got %d", w.Code)
	}
	if w := postLogout(srv, f.logoutToken(t, "nobody", "")); w.Code != http.StatusOK {
		t.Fatalf("logout for an unknown subject: got %d", w.Code)
	}

	// A subject-wide logout fences a callback already holding an ID token issued before
	// it, even for a subject not yet provisioned here; a later login still works.
	daveLogout := f.sign(t, "logout+jwt", map[string]any{"sub": "dave", "jti": "dave", "iat": time.Now().Add(-30 * time.Second).Unix(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}}})
	if w := postLogout(srv, daveLogout); w.Code != http.StatusOK {
		t.Fatalf("subject-wide logout of an unprovisioned subject: got %d", w.Code)
	}
	if w := ssoLoginAt(t, srv, f, "dave", "sid-d1", time.Now().Add(-time.Minute)); w.Code != http.StatusUnauthorized || sessionCookie(w) != nil {
		t.Fatalf("pending callback survived a subject-wide logout: %d", w.Code)
	}
	if w := ssoLoginAt(t, srv, f, "dave", "sid-d2", time.Now().Add(5*time.Second)); w.Code != http.StatusFound || !signedIn(t, srv, sessionCookie(w)) {
		t.Fatalf("login issued after the subject-wide logout was refused: %d", w.Code)
	}

	var audits int
	recs, _, err := st.Audit().ListAuditRecords(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Action == "auth.sso_backchannel_logout" {
			audits++
		}
	}
	if audits == 0 {
		t.Fatal("back-channel logout was not audited")
	}
}

// An editor opened through an SSO login stays bound to it: a back-channel logout revokes it
// even after the login's browser session expired and was pruned.
func TestBackchannelLogoutRevokesEditorsOfPrunedSessions(t *testing.T) {
	editorFixture(t)
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	f := newFakeKySignOn(t)
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.AutoProvision = f.issuer, "client", true
	srv = api.NewServer(cfg, st)
	loginAs(t, srv, st, "boss", "admin")
	w, err := st.Drive().CreateWorkspace(ctx, "usr_boss", "Team", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, logout := range []func() string{
		func() string { return f.logoutToken(t, "alice", "sid-e0") },
		func() string { return f.logoutToken(t, "alice", "") },
	} {
		cookie := sessionCookie(ssoLogin(t, srv, f, "alice", fmt.Sprintf("sid-e%d", i)))
		alice, err := store.DirectorySubject(ctx, st.Users(), "alice")
		if cookie == nil || err != nil {
			t.Fatalf("SSO login: %v", err)
		}
		if i == 0 {
			if err = st.Groups().ReplaceGroup(ctx, &store.Group{ID: "writers", DisplayName: "Writers", Members: []string{alice.ID}}, true); err != nil {
				t.Fatal(err)
			}
			if err = st.Drive().Grant(ctx, "usr_boss", w.ID, "writers", "editor"); err != nil {
				t.Fatal(err)
			}
		}
		var file struct{ ID string }
		out := send(t, srv, "POST", fmt.Sprintf("/api/drive/workspaces/%s/uploads?name=%d.docx&revision=0", w.ID, i), "hello", cookie, "")
		if out.Code != 201 || json.Unmarshal(out.Body.Bytes(), &file) != nil {
			t.Fatalf("upload %d %s", out.Code, out.Body.String())
		}
		var opened struct {
			Config struct {
				Document struct{ Key string } `json:"document"`
			} `json:"config"`
		}
		out = send(t, srv, "POST", "/api/drive/files/"+file.ID+"/editor", "", cookie, "")
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &opened) != nil {
			t.Fatalf("open %d %s", out.Code, out.Body.String())
		}
		// The session expires and the sweep removes it; the editor stays open.
		if _, err = db.ExecContext(ctx, `UPDATE sessions SET expires_at=? WHERE user_id=?`, time.Now().Add(-time.Minute).UTC(), alice.ID); err != nil {
			t.Fatal(err)
		}
		if err = st.Sessions().CleanExpiredSessions(ctx); err != nil {
			t.Fatal(err)
		}
		id, revision, epoch, _ := drive.ParseDocumentKey(opened.Config.Document.Key)
		if _, err = st.Drive().CallbackActor(ctx, id, revision, epoch); err != nil {
			t.Fatalf("editor ended with its browser session: %v", err)
		}
		if out := postLogout(srv, logout()); out.Code != http.StatusOK {
			t.Fatalf("logout %d", out.Code)
		}
		if _, err = st.Drive().CallbackActor(ctx, id, revision, epoch); !errors.Is(err, drive.ErrDenied) {
			t.Fatalf("logout %d left the editor authorized: %v", i, err)
		}
	}
}
