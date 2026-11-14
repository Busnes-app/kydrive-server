package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/drive"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

const (
	officePeer  = "203.0.113.5:41000" // inside officeRange
	officeRange = "203.0.113.0/24"
	outsidePeer = "198.51.100.9:41000"
)

// exemptCall sends one request from peer, optionally forwarded for xff, with a session.
func exemptCall(t *testing.T, srv *api.Server, method, path, body, peer, xff string, session *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = peer
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	if session != nil {
		req.AddCookie(session)
		api.SignCSRFForTest(srv, req)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// signIn tries a wrong password for a new username each time, so only the per-client window
// can refuse it.
func signIn(t *testing.T, srv *api.Server, peer, xff string, i int) int {
	t.Helper()
	body := fmt.Sprintf(`{"username":"nobody%d","password":"WrongPass123!"}`, i)
	return exemptCall(t, srv, "POST", "/api/auth/login", body, peer, xff, nil).Code
}

// limited reports whether 25 sign-ins from peer reach a 429.
func limited(t *testing.T, srv *api.Server, peer string) bool {
	t.Helper()
	for i := 0; i < 25; i++ {
		if signIn(t, srv, peer, "", i) == http.StatusTooManyRequests {
			return true
		}
	}
	return false
}

func putNetworks(t *testing.T, srv *api.Server, session *http.Cookie, networks string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"networks": networks})
	return exemptCall(t, srv, "PUT", "/api/settings/rate-limit-networks", string(body), "192.0.2.1:1234", "", session)
}

func networksStatus(t *testing.T, w *httptest.ResponseRecorder) (networks, environment string, exempted float64) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out["networks"].(string), out["environment"].(string), out["exempted"].(float64)
}

func createLocalUser(t *testing.T, st store.Store, username, pass string) {
	t.Helper()
	hash, _ := password.Hash(pass)
	if err := st.Users().CreateUser(context.Background(), &store.User{
		ID: "usr_" + username, Username: username, PasswordHash: hash, Role: "user", Status: "active", SSOProvider: "local",
	}); err != nil {
		t.Fatal(err)
	}
}

// An allowlisted office goes past the per-client sign-in window; everyone else keeps it.
func TestExemptNetworkSkipsThePerIPLoginLimit(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, _, _ := setupTestServer(t)

	for i := 1; i <= 60; i++ {
		if code := signIn(t, srv, officePeer, "", i); code != http.StatusUnauthorized {
			t.Fatalf("office attempt %d answered %d, want the wrong-password 401", i, code)
		}
	}
	for i := 1; i <= 20; i++ {
		if code := signIn(t, srv, outsidePeer, "", i); code == http.StatusTooManyRequests {
			t.Fatalf("outside attempt %d throttled early", i)
		}
	}
	if code := signIn(t, srv, outsidePeer, "", 21); code != http.StatusTooManyRequests {
		t.Fatalf("outside attempt 21 answered %d, want 429", code)
	}
}

// The exemption covers the per-IP window only: ten wrong guesses at one account from the
// office still close that account's window there, and the right password is then refused.
func TestExemptNetworkKeepsThePerAccountLoginWindow(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	createLocalUser(t, st, "carol", "SuperSecretPass123!")
	try := func(pass string) int {
		return exemptCall(t, srv, "POST", "/api/auth/login", `{"username":"carol","password":"`+pass+`"}`, officePeer, "", nil).Code
	}
	for i := 1; i <= 10; i++ {
		if code := try("WrongPass123!"); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: %d", i, code)
		}
	}
	if code := try("SuperSecretPass123!"); code != http.StatusUnauthorized {
		t.Fatalf("exhausted account window from the office answered %d, want 401", code)
	}
}

// With no trusted proxy, X-Forwarded-For is the caller's own claim and buys no exemption.
func TestForgedForwardedForCannotClaimAnExemptNetwork(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, _, _ := setupTestServer(t)

	for i := 1; i <= 20; i++ {
		signIn(t, srv, outsidePeer, "203.0.113.5", i)
	}
	if code := signIn(t, srv, outsidePeer, "203.0.113.5", 21); code != http.StatusTooManyRequests {
		t.Fatalf("attempt 21 answered %d, want 429: a forged header claimed the office", code)
	}
}

