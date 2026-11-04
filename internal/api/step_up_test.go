package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// stepUpCode reports the 403 step-up refusal's method, or "" when w is not one.
func stepUpCode(w *httptest.ResponseRecorder) string {
	var body struct{ Code, Method string }
	if w.Code != http.StatusForbidden || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Code != "step_up_required" {
		return ""
	}
	return body.Method
}

// TestStepUpGuardsSessionOutlivingCredentials pins Q2: WebDAV passwords, agent tokens, service
// tokens and linked computers are minted only within five minutes of re-proving the user.
func TestStepUpGuardsSessionOutlivingCredentials(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	start := time.Now()
	at := func(d time.Duration) { api.SetClockForTest(srv, func() time.Time { return start.Add(d) }) }
	user := loginAs(t, srv, st, "stepper", "admin")
	w, err := st.Drive().CreateWorkspace(ctx, "usr_stepper", "Steps", 8<<20)
	if err == nil {
		err = st.Groups().ReplaceGroup(ctx, &store.Group{ID: "steppers", DisplayName: "Steppers", Members: []string{"usr_stepper"}}, true)
	}
	if err == nil {
		err = st.Drive().Grant(ctx, "usr_stepper", w.ID, "steppers", "manager")
	}
	if err != nil {
		t.Fatal(err)
	}
	creates := []struct{ path, body string }{
		{"/api/auth/app-passwords", `{"name":"Laptop"}`},
		{"/api/auth/app-passwords", `{"name":"Agent","scope":"mcp:write"}`},
		{"/api/drive/workspaces/" + w.ID + "/tokens", `{"name":"Robot","role":"editor"}`},
		{"/api/devices/approve", approveBody("http://127.0.0.1:53682/cb")},
	}
	// A password sign-in is itself a fresh proof.
	for _, c := range creates {
		if got := send(t, srv, "POST", c.path, c.body, user, ""); got.Code/100 != 2 {
			t.Fatalf("fresh sign-in %s: %d %s", c.path, got.Code, got.Body.String())
		}
	}
	at(stepUpAge())
	for _, c := range creates {
		if got := send(t, srv, "POST", c.path, c.body, user, ""); stepUpCode(got) != "password" {
			t.Fatalf("stale session %s: %d %s", c.path, got.Code, got.Body.String())
		}
	}
	if got := send(t, srv, "POST", "/api/auth/step-up", `{"password":"wrong password here"}`, user, ""); got.Code != http.StatusForbidden {
		t.Fatalf("wrong password: %d %s", got.Code, got.Body.String())
	}
	if got := send(t, srv, "POST", "/api/auth/step-up", `{"password":"SuperSecretPass123!"}`, user, ""); got.Code != http.StatusOK {
		t.Fatalf("step-up: %d %s", got.Code, got.Body.String())
	}
	for _, c := range creates {
		if got := send(t, srv, "POST", c.path, c.body, user, ""); got.Code/100 != 2 {
			t.Fatalf("after step-up %s: %d %s", c.path, got.Code, got.Body.String())
		}
	}
	// Listing and revoking stay plain session routes.
	at(2 * stepUpAge())
	if got := send(t, srv, "GET", "/api/auth/app-passwords", "", user, ""); got.Code != http.StatusOK {
		t.Fatalf("list after the window: %d", got.Code)
	}
	// The step-up limit is the login limit: ten wrong passwords lock out the eleventh try.
	for i := 0; i < 10; i++ {
		send(t, srv, "POST", "/api/auth/step-up", `{"password":"wrong password here"}`, user, "")
	}
	if got := send(t, srv, "POST", "/api/auth/step-up", `{"password":"SuperSecretPass123!"}`, user, ""); got.Code != http.StatusTooManyRequests {
		t.Fatalf("step-up past the login limit: %d", got.Code)
	}
}

func stepUpAge() time.Duration { return 5*time.Minute + time.Second }

