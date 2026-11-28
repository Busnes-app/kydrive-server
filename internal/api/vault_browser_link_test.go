package api_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/api"
	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

func vaultSigningKey(t *testing.T) *hybridsig.PrivateKey {
	t.Helper()
	k, err := hybridsig.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestVaultBrowserScope(t *testing.T) {
	_, st, cfg := setupTestServer(t)
	const origin = "https://editor.example.test"
	cfg.Vault = config.VaultConfig{Publisher: vaultSigningKey(t), Witness: vaultSigningKey(t).Public(), BrowserOrigin: origin}
	srv := api.NewServer(cfg, st)
	user := loginAs(t, srv, st, "vaultscope", "user")
	digest := sha256.Sum256([]byte(linkVerifier))
	body := map[string]string{
		"redirect": "https://other.example.test/", "state": "test-state", "name": "Vault browser", "platform": "vault-browser",
		"challenge": base64.RawURLEncoding.EncodeToString(digest[:]),
	}
	approve := func() *httptest.ResponseRecorder {
		data, _ := json.Marshal(body)
		return send(t, srv, "POST", "/api/devices/approve", string(data), user, "")
	}
	if out := approve(); out.Code != 400 {
		t.Fatalf("external callback: %d %s", out.Code, out.Body)
	}
	body["redirect"] = origin + "/"
	out := approve()
	var approval struct{ Redirect string }
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &approval) != nil {
		t.Fatalf("approval: %d %s", out.Code, out.Body)
	}
	callback, err := url.Parse(approval.Redirect)
	if err != nil || callback.Query().Get("state") != body["state"] || callback.Query().Get("code") == "" {
		t.Fatal("invalid approval callback")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	spki, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	tokenRequest := url.Values{"code": {callback.Query().Get("code")}, "verifier": {linkVerifier}, "publicKey": {base64.StdEncoding.EncodeToString(spki)}}
	exchange := func(path, site string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(tokenRequest.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if site != "" {
			r.Header.Set("Origin", site)
		}
		return serve(srv, r)
	}
	if out := exchange("/api/vault-browser/token", evilOrigin); out.Code != 403 || out.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("wrong origin exchange: %d %v", out.Code, out.Header())
	}
	if out := exchange("/api/devices/token", ""); out.Code != 400 {
		t.Fatalf("vault code redeemed on native route: %d", out.Code)
	}
	out = exchange("/api/vault-browser/token", origin)
	var browser struct {
		Token string
		Org   string `json:"org_id"`
	}
	if out.Code != 200 || out.Header().Get("Access-Control-Allow-Origin") != origin ||
		out.Header().Get("Access-Control-Allow-Credentials") != "" || json.Unmarshal(out.Body.Bytes(), &browser) != nil ||
		browser.Token == "" || !vault.ID(browser.Org) {
		t.Fatalf("browser exchange: %d %s", out.Code, out.Body)
	}
	if out := exchange("/api/vault-browser/token", origin); out.Code != 400 {
		t.Fatalf("approval code replay: %d", out.Code)
	}
	linked := &signer{key: key, token: browser.Token, origin: api.DeviceOriginForTest(srv), now: time.Now}
	native := linkSigned(t, srv, user, linked.origin)
	const keys = "/api/vault-keys/usr_vaultscope"
	for _, c := range []struct {
		who        *signer
		path, site string
		code       int
	}{
		{linked, keys, origin, 404}, // authenticated, key not yet enrolled
		{linked, keys, "", 401},
		{linked, keys, evilOrigin, 401},
		{linked, "/api/drive/device", "", 401},
		{native, keys, origin, 401},
		{native, keys, "", 404},
	} {
		r := c.who.request("GET", c.path, nil)
		if c.site != "" {
			r.Header.Set("Origin", c.site)
		}
		if out := serve(srv, r); out.Code != c.code {
			t.Errorf("%s, origin %q: %d, expected %d", c.path, c.site, out.Code, c.code)
		}
	}
	for _, path := range []string{"/api/drive/workspaces", "/api/devices/token", "/api/vault-relay/check"} {
		r := httptest.NewRequest("OPTIONS", path, nil)
		r.Header.Set("Origin", origin)
		if out := serve(srv, r); out.Code != 403 || out.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("editor preflight outside vault scope: %s got %d", path, out.Code)
		}
	}
}

func TestVaultBrowserPersonalVault(t *testing.T) {
	if os.Getenv("KYDRIVE_VAULT_BROWSER_FIXTURE") != "1" {
		t.Skip("set KYDRIVE_VAULT_BROWSER_FIXTURE=1 with the built web UI and Chrome")
	}
	_, st, cfg := setupTestServer(t)
	assets := t.TempDir()
	front := httptest.NewServer(http.FileServer(http.Dir(assets)))
	defer front.Close()
	publisher, witnessKey := vaultSigningKey(t), vaultSigningKey(t)
	witness, err := vault.InitializeWitness(t.Context(), filepath.Join(t.TempDir(), "witness.db"), witnessKey, publisher.Public())
	if err != nil {
		t.Fatal(err)
	}
	defer witness.Close()
	witnessHandler := witness.Handler()
	witnessHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != front.URL {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", front.URL)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		witnessHandler.ServeHTTP(w, r)
	}))
	defer witnessHTTP.Close()
	driveHTTP := httptest.NewUnstartedServer(nil)
	cfg.Server.AppURL = "http://" + driveHTTP.Listener.Addr().String()
	cfg.Security.CookieSecure = false
	cfg.Vault = config.VaultConfig{Publisher: publisher, Witness: witnessKey.Public(), BrowserOrigin: front.URL}
	srv := api.NewServer(cfg, st)
	loginAs(t, srv, st, "vaultbrowser", "user")
	loginAs(t, srv, st, "vaultguest", "user")
	driveHTTP.Config.Handler = srv
	driveHTTP.Start()
	defer driveHTTP.Close()
	build := []string{assets, driveHTTP.URL, witnessHTTP.URL, base64.StdEncoding.EncodeToString(witnessKey.Public().Bytes())}
	args := []string{"../../deploy/vault-browser/accept.mjs", driveHTTP.URL, front.URL, witnessHTTP.URL}
	if os.Getenv("KYDRIVE_VAULT_BROWSER_GATEWAY") == "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
		defer cancel()
		origins := front.URL
		fixture := os.Getenv("KYDRIVE_CRYPTPAD_INTEGRATION_FIXTURE")
		if fixture != "" {
			origins += ",http://localhost:18951"
		}
		stage := startVaultGateway(t, ctx, origins, srv)
		args = append(args, stage.url, stage.pin, fixture)
		if fixture != "" {
			build = append(build, "http://localhost:18951", stage.url)
		}
	}
	cmd := exec.CommandContext(t.Context(), "../../scripts/build-vault-browser.sh", build...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build browser: %v %s", err, out)
	}
	cmd = exec.CommandContext(t.Context(), "node", args...)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		t.Logf("%s", out)
	}
	if err != nil {
		t.Fatalf("browser vault flow: %v\n%s", err, out)
	}
}