// Behind a trusted proxy the forwarded client is the one matched, and a client that
// prepends an office address to the chain is matched on the entry the proxy appended.
func TestExemptNetworkMatchesTheClientATrustedProxyReports(t *testing.T) {
	t.Setenv("KY_TRUSTED_PROXIES", "192.0.2.0/24")
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, _, _ := setupTestServer(t)

	for i := 1; i <= 30; i++ {
		if code := signIn(t, srv, "192.0.2.7:41000", "203.0.113.5", i); code == http.StatusTooManyRequests {
			t.Fatalf("office client behind the proxy throttled at attempt %d", i)
		}
	}
	for i := 1; i <= 20; i++ {
		signIn(t, srv, "192.0.2.7:41000", "203.0.113.5, 198.51.100.9", i)
	}
	if code := signIn(t, srv, "192.0.2.7:41000", "203.0.113.5, 198.51.100.9", 21); code != http.StatusTooManyRequests {
		t.Fatalf("attempt 21 answered %d, want 429: a prepended office address was believed", code)
	}
}

// Password change from the office skips the per-IP window (10 a minute) but keeps the
// per-account one (5 a minute); from elsewhere the per-IP window still closes.
func TestExemptNetworkKeepsThePasswordChangeAccountLimit(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	change := func(session *http.Cookie, peer string) int {
		return exemptCall(t, srv, "POST", "/api/auth/change-password", `{"current_password":"nope","new_password":"AnotherPass456!"}`, peer, "", session).Code
	}
	for _, peer := range []string{officePeer, outsidePeer} {
		sessions := []*http.Cookie{}
		for _, name := range []string{"dave", "erin", "frank"} {
			sessions = append(sessions, loginAs(t, srv, st, name+peer[:3], "user"))
		}
		refused := 0
		for _, session := range sessions {
			for i := 1; i <= 5; i++ {
				if change(session, peer) == http.StatusTooManyRequests {
					refused++
				}
			}
		}
		switch {
		case peer == officePeer && refused != 0:
			t.Fatalf("office: %d of 15 changes across three accounts throttled, want none", refused)
		case peer == outsidePeer && refused != 5:
			t.Fatalf("outside: %d of 15 changes throttled, want the 5 past the per-IP window", refused)
		}
		if peer == officePeer {
			if code := change(sessions[0], peer); code != http.StatusTooManyRequests {
				t.Fatalf("office attempt 6 on one account answered %d, want 429 from the per-account limit", code)
			}
		}
	}
}

// Step-up from the office skips the per-IP sign-in window but keeps the account window.
func TestExemptNetworkKeepsTheStepUpAccountWindow(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "gina", "user")
	for i := 1; i <= 25; i++ { // more than the per-IP window, all from the office
		signIn(t, srv, officePeer, "", i)
	}
	stepUp := func(pass string) int {
		return exemptCall(t, srv, "POST", "/api/auth/step-up", `{"password":"`+pass+`"}`, officePeer, "", session).Code
	}
	if code := stepUp("SuperSecretPass123!"); code != http.StatusOK {
		t.Fatalf("step-up after 25 office sign-ins answered %d, want 200", code)
	}
	for i := 1; i <= 10; i++ {
		if code := stepUp("wrong password here"); code != http.StatusForbidden {
			t.Fatalf("wrong step-up %d: %d", i, code)
		}
	}
	if code := stepUp("SuperSecretPass123!"); code != http.StatusTooManyRequests {
		t.Fatalf("step-up past the account window answered %d, want 429", code)
	}
}