// TestSSOStepUp pins the KyIdentity step-up: max_age=0, a required fresh auth_time for the
// session's own subject, and a window dated from auth_time.
func TestSSOStepUp(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	f := newFakeKySignOn(t)
	cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.AutoProvision = f.issuer, "client", true
	srv = api.NewServer(cfg, st)
	alice := sessionCookie(ssoLogin(t, srv, f, "alice", "sid-1"))
	sessionCookie(ssoLogin(t, srv, f, "bob", "sid-2"))
	if alice == nil {
		t.Fatal("no SSO session")
	}
	const create = `{"name":"Laptop"}`
	// An SSO sign-in is not a proof of presence: KyIdentity may have reused its own session.
	if got := send(t, srv, "POST", "/api/auth/app-passwords", create, alice, ""); stepUpCode(got) != "sso" {
		t.Fatalf("SSO session without step-up: %d %s", got.Code, got.Body.String())
	}
	if got := send(t, srv, "POST", "/api/auth/step-up", `{"password":"x"}`, alice, ""); got.Code != http.StatusConflict {
		t.Fatalf("password step-up for SSO account: %d", got.Code)
	}

	stepUp := func(claims map[string]any, back string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/sso/kysignon/login?step_up=1&return="+url.QueryEscape(back), nil)
		req.AddCookie(alice)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		loc, err := url.Parse(w.Header().Get("Location"))
		if w.Code != http.StatusFound || err != nil || loc.Query().Get("max_age") != "0" {
			t.Fatalf("step-up redirect: %d %s", w.Code, w.Header().Get("Location"))
		}
		claims["nonce"] = loc.Query().Get("nonce")
		f.idToken = f.sign(t, "", claims)
		req = httptest.NewRequest(http.MethodGet, "/api/sso/kysignon/callback?code=c&state="+loc.Query().Get("state"), nil)
		for _, c := range w.Result().Cookies() {
			req.AddCookie(c)
		}
		req.AddCookie(alice)
		w = httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	now := time.Now().Unix()
	for name, claims := range map[string]map[string]any{
		"no auth_time":    {"sub": "alice", "sid": "sid-1"},
		"stale auth_time": {"sub": "alice", "sid": "sid-1", "auth_time": now - 3600},
		"other subject":   {"sub": "bob", "sid": "sid-2", "auth_time": now},
	} {
		if got := stepUp(claims, "/"); got.Code != http.StatusForbidden {
			t.Fatalf("%s: %d %s", name, got.Code, got.Body.String())
		}
		if got := send(t, srv, "POST", "/api/auth/app-passwords", create, alice, ""); stepUpCode(got) != "sso" {
			t.Fatalf("%s granted a step-up: %d", name, got.Code)
		}
	}
	// An off-site return path falls back to the app.
	if got := stepUp(map[string]any{"sub": "alice", "sid": "sid-1", "auth_time": now}, "//evil.example/x"); got.Code != http.StatusFound || got.Header().Get("Location") != "/" {
		t.Fatalf("off-site return: %d %q", got.Code, got.Header().Get("Location"))
	}
	if got := send(t, srv, "POST", "/api/auth/app-passwords", create, alice, ""); got.Code != http.StatusCreated {
		t.Fatalf("after SSO step-up: %d %s", got.Code, got.Body.String())
	}
	// The window counts from auth_time, not from when the callback ran.
	api.SetClockForTest(srv, func() time.Time { return time.Unix(now, 0).Add(stepUpAge()) })
	if got := send(t, srv, "POST", "/api/auth/app-passwords", create, alice, ""); stepUpCode(got) != "sso" {
		t.Fatalf("window outlived auth_time: %d", got.Code)
	}
	later := time.Now().Add(stepUpAge()).Unix()
	if got := stepUp(map[string]any{"sub": "alice", "sid": "sid-1", "auth_time": later}, "/settings?x=1"); got.Code != http.StatusFound || got.Header().Get("Location") != "/settings?x=1" {
		t.Fatalf("return path: %d %q", got.Code, got.Header().Get("Location"))
	}
	if got := send(t, srv, "POST", "/api/auth/app-passwords", create, alice, ""); got.Code != http.StatusCreated {
		t.Fatalf("after a second SSO step-up: %d %s", got.Code, got.Body.String())
	}
	records, _, err := st.Audit().ListAuditRecords(context.Background(), 0, 100)
	audited := 0
	for _, r := range records {
		if r.Action == "auth.step_up" && r.Details == "method=sso" {
			audited++
		}
	}
	if err != nil || audited != 2 {
		t.Fatalf("SSO step-ups audited %d times: %v", audited, err)
	}
}
