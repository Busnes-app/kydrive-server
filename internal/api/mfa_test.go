package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c
		}
	}
	return nil
}

func signedIn(t *testing.T, srv *api.Server, cookie *http.Cookie) bool {
	t.Helper()
	return bytes.Contains(do(t, srv, "GET", "/api/auth/me", cookie).Body.Bytes(), []byte(`"authenticated":true`))
}

// Local two-factor sign-in is removed (two-factor is KyIdentity's). An account that had it on
// keeps its stored secret and codes, unused, and signs in with its password alone.
func TestLocalMFAIsGone(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	ctx := context.Background()
	hash, _ := password.Hash("SuperSecretPass123!")
	if err := st.Users().CreateUser(ctx, &store.User{
		ID: "usr_mfa", Username: "mfa", PasswordHash: hash, Role: "user", Status: "active", SSOProvider: "local",
		TOTPEnabled: true, TOTPSecretEnc: "sealed", RecoveryCodesHash: `["a","b"]`,
	}); err != nil {
		t.Fatal(err)
	}

	w := postLogin(srv, "192.0.2.20", map[string]string{"username": "mfa", "password": "SuperSecretPass123!"})
	var out struct {
		Authenticated bool `json:"authenticated"`
		MFARequired   bool `json:"mfa_required"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.Authenticated || out.MFARequired {
		t.Fatalf("password login with totp_enabled=1: %d %s", w.Code, w.Body)
	}
	session := sessionCookie(w)
	if session == nil || !signedIn(t, srv, session) {
		t.Fatal("password login with totp_enabled=1 issued no working session")
	}
	user, err := st.Users().GetUserByID(ctx, "usr_mfa")
	if err != nil || !user.TOTPEnabled || user.TOTPSecretEnc != "sealed" || user.RecoveryCodesHash != `["a","b"]` {
		t.Fatalf("stored MFA data changed: %+v %v", user, err)
	}

	for _, path := range []string{"/api/auth/mfa/totp", "/api/auth/mfa/recovery-code", "/api/auth/mfa/enroll", "/api/auth/mfa/enroll/confirm", "/api/auth/mfa/disable"} {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(session)
		api.SignCSRFForTest(srv, req)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", path, w.Code)
		}
	}
}