// WebDAV app-password failures from the office skip the per-client window but keep the
// per-account one; device tokens, which have no account window, keep the per-client one.
func TestExemptNetworkWebDAV(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	loginAs(t, srv, st, "hal", "user")
	_, secret, err := st.Drive().CreateAppPassword(context.Background(), "usr_hal", "Laptop")
	if err != nil {
		t.Fatal(err)
	}
	dav := func(peer, user, pass, bearer string) int {
		r := httptest.NewRequest("PROPFIND", "/dav/", nil)
		r.Header.Set("Depth", "0")
		r.RemoteAddr = peer
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		} else {
			r.SetBasicAuth(user, pass)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		return w.Code
	}
	for i := 1; i <= 30; i++ {
		if code := dav(officePeer, fmt.Sprintf("stale%d", i%3), "wrong", ""); code != http.StatusUnauthorized {
			t.Fatalf("office app-password failure %d answered %d, want 401", i, code)
		}
	}
	for i := 1; i <= 20; i++ {
		dav(outsidePeer, fmt.Sprintf("stale%d", i%3), "wrong", "")
	}
	if code := dav(outsidePeer, "hal", secret, ""); code != http.StatusTooManyRequests {
		t.Fatalf("outside after 20 failures answered %d, want 429", code)
	}
	// Per account: ten wrong guesses at hal from the office shut hal's window there.
	for i := 1; i <= 10; i++ {
		dav(officePeer, "hal", "wrong", "")
	}
	if code := dav(officePeer, "hal", secret, ""); code != http.StatusUnauthorized {
		t.Fatalf("hal's exhausted window from the office answered %d, want 401", code)
	}
	// Device tokens from the office keep the per-client window.
	for i := 1; i <= 20; i++ {
		dav(officePeer, "", "", "kyd_wrong")
	}
	if code := dav(officePeer, "", "", "kyd_wrong"); code != http.StatusTooManyRequests {
		t.Fatalf("office device-token guess 21 answered %d, want 429", code)
	}
}

// An admin's list takes effect at once, is stored canonically, audited, counted, and still
// there after a restart; the environment's list stays alongside it.
func TestAdminSetsExemptNetworks(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", "198.18.7.0/24")
	srv, st, cfg := setupTestServer(t)
	admin := loginAs(t, srv, st, "alice", "admin")
	get := func(s *api.Server) *httptest.ResponseRecorder {
		return exemptCall(t, s, "GET", "/api/settings/rate-limit-networks", "", "192.0.2.1:1234", "", admin)
	}

	networks, environment, exempted := networksStatus(t, get(srv))
	if networks != "" || environment != "198.18.7.0/24" || exempted != 0 {
		t.Fatalf("before: networks=%q environment=%q exempted=%v", networks, environment, exempted)
	}

	networks, _, _ = networksStatus(t, putNetworks(t, srv, admin, " 203.0.113.77/24 , 2001:db8:1::/48"))
	if networks != "203.0.113.0/24,2001:db8:1::/48" {
		t.Fatalf("stored %q, want the canonical list", networks)
	}
	if limited(t, srv, officePeer) || limited(t, srv, "198.18.7.50:41000") || limited(t, srv, "[2001:db8:1::5]:41000") {
		t.Fatal("an allowlisted network was throttled")
	}
	if _, _, exempted = networksStatus(t, get(srv)); exempted != 75 {
		t.Fatalf("exempted = %v, want 75", exempted)
	}

	records, _, err := st.Audit().ListAuditRecords(context.Background(), 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	audited := false
	for _, rec := range records {
		if rec.Action == "admin.rate_limit_networks" && rec.UserID == "usr_alice" &&
			rec.Details == `from="" to="203.0.113.0/24,2001:db8:1::/48"` {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("no audit row for the change in %d records", len(records))
	}

	restarted := api.NewServer(cfg, st)
	if networks, _, _ = networksStatus(t, get(restarted)); networks != "203.0.113.0/24,2001:db8:1::/48" {
		t.Fatalf("after restart the list reads %q", networks)
	}
	if limited(t, restarted, officePeer) {
		t.Fatal("the stored list was not loaded at startup")
	}

	// Clearing it restores the limit for the office; the environment's network stays exempt.
	if networks, _, _ = networksStatus(t, putNetworks(t, srv, admin, "")); networks != "" {
		t.Fatalf("cleared list reads %q", networks)
	}
	if !limited(t, srv, "203.0.113.200:41000") {
		t.Fatal("a removed network is still exempt")
	}
	if limited(t, srv, "198.18.7.51:41000") {
		t.Fatal("clearing the admin list dropped the environment's network")
	}
}

// Members cannot read or change the list; anonymous callers neither.
func TestExemptNetworksAreAdminOnly(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	member := loginAs(t, srv, st, "bob", "user")
	if w := exemptCall(t, srv, "GET", "/api/settings/rate-limit-networks", "", "192.0.2.1:1234", "", member); w.Code != http.StatusForbidden {
		t.Fatalf("member GET: %d", w.Code)
	}
	if w := putNetworks(t, srv, member, officeRange); w.Code != http.StatusForbidden {
		t.Fatalf("member PUT: %d", w.Code)
	}
	if w := putNetworks(t, srv, nil, officeRange); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("anonymous PUT: %d", w.Code)
	}
	if !limited(t, srv, officePeer) {
		t.Fatal("a refused PUT exempted the office")
	}
}

// A list that would switch the limit off widely is refused and nothing changes.
func TestExemptNetworksRefuseBroadOrInvalidRanges(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "alice", "admin")
	if w := putNetworks(t, srv, admin, officeRange); w.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/8", "2001:db8::/32", "office", "127.0.0.1", "203.0.113.0/24,nope"} {
		w := putNetworks(t, srv, admin, bad)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "netallow:") {
			t.Errorf("PUT %q answered %d %s, want 400 with the reason", bad, w.Code, w.Body.String())
		}
	}
	get := exemptCall(t, srv, "GET", "/api/settings/rate-limit-networks", "", "192.0.2.1:1234", "", admin)
	if networks, _, _ := networksStatus(t, get); networks != officeRange {
		t.Fatalf("a refused list replaced the stored one: %q", networks)
	}
	if !limited(t, srv, "10.9.9.9:41000") {
		t.Fatal("an address in a refused range was exempted")
	}
}

