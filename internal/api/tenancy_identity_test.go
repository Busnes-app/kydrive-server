package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/syncauth"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

const webhookSecret = "hook-secret-16-bytes"

// multiSSO is the multi-tenant server signed on to a fake multi-tenant KyIdentity. Auto-
// provisioning is left off: multi-tenant mode forces it.
func multiSSO(t *testing.T) (*multiAPI, *fakeKySignOn) {
	t.Helper()
	m := setupMultiServer(t)
	f := newFakeKySignOn(t)
	f.orgs = `{"orgs":[]}`
	m.cfg.SSO.KySignOnIssuer, m.cfg.SSO.KySignOnClientID, m.cfg.SSO.KySignOnSecret = f.issuer, "client", "secret"
	m.cfg.SSO.KySignOnHMACSecret, m.cfg.SSO.AutoProvision = webhookSecret, false
	m.srv = api.NewServer(m.cfg, m.st)
	return m, f
}

func (m *multiAPI) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := m.db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (m *multiAPI) mismatches(t *testing.T, entry string) int {
	return m.count(t, `SELECT COUNT(*) FROM audit_records WHERE action='tenancy.org_mismatch' AND details=?`, "entry="+entry)
}

// orgList is a /api/suite/orgs page listing one org.
func orgList(id, status string, rev int64, quotas string) string {
	return fmt.Sprintf(`{"orgs":[{"id":%q,"status":%q,"revision":%d,"enabled":true,"quotas":%s}]}`, id, status, rev, quotas)
}

