package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/totp"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

// post sends JSON; a nil cookie posts without a session, csrf=false omits the CSRF header.
func post(t *testing.T, srv *api.Server, path string, cookie *http.Cookie, csrf bool, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
		if csrf {
			api.SignCSRFForTest(srv, req)
		}
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// signIn runs a password login and, when MFA is required, returns its token instead.
func signIn(t *testing.T, srv *api.Server, username string) (*http.Cookie, string) {
	t.Helper()
	w := post(t, srv, "/api/auth/login", nil, false, map[string]string{"username": username, "password": "SuperSecretPass123!"})
	if w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	var out struct {
		MFAToken string `json:"mfa_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return sessionCookie(w), out.MFAToken
}

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

func code(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.Code(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMFAEnrolment(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	ctx := context.Background()
	session := loginAs(t, srv, st, "mia", "user")
	other, _ := signIn(t, srv, "mia")

	if w := post(t, srv, "/api/auth/mfa/enroll", session, false, nil); w.Code != http.StatusForbidden {
		t.Fatalf("enroll without CSRF: %d, want 403", w.Code)
	}
	w := post(t, srv, "/api/auth/mfa/enroll", session, true, nil)
	var enrol struct {
		Secret string `json:"secret"`
		URI    string `json:"otpauth_uri"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &enrol) != nil || enrol.Secret == "" || len(enrol.URI) < 15 || enrol.URI[:15] != "otpauth://totp/" {
		t.Fatalf("enroll: %d %s", w.Code, w.Body)
	}
	user, _ := st.Users().GetUserByID(ctx, "usr_mia")
	if user.TOTPEnabled {
		t.Fatal("enroll enabled MFA before confirmation")
	}
	if got, legacy, err := auth.OpenTOTPSecret(cfg.Security.EncryptionKey, "usr_mia", user.TOTPSecretEnc); err != nil || legacy || got != enrol.Secret {
		t.Fatalf("pending secret not sealed in the derived form: %v %v", legacy, err)
	}

	// A wrong code leaves MFA off and password login single-factor.
	if w := post(t, srv, "/api/auth/mfa/enroll/confirm", session, true, map[string]string{"code": "000000x"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", w.Code)
	}
	if user, _ := st.Users().GetUserByID(ctx, "usr_mia"); user.TOTPEnabled {
		t.Fatal("a wrong code enabled MFA")
	}

	now := code(t, enrol.Secret, time.Now())
	w = post(t, srv, "/api/auth/mfa/enroll/confirm", session, true, map[string]string{"code": now})
	var confirmed struct {
		Codes []string `json:"recovery_codes"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &confirmed) != nil || len(confirmed.Codes) != 10 {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	if !signedIn(t, srv, session) || signedIn(t, srv, other) {
		t.Fatal("enable must keep the current session and revoke the others")
	}
	if w := post(t, srv, "/api/auth/mfa/enroll", session, true, nil); w.Code != http.StatusConflict {
		t.Fatalf("enroll while enabled: %d, want 409", w.Code)
	}

	// Login now requires MFA; the confirm code cannot be replayed there.
	cookie, token := signIn(t, srv, "mia")
	if cookie != nil || token == "" {
		t.Fatal("login after enrolment did not require MFA")
	}
	if w := post(t, srv, "/api/auth/mfa/totp", nil, false, map[string]string{"mfa_token": token, "code": now}); w.Code != http.StatusUnauthorized {
		t.Fatalf("confirm code replayed at login: %d", w.Code)
	}
	_, token = signIn(t, srv, "mia")
	if w := post(t, srv, "/api/auth/mfa/totp", nil, false, map[string]string{"mfa_token": token, "code": code(t, enrol.Secret, time.Now().Add(30*time.Second))}); w.Code != http.StatusOK {
		t.Fatalf("TOTP login: %d %s", w.Code, w.Body)
	}

	// Recovery codes from confirm work exactly once.
	_, token = signIn(t, srv, "mia")
	if w := post(t, srv, "/api/auth/mfa/recovery-code", nil, false, map[string]string{"mfa_token": token, "code": confirmed.Codes[0]}); w.Code != http.StatusOK {
		t.Fatalf("recovery login: %d %s", w.Code, w.Body)
	}
	_, token = signIn(t, srv, "mia")
	if w := post(t, srv, "/api/auth/mfa/recovery-code", nil, false, map[string]string{"mfa_token": token, "code": confirmed.Codes[0]}); w.Code != http.StatusUnauthorized {
		t.Fatalf("spent recovery code: %d", w.Code)
	}

	// Disable needs the password and a second factor.
	_, token = signIn(t, srv, "mia")
	other = sessionCookie(post(t, srv, "/api/auth/mfa/recovery-code", nil, false, map[string]string{"mfa_token": token, "code": confirmed.Codes[1]}))
	if w := post(t, srv, "/api/auth/mfa/disable", session, true, map[string]string{"password": "wrong-password-123", "code": confirmed.Codes[2]}); w.Code != http.StatusUnauthorized {
		t.Fatalf("disable with wrong password: %d", w.Code)
	}
	if w := post(t, srv, "/api/auth/mfa/disable", session, true, map[string]string{"password": "SuperSecretPass123!", "code": "123456"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("disable with wrong code: %d", w.Code)
	}
	if w := post(t, srv, "/api/auth/mfa/disable", session, true, map[string]string{"password": "SuperSecretPass123!", "code": confirmed.Codes[2]}); w.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	if !signedIn(t, srv, session) || signedIn(t, srv, other) {
		t.Fatal("disable must keep the current session and revoke the others")
	}
	user, _ = st.Users().GetUserByID(ctx, "usr_mia")
	if user.TOTPEnabled || user.TOTPSecretEnc != "" || user.RecoveryCodesHash != "[]" {
		t.Fatalf("disable left MFA state: %+v", user)
	}
	if cookie, _ := signIn(t, srv, "mia"); cookie == nil {
		t.Fatal("login after disable still requires MFA")
	}
}

func TestMFADisableAcceptsTOTP(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "tom", "user")
	var enrol struct {
		Secret string `json:"secret"`
	}
	_ = json.Unmarshal(post(t, srv, "/api/auth/mfa/enroll", session, true, nil).Body.Bytes(), &enrol)
	if w := post(t, srv, "/api/auth/mfa/enroll/confirm", session, true, map[string]string{"code": code(t, enrol.Secret, time.Now())}); w.Code != http.StatusOK {
		t.Fatalf("confirm: %d", w.Code)
	}
	if w := post(t, srv, "/api/auth/mfa/disable", session, true, map[string]string{"password": "SuperSecretPass123!", "code": code(t, enrol.Secret, time.Now().Add(30*time.Second))}); w.Code != http.StatusOK {
		t.Fatalf("disable with TOTP: %d %s", w.Code, w.Body)
	}
}

func TestMFAEnrolmentLimitsAndScope(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	session := loginAs(t, srv, st, "lim", "user")
	if w := post(t, srv, "/api/auth/mfa/enroll", nil, false, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("enroll without session: %d", w.Code)
	}
	if w := post(t, srv, "/api/auth/mfa/enroll/confirm", session, true, map[string]string{"code": "123456"}); w.Code != http.StatusConflict {
		t.Fatalf("confirm without enrolment: %d", w.Code)
	}
	post(t, srv, "/api/auth/mfa/enroll", session, true, nil)
	last := 0
	for range 6 {
		last = post(t, srv, "/api/auth/mfa/enroll/confirm", session, true, map[string]string{"code": "000000"}).Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("sixth confirm: %d, want 429", last)
	}
	// Directory accounts sign in through their provider, which owns their MFA.
	user, _ := st.Users().GetUserByID(context.Background(), "usr_lim")
	user.SSOProvider = "kysignon"
	if err := st.Users().UpdateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if w := post(t, srv, "/api/auth/mfa/enroll", session, true, nil); w.Code != http.StatusConflict {
		t.Fatalf("directory account enroll: %d, want 409", w.Code)
	}
}

// A secret sealed by an older release (raw key, no AAD) still signs in, once, and is resealed.
func TestLegacyTOTPSecretIsResealed(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	ctx := context.Background()
	loginAs(t, srv, st, "old", "user")
	secret, _ := totp.GenerateSecret()
	legacy, _ := crypto.EncryptAESGCM([]byte(secret), cfg.Security.EncryptionKey)
	user, _ := st.Users().GetUserByID(ctx, "usr_old")
	user.TOTPEnabled, user.TOTPSecretEnc = true, legacy
	if err := st.Users().UpdateUser(ctx, user); err != nil {
		t.Fatal(err)
	}
	_, token := signIn(t, srv, "old")
	if w := post(t, srv, "/api/auth/mfa/totp", nil, false, map[string]string{"mfa_token": token, "code": code(t, secret, time.Now())}); w.Code != http.StatusOK {
		t.Fatalf("legacy login: %d %s", w.Code, w.Body)
	}
	user, _ = st.Users().GetUserByID(ctx, "usr_old")
	if got, wasLegacy, err := auth.OpenTOTPSecret(cfg.Security.EncryptionKey, "usr_old", user.TOTPSecretEnc); err != nil || wasLegacy || got != secret {
		t.Fatalf("row not resealed: legacy=%v err=%v", wasLegacy, err)
	}
	// Copied into another user's row, the resealed secret does not open.
	if _, _, err := auth.OpenTOTPSecret(cfg.Security.EncryptionKey, "usr_mia", user.TOTPSecretEnc); err == nil {
		t.Fatal("resealed secret opened for another user")
	}
}
