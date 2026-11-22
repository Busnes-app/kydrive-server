package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/testdb"
)

// A suspended org ends its members' browser sessions and refuses their password sign-in, as
// for an inactive account; on resume they sign in again.
func TestSuspendedOrgEndsSessionsAndSignIn(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	cookie := loginAs(t, srv, st, "member", "user")
	db := testdb.Open(t, cfg.Database)
	defer db.Close()
	setStatus := func(status string) {
		t.Helper()
		if _, err := db.Exec(`UPDATE drive_orgs SET status=? WHERE id=?`, status, drive.DefaultOrg); err != nil {
			t.Fatal(err)
		}
	}
	signIn := func() int {
		body, _ := json.Marshal(map[string]string{"username": "member", "password": "SuperSecretPass123!"})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code
	}
	setStatus("suspended")
	if w := do(t, srv, http.MethodGet, "/api/auth/me", cookie); !bytes.Contains(w.Body.Bytes(), []byte(`"authenticated":false`)) {
		t.Fatalf("suspended org kept its session: %s", w.Body.String())
	}
	if code := signIn(); code != http.StatusUnauthorized {
		t.Fatalf("suspended org signed in: %d", code)
	}
	setStatus("active")
	if code := signIn(); code != http.StatusOK {
		t.Fatalf("resumed org cannot sign in: %d", code)
	}
}

// A suspended org refuses its members' KyIdentity sign-in, as for an inactive account.
func TestSuspendedOrgRefusesSSOSignIn(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	f := newFakeKySignOn(t)
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.AutoProvision = f.issuer, "client", true
	srv = api.NewServer(cfg, st)
	if sessionCookie(ssoLogin(t, srv, f, "alice", "sid-1")) == nil {
		t.Fatal("SSO login did not issue a session")
	}
	db := testdb.Open(t, cfg.Database)
	defer db.Close()
	if _, err := db.Exec(`UPDATE drive_orgs SET status='suspended' WHERE id=?`, drive.DefaultOrg); err != nil {
		t.Fatal(err)
	}
	if w := ssoLogin(t, srv, f, "alice", "sid-2"); w.Code != http.StatusForbidden || sessionCookie(w) != nil {
		t.Fatalf("suspended org signed in through KyIdentity: %d", w.Code)
	}
}
