package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/appversion"
)

func TestOpenInNewTabPreferenceAPI(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	cookie := loginAs(t, srv, st, "prefuser", "user")
	put := func(body string, csrf bool, c *http.Cookie) int {
		req := httptest.NewRequest("PUT", "/api/auth/preferences", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if c != nil {
			req.AddCookie(c)
		}
		if csrf {
			api.SignCSRFForTest(srv, req)
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code
	}
	me := func() bool {
		w := do(t, srv, "GET", "/api/auth/me", cookie)
		var out struct {
			OpenInNewTab *bool `json:"open_in_new_tab"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.OpenInNewTab == nil {
			t.Fatalf("me: %s", w.Body.String())
		}
		return *out.OpenInNewTab
	}

	if !me() {
		t.Fatal("default should open in a new tab")
	}
	if code := put(`{"open_in_new_tab":false}`, true, cookie); code != http.StatusOK {
		t.Fatalf("set: %d", code)
	}
	if me() {
		t.Fatal("preference not saved")
	}
	for name, code := range map[string]int{
		"no session":    put(`{"open_in_new_tab":true}`, true, nil),
		"no csrf":       put(`{"open_in_new_tab":true}`, false, cookie),
		"missing field": put(`{}`, true, cookie),
		"unknown field": put(`{"open_in_new_tab":true,"role":"admin"}`, true, cookie),
		"wrong type":    put(`{"open_in_new_tab":"yes"}`, true, cookie),
	} {
		if code < 400 {
			t.Errorf("%s: accepted with %d", name, code)
		}
	}
	if me() {
		t.Fatal("a refused request changed the preference")
	}
}

// The About dialog's version badge reads /api/auth/me; signed-out callers never see the version.
func TestMeReportsVersionOnlyWhenSignedIn(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	read := func(c *http.Cookie) map[string]any {
		var out map[string]any
		if err := json.Unmarshal(do(t, srv, "GET", "/api/auth/me", c).Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if v := read(loginAs(t, srv, st, "veruser", "user"))["version"]; v != appversion.String() {
		t.Fatalf("signed in: version %v, want %q", v, appversion.String())
	}
	if out := read(nil); out["version"] != nil || out["authenticated"] != false {
		t.Fatalf("signed out: %v", out)
	}
}
