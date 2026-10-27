package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/password"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/auth"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

func postLogin(srv *api.Server, ip string, body map[string]string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = ip + ":40000"
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	hash, _ := password.Hash("SuperSecretPass123!")
	for _, u := range []*store.User{
		{ID: "usr_on", Username: "on", PasswordHash: hash, Role: "user", Status: "active", SSOProvider: "local"},
		{ID: "usr_off", Username: "off", PasswordHash: hash, Role: "user", Status: "inactive", SSOProvider: "local"},
	} {
		if err := st.Users().CreateUser(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	var bodies []string
	for i, attempt := range []map[string]string{
		{"username": "nobody", "password": "SuperSecretPass123!"},
		{"username": "off", "password": "SuperSecretPass123!"},
		{"username": "on", "password": "WrongPassword123!"},
	} {
		w := postLogin(srv, "192.0.2."+strconv.Itoa(i+1), attempt)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s: got %d %s", attempt["username"], w.Code, w.Body)
		}
		bodies = append(bodies, w.Body.String())
	}
	if bodies[0] != bodies[1] || bodies[1] != bodies[2] {
		t.Fatalf("login failures differ: %q", bodies)
	}
}

func TestLoginLimitsGuessesPerAccountAcrossClients(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	hash, _ := password.Hash("SuperSecretPass123!")
	if err := st.Users().CreateUser(context.Background(), &store.User{ID: "usr_t", Username: "target", PasswordHash: hash, Role: "user", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 10; i++ {
		if w := postLogin(srv, "198.51.100."+strconv.Itoa(i), map[string]string{"username": "Target", "password": "WrongPassword123!"}); w.Code != http.StatusUnauthorized {
			t.Fatalf("guess %d: %d", i, w.Code)
		}
	}
	if w := postLogin(srv, "198.51.100.99", map[string]string{"username": "target", "password": "SuperSecretPass123!"}); w.Code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt from a new client: got %d, want 429", w.Code)
	}
}

func solvePoW(t *testing.T, srv *api.Server) string {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/pow-challenge", nil))
	var c auth.PoWChallenge
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= c.MaxNumber; i++ {
		h := sha256.Sum256([]byte(c.Salt + strconv.Itoa(i)))
		if hex.EncodeToString(h[:]) == c.Challenge {
			raw, _ := json.Marshal(auth.PoWSolution{Algorithm: c.Algorithm, Salt: c.Salt, Challenge: c.Challenge, Number: i, MaxNumber: c.MaxNumber, ExpiresAt: c.ExpiresAt, Signature: c.Signature})
			return base64.StdEncoding.EncodeToString(raw)
		}
	}
	t.Fatal("unsolvable challenge")
	return ""
}

func TestPoWSolutionIsSingleUse(t *testing.T) {
	srv, _, cfg := setupTestServer(t)
	cfg.Captcha.Provider = "pow"
	cfg.Captcha.DifficultyPoW = 2000
	token := solvePoW(t, srv)
	body := map[string]string{"username": "nobody", "password": "WrongPassword123!", "captcha_token": token}
	if w := postLogin(srv, "192.0.2.10", body); w.Code != http.StatusUnauthorized {
		t.Fatalf("first use: %d %s", w.Code, w.Body)
	}
	if w := postLogin(srv, "192.0.2.11", body); w.Code != http.StatusForbidden {
		t.Fatalf("replayed solution: got %d, want 403", w.Code)
	}
}

func TestCredentialRoutesRefuseFormPosts(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	for _, path := range []string{"/api/auth/login", "/api/auth/mfa/totp", "/api/auth/mfa/recovery-code"} {
		for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
			req := httptest.NewRequest("POST", path, strings.NewReader(`{"username":"a","password":"b"}`))
			if ct != "" {
				req.Header.Set("Content-Type", ct)
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("%s as %q: got %d, want 415", path, ct, w.Code)
			}
		}
	}
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("JSON with parameters: got %d, want 400", w.Code)
	}
}