func TestMultiTenantSignInBindsTheOrg(t *testing.T) {
	m, f := multiSSO(t)
	accounts := func(sub string) int {
		return m.count(t, `SELECT COUNT(*) FROM users WHERE sso_provider='kysignon' AND sso_subject=?`, sub)
	}

	// No org_id: refused before any lookup, and nothing created.
	if w := ssoLogin(t, m.srv, f, "carol", "sid-1"); w.Code != http.StatusForbidden || sessionCookie(w) != nil || accounts("carol") != 0 {
		t.Fatalf("no org_id: %d %s", w.Code, w.Body)
	}
	// An org the poll does not list, or lists as suspended, signs nobody in or up.
	f.org = "org-c"
	if w := ssoLogin(t, m.srv, f, "carol", "sid-2"); w.Code != http.StatusForbidden || accounts("carol") != 0 || f.polls != 1 {
		t.Fatalf("unknown org: %d %s, %d polls", w.Code, w.Body, f.polls)
	}
	f.orgs = orgList("org-c", "suspended", 3, `{}`)
	if w := ssoLogin(t, m.srv, f, "carol", "sid-3"); w.Code != http.StatusForbidden || accounts("carol") != 0 {
		t.Fatalf("suspended org: %d %s", w.Code, w.Body)
	}
	// A new org is registered by the poll the sign-in makes, and the account created in it,
	// with auto-provisioning off in the configuration.
	f.orgs = orgList("org-c", "active", 4, `{"storage_bytes":1000}`)
	// The suspended row is known, so the sign-in does not poll; the background poll applies it.
	if w := ssoLogin(t, m.srv, f, "carol", "sid-4"); w.Code != http.StatusForbidden {
		t.Fatalf("known suspended org: %d %s", w.Code, w.Body)
	}
	if err := api.PollOrgsForTest(m.srv, context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := m.count(t, `SELECT COUNT(*) FROM drive_orgs WHERE id='org-c' AND status='active' AND storage_bytes=1000 AND revision=4`); n != 1 {
		t.Fatal("poll did not apply org-c")
	}
	polls := f.polls
	carol := sessionCookie(ssoLogin(t, m.srv, f, "carol", "sid-5"))
	if carol == nil || !signedIn(t, m.srv, carol) || f.polls != polls {
		t.Fatalf("active org sign-in: %v, %d polls", carol, f.polls-polls)
	}
	if n := m.count(t, `SELECT COUNT(*) FROM users WHERE sso_subject='carol' AND org_id='org-c' AND role='user'`); n != 1 {
		t.Fatal("account not created in the token's org")
	}
	// A known subject arriving with another org is refused and audited.
	f.org = "org-b"
	if w := ssoLogin(t, m.srv, f, "carol", "sid-6"); w.Code != http.StatusForbidden || sessionCookie(w) != nil || m.mismatches(t, "sign-in") != 1 {
		t.Fatalf("other org: %d %s", w.Code, w.Body)
	}
}

func TestMultiTenantStepUpChecksTheOrg(t *testing.T) {
	m, f := multiSSO(t)
	f.org = "org-a"
	alice := sessionCookie(ssoLogin(t, m.srv, f, "alice", "sid-1"))
	if alice == nil {
		t.Fatal("no session")
	}
	stepUp := func(claims map[string]any) int {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/sso/kysignon/login?step_up=1&return=/", nil)
		req.AddCookie(alice)
		w := httptest.NewRecorder()
		m.srv.ServeHTTP(w, req)
		loc, err := url.Parse(w.Header().Get("Location"))
		if w.Code != http.StatusFound || err != nil {
			t.Fatalf("step-up redirect: %d", w.Code)
		}
		claims["nonce"], claims["sub"], claims["sid"], claims["auth_time"] = loc.Query().Get("nonce"), "alice", "sid-1", time.Now().Unix()
		f.idToken = f.sign(t, "", claims)
		req = httptest.NewRequest(http.MethodGet, "/api/sso/kysignon/callback?code=c&state="+loc.Query().Get("state"), nil)
		for _, c := range w.Result().Cookies() {
			req.AddCookie(c)
		}
		req.AddCookie(alice)
		w = httptest.NewRecorder()
		m.srv.ServeHTTP(w, req)
		return w.Code
	}
	if code := stepUp(map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("step-up without org_id: %d", code)
	}
	if code := stepUp(map[string]any{"org_id": "org-b"}); code != http.StatusForbidden || m.mismatches(t, "step-up") != 2 {
		t.Fatalf("step-up with another org: %d", code)
	}
	if code := stepUp(map[string]any{"org_id": "org-a", "export_only": true}); code != http.StatusForbidden {
		t.Fatalf("export-only claim stepped up: %d", code)
	}
	if code := stepUp(map[string]any{"org_id": "org-a"}); code != http.StatusFound {
		t.Fatalf("step-up in the account's org: %d", code)
	}
}

func TestMultiTenantLogoutNeedsTheOrg(t *testing.T) {
	m, f := multiSSO(t)
	f.org = "org-a"
	s1 := sessionCookie(ssoLogin(t, m.srv, f, "alice", "sid-1"))
	s2 := sessionCookie(ssoLogin(t, m.srv, f, "alice", "sid-2"))
	f.org = ""
	if w := postLogout(m.srv, f.logoutToken(t, "alice", "sid-1")); w.Code != http.StatusBadRequest || !signedIn(t, m.srv, s1) {
		t.Fatalf("logout without org_id: %d", w.Code)
	}
	f.org = "org-a"
	if w := postLogout(m.srv, f.logoutToken(t, "alice", "sid-1")); w.Code != http.StatusOK || signedIn(t, m.srv, s1) || !signedIn(t, m.srv, s2) {
		t.Fatalf("logout in the org: %d", w.Code)
	}
	// A subject-wide logout naming another org still ends the sessions, and is audited.
	f.org = "org-b"
	if w := postLogout(m.srv, f.logoutToken(t, "alice", "")); w.Code != http.StatusOK || signedIn(t, m.srv, s2) || m.mismatches(t, "logout") != 1 {
		t.Fatalf("logout naming another org: %d", w.Code)
	}
}

// hook posts a signed suite webhook event.
func hook(t *testing.T, srv *api.Server, event, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sso/kysignon/sync", strings.NewReader(body))
	h, err := syncauth.Sign([]byte(webhookSecret), time.Now(), event, "evt-"+crypto.RandomHex(4), []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	h.Apply(req)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// scimBody is a suite webhook user at revision rev, with org_id when org is not "".
func scimBody(sub string, active bool, rev int, org string) string {
	extra := ""
	if org != "" {
		extra = fmt.Sprintf(`,"org_id":%q`, org)
	}
	return fmt.Sprintf(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"id":%q,"userName":%q,"active":%t,"meta":{"version":"W/\"%d\""}%s}`, sub, sub, active, rev, extra)
}

func TestMultiTenantWebhookBindsTheOrg(t *testing.T) {
	m, _ := multiSSO(t)
	status := func(sub string) (s string) {
		_ = m.db.QueryRow(`SELECT status||'/'||org_id FROM users WHERE sso_subject=?`, sub).Scan(&s)
		return s
	}
	if w := hook(t, m.srv, "user.created", scimBody("dave", true, 1, "")); w.Code != http.StatusBadRequest || status("dave") != "" {
		t.Fatalf("no org_id: %d %s", w.Code, w.Body)
	}
	if w := hook(t, m.srv, "user.created", scimBody("dave", true, 1, "org-a")); w.Code != http.StatusOK || status("dave") != "active/org-a" {
		t.Fatalf("create in org-a: %d %s %q", w.Code, w.Body, status("dave"))
	}
	// Another org may neither change nor delete the account.
	for i, event := range []string{"user.updated", "user.deleted"} {
		if w := hook(t, m.srv, event, scimBody("dave", false, 2+i, "org-b")); w.Code != http.StatusBadRequest || status("dave") != "active/org-a" || m.mismatches(t, "webhook") != i+1 {
			t.Fatalf("%s from org-b: %d %s %q", event, w.Code, w.Body, status("dave"))
		}
	}
	if w := hook(t, m.srv, "user.updated", scimBody("dave", false, 4, "org-a")); w.Code != http.StatusOK || status("dave") != "inactive/org-a" {
		t.Fatalf("update in org-a: %d %s", w.Code, w.Body)
	}
	// An org not yet registered is an outage the sender retries, not bad input.
	if w := hook(t, m.srv, "user.created", scimBody("erin", true, 1, "org-new")); w.Code != http.StatusInternalServerError || status("erin") != "" {
		t.Fatalf("unregistered org: %d %s", w.Code, w.Body)
	}
}

// A single-tenant install ignores org_id wherever KyIdentity sends it.
func TestSingleTenantIgnoresOrgClaims(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	f := newFakeKySignOn(t)
	f.org = "org-x"
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.AutoProvision, cfg.SSO.KySignOnHMACSecret = f.issuer, "client", true, webhookSecret
	srv = api.NewServer(cfg, st)
	if c := sessionCookie(ssoLogin(t, srv, f, "alice", "sid-1")); c == nil || !signedIn(t, srv, c) {
		t.Fatal("sign-in with an org claim refused")
	}
	if w := hook(t, srv, "user.created", scimBody("bob", true, 1, "org-x")); w.Code != http.StatusOK {
		t.Fatalf("webhook with org_id: %d %s", w.Code, w.Body)
	}
	if f.polls != 0 {
		t.Fatal("single-tenant polled orgs")
	}
}

func TestMultiTenantRefusesTheInstaller(t *testing.T) {
	m := setupMultiServer(t)
	if _, err := m.srv.ApplyInstaller(context.Background(), []byte(`{}`), true); err == nil || !bytes.Contains([]byte(err.Error()), []byte("single-tenant")) {
		t.Fatalf("installer on a multi-tenant install: %v", err)
	}
}

// A multi-tenant install reads suite groups once per active, enabled org, and each org's
// answer lands in that org.
func TestMultiTenantReadsSuiteGroupsPerOrg(t *testing.T) {
	m, f := multiSSO(t)
	if _, err := m.db.Exec(`INSERT INTO drive_orgs(id,status,enabled) VALUES('org-off','active',0),('org-s','suspended',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec(`UPDATE users SET sso_provider='kysignon',sso_subject='sub-'||id WHERE id IN ('a1','b1')`); err != nil {
		t.Fatal(err)
	}
	family := `{"groups":[{"id":"g-%s","name":"Family","slug":"family","state":"active","members":["sub-a1","sub-b1"]}]}`
	f.byOrg = map[string]string{"org-a": fmt.Sprintf(family, "a"), "org-b": fmt.Sprintf(family, "b")}
	api.SyncSuiteGroupsForTest(m.srv, context.Background())
	asked := strings.Join(f.orgOf, " ")
	if !strings.Contains(asked, "org-a org-b") || strings.Contains(asked, "org-off") || strings.Contains(asked, "org-s") || slices.Contains(f.orgOf, "") {
		t.Fatalf("asked for %q", asked)
	}
	for _, c := range [][3]string{{"g-a", "org-a", "a1"}, {"g-b", "org-b", "b1"}} {
		if n := m.count(t, `SELECT COUNT(*) FROM drive_suite_groups s JOIN drive_workspaces w ON w.id=s.workspace JOIN group_members gm ON gm.group_id=s.grp WHERE s.kyid=? AND w.org_id=?`, c[0], c[1]); n != 1 {
			t.Fatalf("%s: %d members in %s", c[0], n, c[1])
		}
		if n := m.count(t, `SELECT COUNT(*) FROM drive_suite_groups s JOIN group_members gm ON gm.group_id=s.grp WHERE s.kyid=? AND gm.user_id=?`, c[0], c[2]); n != 1 {
			t.Fatalf("%s lacks %s", c[0], c[2])
		}
	}
}

// A single-tenant install asks once, without an org.
func TestSingleTenantReadsSuiteGroupsOnce(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("suite groups live in the SQLite drive store")
	}
	f := newFakeKySignOn(t)
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.KySignOnSecret = f.issuer, "client", "secret"
	srv = api.NewServer(cfg, st)
	api.SyncSuiteGroupsForTest(srv, context.Background())
	if len(f.orgOf) != 1 || f.orgOf[0] != "" {
		t.Fatalf("asked for %q", f.orgOf)
	}
}

// A multi-tenant KyPost names the account's org in every signed body; KyDrive acts only for an
// account in that org.
func TestMultiTenantKyPostChecksTheOrg(t *testing.T) {
	m := setupMultiServer(t)
	m.cfg.SSO.KySignOnIssuer = kypostIssuer
	m.srv = api.NewServer(m.cfg, m.st)
	api.SetKyPostForTest(m.srv, kypostOrigin, kypostSecret, "kyidentity")
	if _, err := m.db.Exec(`UPDATE users SET sso_provider='kysignon',sso_subject='sub-'||id WHERE id='a1'`); err != nil {
		t.Fatal(err)
	}
	w := &kypostWorld{t: t, srv: m.srv, st: m.st, cfg: m.cfg}
	status := func(org string) *httptest.ResponseRecorder {
		body := map[string]any{"sub": "sub-a1", "issuer": kypostIssuer}
		if org != "" {
			body["org_id"] = org
		}
		return w.signed("/api/suite/kypost/status", "kypost.drive.status", "", body, kypostSecret)
	}
	code(t, status(""), 400, "bad_body")
	code(t, status("org-b"), 404, "no_user")
	if m.mismatches(t, "kypost") != 1 {
		t.Fatal("org mismatch not audited")
	}
	if rec := status("org-a"); rec.Code != http.StatusOK {
		t.Fatalf("own org: %d %s", rec.Code, rec.Body)
	}
}

// A single-tenant KyDrive ignores org_id in a KyPost body.
func TestSingleTenantKyPostIgnoresTheOrg(t *testing.T) {
	w := newKyPostWorld(t, "kyidentity")
	rec := w.signed("/api/suite/kypost/status", "kypost.drive.status", "", map[string]any{"sub": "sub-alice", "issuer": kypostIssuer, "org_id": "org-x"}, kypostSecret)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with org_id: %d %s", rec.Code, rec.Body)
	}
}