// A stored list that no longer parses is dropped at startup for the environment's list:
// fewer exemptions, never more.
func TestUnreadableStoredExemptNetworksAreDropped(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", "198.18.7.0/24")
	_, st, cfg := setupTestServer(t)
	if err := st.Settings().SetSetting(context.Background(), "rate_limit_exempt_networks", "203.0.113.0/24,10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	srv := api.NewServer(cfg, st)
	if !limited(t, srv, officePeer) || !limited(t, srv, "10.9.9.9:41000") {
		t.Fatal("a network from the unparsable stored list was exempted")
	}
	if limited(t, srv, "198.18.7.50:41000") {
		t.Fatal("the environment's network lost its exemption")
	}
}

// davBasic sends one WebDAV PROPFIND with an app password from peer.
func davBasic(srv *api.Server, peer, user, pass string) int {
	r := httptest.NewRequest("PROPFIND", "/dav/", nil)
	r.Header.Set("Depth", "0")
	r.RemoteAddr = peer
	r.SetBasicAuth(user, pass)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w.Code
}

// The two limits whose callers pick the account name, sign-in and WebDAV, each let an office
// mint at most officeBudget keys a minute; over a 15-minute account window that must stay
// well under the limiter's capacity, or office traffic could evict live account windows.
func TestOfficeBudgetFitsTheLimiter(t *testing.T) {
	if minted := 2 * api.OfficeBudgetForTest * 15; minted > api.AttemptsCapForTest*2/3 {
		t.Fatalf("office traffic can mint %d keys in 15 minutes, against a cap of %d", minted, api.AttemptsCapForTest)
	}
}

// All listed networks share one sign-in budget; once it is spent, each office address meets
// its own ordinary per-IP window again.
func TestOfficeBudgetFallsBackToThePerIPLimit(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	admin := loginAs(t, srv, st, "alice", "admin")

	n := api.OfficeBudgetForTest + 20
	for i := 1; i <= n; i++ {
		if code := signIn(t, srv, officePeer, "", i); code == http.StatusTooManyRequests {
			t.Fatalf("office attempt %d throttled, want the budget of %d and then a window of 20", i, api.OfficeBudgetForTest)
		}
	}
	if code := signIn(t, srv, officePeer, "", n+1); code != http.StatusTooManyRequests {
		t.Fatalf("office attempt %d answered %d, want 429", n+1, code)
	}
	if code := signIn(t, srv, "203.0.113.77:41000", "", 1); code == http.StatusTooManyRequests {
		t.Fatal("a second office address was throttled by the first one's window")
	}
	// Only the requests that skipped the per-IP window are counted as exempted.
	w := exemptCall(t, srv, "GET", "/api/settings/rate-limit-networks", "", "192.0.2.1:1234", "", admin)
	if _, _, exempted := networksStatus(t, w); exempted != float64(api.OfficeBudgetForTest) {
		t.Fatalf("exempted = %v, want %d", exempted, api.OfficeBudgetForTest)
	}
}

// WebDAV app-password failures from the office draw on their own shared budget and then meet
// the per-client window; a second office address keeps its own window.
func TestOfficeDAVBudgetFallsBackToThePerIPLimit(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	n := api.OfficeBudgetForTest + 20
	for i := 1; i <= n; i++ {
		if code := davBasic(srv, officePeer, fmt.Sprintf("stale%d", i), "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("office failure %d answered %d, want 401", i, code)
		}
	}
	if code := davBasic(srv, officePeer, "stale0", "wrong"); code != http.StatusTooManyRequests {
		t.Fatalf("office failure %d answered %d, want 429", n+1, code)
	}
	if code := davBasic(srv, "203.0.113.77:41000", "stale0", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("a second office address answered %d, want its own window's 401", code)
	}
}

// Office WebDAV failures under more distinct usernames than the limiter holds cannot evict a
// live sign-in window: an account exhausted from the office stays shut there.
func TestOfficeAccountChurnKeepsLiveLoginWindows(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	createLocalUser(t, st, "carol", "SuperSecretPass123!")
	try := func(pass string) int {
		return exemptCall(t, srv, "POST", "/api/auth/login", `{"username":"carol","password":"`+pass+`"}`, officePeer, "", nil).Code
	}
	for i := 0; i < 10; i++ {
		try("WrongPass123!")
	}
	for i := 0; i <= api.AttemptsCapForTest; i++ {
		davBasic(srv, officePeer, fmt.Sprintf("churn%d", i), "wrong")
	}
	if keys := len(api.AttemptKeysForTest(srv)); keys > api.OfficeBudgetForTest+100 {
		t.Fatalf("office churn left %d limiter keys", keys)
	}
	if code := try("SuperSecretPass123!"); code != http.StatusUnauthorized {
		t.Fatalf("carol's exhausted window answered %d after the churn, want 401", code)
	}
}

// Concurrent wrong guesses at one account from the office reserve their place in the account
// window before the lookup: no more than ten reach verification, however they interleave.
func TestConcurrentOfficeDAVGuessesCannotOutrunTheAccountWindow(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	if st.Drive() == nil {
		t.Skip("SQLite drive")
	}
	var checks atomic.Int64
	api.SetAppPasswordCheckForTest(srv, func(context.Context, string, string) (string, error) {
		checks.Add(1)
		time.Sleep(20 * time.Millisecond) // widen the window a check-then-record limiter loses
		return "", drive.ErrDenied
	})
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); davBasic(srv, officePeer, "hal", "wrong") }()
	}
	wg.Wait()
	if n := checks.Load(); n > 10 {
		t.Fatalf("%d concurrent guesses reached verification, want at most 10", n)
	}
}