func TestCrossSiteWritesAreRefused(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	session := loginAs(t, srv, st, "xsite", "admin")
	for _, tc := range []struct{ header, value string }{
		{"Origin", "https://evil.example"},
		{"Origin", "null"},
		{"Sec-Fetch-Site", "cross-site"},
		{"Sec-Fetch-Site", "same-site"},
	} {
		for _, path := range []string{"/api/auth/login", "/api/auth/mfa/totp", "/api/settings/theme"} {
			req := httptest.NewRequest("POST", path, strings.NewReader(`{"theme":"oled"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(tc.header, tc.value)
			if path == "/api/settings/theme" {
				req.AddCookie(session)
				api.SignCSRFForTest(srv, req)
			}
			w := httptest.NewRecorder()
			srv.ServeHTTP(w, req)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s with %s=%s: got %d, want 403", path, tc.header, tc.value, w.Code)
			}
		}
	}
	req := httptest.NewRequest("POST", "/api/settings/theme", strings.NewReader(`{"theme":"oled"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", cfg.Server.AppURL)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(session)
	api.SignCSRFForTest(srv, req)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("same-origin write: %d %s", w.Code, w.Body)
	}
	// The signed webhook is server-to-server: a foreign Origin still reaches the signature check.
	req = httptest.NewRequest("POST", "/api/sso/kysignon/sync", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://identity.example")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("webhook: got %d, want 401", w.Code)
	}
}

func TestCSRFTokenIsBoundToTheSession(t *testing.T) {
	srv, st, _ := setupTestServer(t)
	hash, _ := password.Hash("SuperSecretPass123!")
	if err := st.Users().CreateUser(context.Background(), &store.User{ID: "usr_c", Username: "csrf", PasswordHash: hash, Role: "admin", Status: "active", SSOProvider: "local"}); err != nil {
		t.Fatal(err)
	}
	login := postLogin(srv, "192.0.2.20", map[string]string{"username": "csrf", "password": "SuperSecretPass123!"})
	var session, csrf *http.Cookie
	for _, c := range login.Result().Cookies() {
		switch c.Name {
		case auth.SessionCookieName:
			session = c
		case auth.CSRFCookieName:
			csrf = c
		}
	}
	if session == nil || csrf == nil {
		t.Fatal("login set no session/CSRF cookies")
	}
	write := func(session *http.Cookie, cookie, header string) int {
		req := httptest.NewRequest("POST", "/api/settings/theme", strings.NewReader(`{"theme":"oled"}`))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(session)
		req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: cookie})
		req.Header.Set(auth.HeaderCSRF, header)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code
	}
	if got := write(session, "tossed", "tossed"); got != http.StatusForbidden {
		t.Fatalf("planted cookie/header pair: got %d, want 403", got)
	}
	if got := write(session, csrf.Value, csrf.Value); got != http.StatusOK {
		t.Fatalf("issued token: got %d", got)
	}
	other := loginAs(t, srv, st, "other", "admin")
	if got := write(other, csrf.Value, csrf.Value); got != http.StatusForbidden {
		t.Fatalf("token from another session: got %d", got)
	}
	// /me re-issues the cookie for the presented session.
	me := httptest.NewRequest("GET", "/api/auth/me", nil)
	me.AddCookie(session)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, me)
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CSRFCookieName && c.Value == csrf.Value {
			return
		}
	}
	t.Fatal("/api/auth/me did not refresh the CSRF cookie")
}

func TestRecoveryCodeIsSingleUse(t *testing.T) {
	srv, st, cfg := setupTestServer(t)
	ctx := context.Background()
	codes, hashed, err := auth.GenerateRecoveryCodes(4)
	if err != nil {
		t.Fatal(err)
	}
	enc, _ := crypto.EncryptAESGCM([]byte("JBSWY3DPEHPK3PXP"), cfg.Security.EncryptionKey)
	if err := st.Users().CreateUser(ctx, &store.User{ID: "usr_rc", Username: "rc", Role: "user", Status: "active", SSOProvider: "local", TOTPEnabled: true, TOTPSecretEnc: enc, RecoveryCodesHash: hashed}); err != nil {
		t.Fatal(err)
	}
	redeem := func(code string) int {
		raw := crypto.RandomHex(32)
		if err := st.Sessions().CreateMFAChallenge(ctx, &store.MFAChallenge{TokenHash: crypto.SHA256Hex([]byte(raw)), UserID: "usr_rc", ExpiresAt: time.Now().Add(time.Minute)}, ""); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(map[string]string{"mfa_token": raw, "code": code})
		req := httptest.NewRequest("POST", "/api/auth/mfa/recovery-code", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w.Code
	}
	if got := redeem(codes[0]); got != http.StatusOK {
		t.Fatalf("first redemption: %d", got)
	}
	if got := redeem(codes[0]); got != http.StatusUnauthorized {
		t.Fatalf("spent code: got %d, want 401", got)
	}
	if got := redeem(codes[1]); got != http.StatusOK {
		t.Fatalf("unspent code: %d", got)
	}
}

func TestSSOErrorsDoNotLeakDetail(t *testing.T) {
	srv, _, _ := setupTestServer(t)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", "/api/sso/kysignon/login", nil))
	if w.Code != http.StatusBadGateway || w.Body.String() != `{"error":"Sign-on provider unavailable"}`+"\n" {
		t.Fatalf("authorization URL failure: %d %s", w.Code, w.Body)
	}
}

func TestDirectoryWebhookStatusCodes(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	cfg.SSO.KySignOnHMACSecret = "hook-secret-16-bytes"
	srv := api.NewServer(cfg, st)
	call := func(body []byte, secret string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/sso/kysignon/sync", bytes.NewReader(body))
		req.Header.Set("X-KySignOn-Signature", crypto.ComputeHMACSHA256(body, secret))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		return w
	}
	good := []byte(fmt.Sprintf(`{"event":"user.created","id":"s1","username":"web","timestamp":%d}`, time.Now().Unix()))
	if w := call(good, "wrong"); w.Code != http.StatusUnauthorized || w.Body.String() != `{"error":"Invalid webhook signature or timestamp"}`+"\n" {
		t.Fatalf("bad signature: %d %s", w.Code, w.Body)
	}
	if w := call([]byte(`not json`), "hook-secret-16-bytes"); w.Code != http.StatusBadRequest || w.Body.String() != `{"error":"Malformed webhook payload"}`+"\n" {
		t.Fatalf("malformed: %d %s", w.Code, w.Body)
	}
	if w := call(good, "hook-secret-16-bytes"); w.Code != http.StatusOK {
		t.Fatalf("valid: %d %s", w.Code, w.Body)
	}
	st.Close() // an outage is a 500 so the sender retries
	if w := call([]byte(fmt.Sprintf(`{"event":"user.created","id":"s2","username":"web2","timestamp":%d}`, time.Now().Unix())), "hook-secret-16-bytes"); w.Code != http.StatusInternalServerError {
		t.Fatalf("store outage: %d %s", w.Code, w.Body)
	}
}
