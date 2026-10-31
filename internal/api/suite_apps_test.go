package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// fakeSuiteDirectory is KyIdentity's /api/suite/apps: it answers each sub with one app named
// after it, and counts calls per sub.
type fakeSuiteDirectory struct {
	mu    sync.Mutex
	calls map[string]int
	fail  bool
}

func (f *fakeSuiteDirectory) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, secret, ok := r.BasicAuth()
	if r.URL.Path != "/api/suite/apps" || !ok || id != "kydrive" || secret != "suite-secret" {
		http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		return
	}
	sub := r.URL.Query().Get("sub")
	f.mu.Lock()
	f.calls[sub]++
	fail := f.fail
	f.mu.Unlock()
	if fail {
		http.Error(w, "down", http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"apps": []map[string]string{
		{"id": "kydrive", "name": "KyDrive", "url": "https://drive.example.com/api/sso/kysignon/login"},
		{"id": "app-" + sub, "name": sub, "url": "https://" + sub + ".example.com/login"},
	}})
}

func (f *fakeSuiteDirectory) count(sub string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[sub]
}

// loginWithProvider signs in an account carrying a directory link, the way an SSO or SCIM
// account looks once its session exists.
func loginWithProvider(t *testing.T, srv *api.Server, st store.Store, username, provider, subject string) *http.Cookie {
	t.Helper()
	hash, err := password.Hash("SuperSecretPass123!")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Users().CreateUser(context.Background(), &store.User{
		ID: "usr_" + username, Username: username, PasswordHash: hash, Role: "user", Status: "active",
		SSOProvider: provider, SSOSubject: subject,
	}); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"username": username, "password": "SuperSecretPass123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	t.Fatalf("login %s: %d %s", username, w.Code, w.Body.String())
	return nil
}

func TestSuiteApps(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	dir := &fakeSuiteDirectory{calls: map[string]int{}}
	idp := httptest.NewServer(dir)
	t.Cleanup(idp.Close)
	cfg.SSO.Enabled, cfg.SSO.KySignOnIssuer, cfg.SSO.KySignOnClientID, cfg.SSO.KySignOnSecret = true, idp.URL, "kydrive", "suite-secret"
	now := time.Now()
	api.SetClockForTest(srv, func() time.Time { return now })

	apps := func(c *http.Cookie) []map[string]string {
		t.Helper()
		w := do(t, srv, http.MethodGet, "/api/suite/apps", c)
		var out struct {
			Apps []map[string]string `json:"apps"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Apps == nil {
			t.Fatalf("suite apps: %d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("suite apps must not be cached by the browser")
		}
		return out.Apps
	}

	if w := do(t, srv, http.MethodGet, "/api/suite/apps", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", w.Code)
	}

	alice := loginWithProvider(t, srv, st, "alice", "kysignon", "sub-alice")
	bob := loginWithProvider(t, srv, st, "bob", "scim", "sub-bob")
	got := apps(alice)
	if len(got) != 2 || got[0]["id"] != "kydrive" || got[0]["url"] != "https://drive.example.com/api/sso/kysignon/login" || got[1]["id"] != "app-sub-alice" {
		t.Fatalf("SSO user: %v", got)
	}
	if got := apps(bob); len(got) != 2 || got[1]["id"] != "app-sub-bob" {
		t.Fatalf("SCIM user got another user's list or none: %v", got)
	}

	// Cached per user for a minute, then asked again.
	apps(alice)
	if n := dir.count("sub-alice"); n != 1 {
		t.Fatalf("cached list re-fetched: %d calls", n)
	}
	now = now.Add(61 * time.Second)
	apps(alice)
	if n := dir.count("sub-alice"); n != 2 {
		t.Fatalf("expired list not re-fetched: %d calls", n)
	}

	// Local accounts never reach KyIdentity.
	local := loginAs(t, srv, st, "carol", "user")
	if got := apps(local); len(got) != 0 {
		t.Fatalf("local user: %v", got)
	}
	if n := dir.count(""); n != 0 {
		t.Fatalf("local user reached KyIdentity %d times", n)
	}

	// A failure is an empty list and is not cached.
	now = now.Add(61 * time.Second)
	dir.mu.Lock()
	dir.fail = true
	dir.mu.Unlock()
	if got := apps(alice); len(got) != 0 {
		t.Fatalf("KyIdentity failure: %v", got)
	}
	dir.mu.Lock()
	dir.fail = false
	dir.mu.Unlock()
	if got := apps(alice); len(got) != 2 {
		t.Fatalf("failure was cached: %v", got)
	}

	// Without a client secret there is nothing to authenticate with.
	now = now.Add(61 * time.Second)
	cfg.SSO.KySignOnSecret = ""
	if got := apps(bob); len(got) != 0 {
		t.Fatalf("no client secret: %v", got)
	}
}
