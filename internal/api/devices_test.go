package api_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

const linkVerifier = "M25iVXpKU3puUjFaYWg3T1NDTDQtcW1ROUY5YXlwalNoc0hhakxifmZHag"

func linkChallenge() string {
	h := sha256.Sum256([]byte(linkVerifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

func approveBody(redirect string) string {
	b, _ := json.Marshal(map[string]string{"redirect": redirect, "challenge": linkChallenge(), "state": "st-123", "name": "Work laptop", "platform": "linux"})
	return string(b)
}

// approve returns the code the server put in the loopback redirect.
func approve(t *testing.T, srv *api.Server, cookie *http.Cookie) string {
	t.Helper()
	out := send(t, srv, "POST", "/api/devices/approve", approveBody("http://127.0.0.1:53682/cb"), cookie, "")
	var got struct{ Redirect string }
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &got) != nil {
		t.Fatalf("approve %d %s", out.Code, out.Body.String())
	}
	u, err := url.Parse(got.Redirect)
	if err != nil || u.Host != "127.0.0.1:53682" || u.Path != "/cb" || u.Query().Get("state") != "st-123" || u.Query().Get("code") == "" || u.Fragment != "" {
		t.Fatalf("redirect %q", got.Redirect)
	}
	return u.Query().Get("code")
}

func redeem(srv *api.Server, code, verifier string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/api/devices/token", strings.NewReader(url.Values{"code": {code}, "verifier": {verifier}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

func linkDevice(t *testing.T, srv *api.Server, cookie *http.Cookie) string {
	t.Helper()
	out := redeem(srv, approve(t, srv, cookie), linkVerifier)
	var got struct {
		Token, User string
		DevicesURL  string `json:"devices_url"`
	}
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &got) != nil || !strings.HasPrefix(got.Token, "kyd_") || got.User == "" || got.DevicesURL == "" {
		t.Fatalf("token %d %s", out.Code, out.Body.String())
	}
	return got.Token
}

func rawDB(t *testing.T, cfg *config.Config, q string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", cfg.Database.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceLink(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	user := loginAs(t, srv, st, "linker", "user")
	expect := func(got *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if got.Code != code {
			t.Fatalf("%s: %d, want %d: %s", what, got.Code, code, got.Body.String())
		}
	}

	// Approval needs a session and its CSRF token; nothing approves on a GET.
	expect(send(t, srv, "POST", "/api/devices/approve", approveBody("http://127.0.0.1:53682/cb"), nil, ""), 401, "no session")
	noCSRF := httptest.NewRequest("POST", "/api/devices/approve", strings.NewReader(approveBody("http://127.0.0.1:53682/cb")))
	noCSRF.AddCookie(user)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, noCSRF)
	expect(rec, 403, "no CSRF token")
	crossSite := httptest.NewRequest("POST", "/api/devices/approve", strings.NewReader(approveBody("http://127.0.0.1:53682/cb")))
	crossSite.AddCookie(user)
	api.SignCSRFForTest(srv, crossSite)
	crossSite.Header.Set("Sec-Fetch-Site", "cross-site")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, crossSite)
	expect(rec, 403, "cross-site")
	if got := send(t, srv, "GET", "/api/devices/approve?"+url.Values{"redirect": {"http://127.0.0.1:53682/cb"}}.Encode(), "", user, ""); got.Code == 200 {
		t.Fatalf("GET approved: %s", got.Body.String())
	}

	for _, redirect := range []string{"http://localhost:53682/cb", "http://127.0.0.1/cb", "http://[::1]/cb", "http://evil.example:53682/cb", "http://user@127.0.0.1:53682/cb", "http://127.0.0.1:53682/cb#x", "https://127.0.0.1:53682/cb"} {
		expect(send(t, srv, "POST", "/api/devices/approve", approveBody(redirect), user, ""), 400, redirect)
	}
	expect(send(t, srv, "POST", "/api/devices/approve", strings.Replace(approveBody("http://127.0.0.1:53682/cb"), `"state":"st-123"`, `"state":""`, 1), user, ""), 400, "no state")

	// Wrong verifier, reuse and expiry are refused; a wrong verifier also burns the code.
	code := approve(t, srv, user)
	expect(redeem(srv, code, strings.Repeat("A", 43)), 400, "wrong verifier")
	expect(redeem(srv, code, linkVerifier), 400, "after wrong verifier")
	code = approve(t, srv, user)
	expect(redeem(srv, code, linkVerifier), 200, "redeem")
	expect(redeem(srv, code, linkVerifier), 400, "reused code")
	code = approve(t, srv, user)
	rawDB(t, cfg, `UPDATE drive_device_codes SET expires=? WHERE used=0`, time.Now().Add(-time.Second).Unix())
	expect(redeem(srv, code, linkVerifier), 400, "expired code")

	// The code travels in a form body, never the URL.
	code = approve(t, srv, user)
	inURL := httptest.NewRequest("POST", "/api/devices/token?"+url.Values{"code": {code}, "verifier": {linkVerifier}}.Encode(), nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, inURL)
	expect(rec, 400, "code in URL")

	// Redemption is limited per client like login.
	for i := 0; i < 30; i++ {
		rec = redeem(srv, "guess", linkVerifier)
	}
	expect(rec, 429, "token guessing")
}