// countingUsers counts the account lookups that precede every sign-in password verification.
type countingUsers struct {
	store.UserStore
	lookups *atomic.Int64
}

func (c countingUsers) GetUserByUsername(ctx context.Context, name string) (*store.User, error) {
	c.lookups.Add(1)
	return c.UserStore.GetUserByUsername(ctx, name)
}

type countingStore struct {
	store.Store
	lookups *atomic.Int64
}

func (c countingStore) Users() store.UserStore { return countingUsers{c.Store.Users(), c.lookups} }

// The same for sign-in: concurrent office guesses at one account reach the lookup and the
// password hash at most ten times.
func TestConcurrentOfficeSignInsCannotOutrunTheAccountWindow(t *testing.T) {
	t.Setenv("KY_RATE_LIMIT_EXEMPT_NETWORKS", officeRange)
	srv, st, _ := setupTestServer(t)
	var lookups atomic.Int64
	api.SetStoreForTest(srv, countingStore{st, &lookups})
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			exemptCall(t, srv, "POST", "/api/auth/login", `{"username":"carol","password":"WrongPass123!"}`, officePeer, "", nil)
		}()
	}
	wg.Wait()
	if n := lookups.Load(); n > 10 {
		t.Fatalf("%d concurrent guesses reached verification, want at most 10", n)
	}
}