func TestDeviceToken(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	ctx := context.Background()
	admin := loginAs(t, srv, st, "devadmin", "admin")
	user := loginAs(t, srv, st, "devuser", "user")
	if err := st.Groups().ReplaceGroup(ctx, &store.Group{ID: "devgroup", DisplayName: "Dev", Members: []string{"usr_devuser"}}, true); err != nil {
		t.Fatal(err)
	}
	w, err := st.Drive().CreateWorkspace(ctx, "usr_devadmin", "Team", 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Drive().Grant(ctx, "usr_devadmin", w.ID, "devgroup", "editor"); err != nil {
		t.Fatal(err)
	}
	expect := func(got *httptest.ResponseRecorder, code int, what string) {
		t.Helper()
		if got.Code != code {
			t.Fatalf("%s: %d, want %d: %s", what, got.Code, code, got.Body.String())
		}
	}
	token := linkDevice(t, srv, user)

	// The token works as Bearer on the drive API and on /dav/, without CSRF.
	got := send(t, srv, "GET", "/api/drive/workspaces", "", nil, token)
	expect(got, 200, "workspaces")
	if !strings.Contains(got.Body.String(), w.ID) {
		t.Fatalf("workspace missing: %s", got.Body.String())
	}
	expect(send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/folders", `{"name":"Synced","parent":""}`, nil, token), 201, "add folder")
	dav := func(method, path, token string) *httptest.ResponseRecorder {
		return davRequest(srv, method, path, "", "", nil, "Authorization", "Bearer "+token, "Depth", "1")
	}
	got = dav("PROPFIND", "/dav/", token)
	expect(got, 207, "DAV bearer")
	if !strings.Contains(got.Body.String(), "/dav/Team/") {
		t.Fatalf("DAV listing:\n%s", got.Body.String())
	}

	// Never a session: account, device and admin routes refuse it.
	expect(send(t, srv, "GET", "/api/devices", "", nil, token), 401, "device list")
	expect(send(t, srv, "GET", "/api/auth/app-passwords", "", nil, token), 401, "app passwords")
	adminToken := linkDevice(t, srv, admin)
	expect(send(t, srv, "GET", "/api/drive/status", "", nil, adminToken), 401, "requireAdmin route")
	expect(send(t, srv, "PUT", "/api/drive/device-policy", `{"wipe_default":false,"wipe_locked":false}`, nil, adminToken), 401, "policy write")
	expect(send(t, srv, "GET", "/api/drive/workspaces?admin=true", "", nil, adminToken), 403, "administrative listing")
	expect(send(t, srv, "PUT", "/api/drive/workspaces/"+w.ID+"/grants/devgroup", `{"role":"manager"}`, nil, adminToken), 403, "admin grant change")
	expect(send(t, srv, "POST", "/api/drive/workspaces/"+w.ID+"/tokens", `{"name":"Robot","role":"reader"}`, nil, token), 403, "service token mint")

	// Revocation: the policy default applies, and a lock overrides the request.
	expect(send(t, srv, "GET", "/api/drive/device-policy", "", nil, token), 200, "policy read")
	if got := send(t, srv, "GET", "/api/drive/device-policy", "", user, ""); !strings.Contains(got.Body.String(), `"wipe_default":true`) || !strings.Contains(got.Body.String(), `"wipe_locked":false`) {
		t.Fatalf("default policy %s", got.Body.String())
	}
	expect(send(t, srv, "PUT", "/api/drive/device-policy", `{"wipe_default":true,"wipe_locked":true}`, user, ""), 403, "user policy write")
	expect(send(t, srv, "PUT", "/api/drive/device-policy", `{"wipe_default":true}`, admin, ""), 400, "partial policy")
	expect(send(t, srv, "PUT", "/api/drive/device-policy", `{"wipe_default":true,"wipe_locked":true}`, admin, ""), 200, "lock policy")
	var devices []struct{ ID, Name, Platform string }
	list := send(t, srv, "GET", "/api/devices", "", user, "")
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &devices) != nil || len(devices) != 1 || devices[0].Name != "Work laptop" || devices[0].Platform != "linux" || strings.Contains(list.Body.String(), token) {
		t.Fatalf("devices %d %s", list.Code, list.Body.String())
	}
	other := loginAs(t, srv, st, "devother", "user")
	expect(send(t, srv, "DELETE", "/api/devices/"+devices[0].ID+"?wipe=false", "", other, ""), 403, "another user's device")
	expect(send(t, srv, "DELETE", "/api/devices/"+devices[0].ID+"?wipe=false", "", user, ""), 200, "revoke")
	for _, path := range []string{"/api/drive/workspaces"} {
		got = send(t, srv, "GET", path, "", nil, token)
		expect(got, 401, "revoked")
		if strings.TrimSpace(got.Body.String()) != `{"error":"device_revoked","wipe":true}` {
			t.Fatalf("locked policy ignored: %s", got.Body.String())
		}
	}
	got = dav("PROPFIND", "/dav/", token)
	expect(got, 401, "revoked on DAV")
	if !strings.Contains(got.Body.String(), `"error":"device_revoked","wipe":true`) {
		t.Fatalf("DAV revoked body: %s", got.Body.String())
	}

	// Unlocked, the request decides.
	expect(send(t, srv, "PUT", "/api/drive/device-policy", `{"wipe_default":true,"wipe_locked":false}`, admin, ""), 200, "unlock policy")
	second := linkDevice(t, srv, user)
	list = send(t, srv, "GET", "/api/devices", "", user, "")
	if json.Unmarshal(list.Body.Bytes(), &devices) != nil || len(devices) != 1 {
		t.Fatalf("devices %s", list.Body.String())
	}
	expect(send(t, srv, "DELETE", "/api/devices/"+devices[0].ID+"?wipe=false", "", user, ""), 200, "revoke unlocked")
	if got = send(t, srv, "GET", "/api/drive/workspaces", "", nil, second); strings.TrimSpace(got.Body.String()) != `{"error":"device_revoked","wipe":false}` {
		t.Fatalf("unlocked revoke: %s", got.Body.String())
	}

	// After 30 days a revoked token is a plain 401; so is one idle for 90 days.
	rawDB(t, cfg, `UPDATE drive_devices SET revoked_at=? WHERE revoked_at>0`, time.Now().Add(-31*24*time.Hour).Unix())
	got = send(t, srv, "GET", "/api/drive/workspaces", "", nil, token)
	expect(got, 401, "old revocation")
	if strings.Contains(got.Body.String(), "wipe") {
		t.Fatalf("old revocation still wipes: %s", got.Body.String())
	}
	idle := linkDevice(t, srv, user)
	rawDB(t, cfg, `UPDATE drive_devices SET last_used=? WHERE revoked_at=0`, time.Now().Add(-91*24*time.Hour).Unix())
	expect(send(t, srv, "GET", "/api/drive/workspaces", "", nil, idle), 401, "idle 90 days")

	// Deactivation: a plain 401 without wipe, on the API and on DAV.
	live := linkDevice(t, srv, user)
	u, err := st.Users().GetUserByID(ctx, "usr_devuser")
	if err != nil {
		t.Fatal(err)
	}
	u.Status = "inactive"
	if err = st.Users().UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	for _, got := range []*httptest.ResponseRecorder{send(t, srv, "GET", "/api/drive/workspaces", "", nil, live), dav("PROPFIND", "/dav/", live)} {
		expect(got, 401, "deactivated")
		if strings.Contains(got.Body.String(), "wipe") {
			t.Fatalf("deactivation wipes: %s", got.Body.String())
		}
	}

	// Failed DAV bearer attempts count toward the per-client limit.
	for i := 0; i < 25; i++ {
		got = dav("PROPFIND", "/dav/", "kyd_guess")
	}
	expect(got, 429, "DAV bearer guessing")
}
