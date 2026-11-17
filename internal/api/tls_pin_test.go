package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/store"
)

// The cross-language vector: clients copy testdata/tls-pin-v1.json and compare the exact
// signature (Ed25519 is deterministic; openssl pkeyutl -sign -rawin produced the same bytes).
func TestTLSPinGoldenVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/tls-pin-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Seed        string `json:"seed_hex"`
		IdentityKey string `json:"identity_key"`
		Host        string `json:"host"`
		Pin         string `json:"pin"`
		IssuedAt    int64  `json:"issued_at"`
		Statement   string `json:"statement"`
		Signature   string `json:"signature"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	seed, err := hex.DecodeString(v.Seed)
	if err != nil {
		t.Fatal(err)
	}
	key := ed25519.NewKeyFromSeed(seed)
	if got := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)); got != v.IdentityKey {
		t.Fatalf("identity_key %s, want %s", got, v.IdentityKey)
	}
	if got := string(tlsPinStatement(v.Host, v.Pin, v.IssuedAt)); got != v.Statement {
		t.Fatalf("statement\n%q\nwant\n%q", got, v.Statement)
	}
	if got := base64.StdEncoding.EncodeToString(ed25519.Sign(key, []byte(v.Statement))); got != v.Signature {
		t.Fatalf("signature %s, want %s", got, v.Signature)
	}
}

// tlsOrigin is an HTTPS stand-in for the proxy in front of KyDrive, serving a leaf for
// localhost: its URL (host in mixed case), leaf pin and a pool that trusts it.
func tlsOrigin(t *testing.T) (string, string, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	return "https://LocalHost:" + port, spkiPin(leaf), roots
}

// closedHTTPSOrigin is an https URL with nothing listening, so a probe fails at once.
func closedHTTPSOrigin(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "https://" + addr
}

func pinServer(t *testing.T, appURL string) *Server {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("KY_DATA_DIR", dir)
	t.Setenv("KYDRIVE_IDENTITY_KEY_FILE", "")
	t.Setenv("KY_APP_URL", appURL)
	cfg, err := config.LoadFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), cfg.Database, cfg.Security.EncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewServer(cfg, st)
}

func getPin(s *Server, ip string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/devices/tls-pin", nil)
	r.RemoteAddr = ip + ":1234"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestTLSPinSignsTheServedLeaf(t *testing.T) {
	base, want, roots := tlsOrigin(t)
	s := pinServer(t, base) // host "LocalHost": the signed host is lowercased
	s.pinProbeRoots = roots
	s.config.Security.TLSPinProbe = true
	before := time.Now().Unix()
	rec := getPin(s, "192.0.2.1")
	if rec.Code != 200 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("a signed pin must not be cached")
	}
	var body struct {
		Host, Pin, Signature string
		IssuedAt             int64 `json:"issued_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Pin != want || body.Host != "localhost" {
		t.Fatalf("host %q pin %q, want localhost %q", body.Host, body.Pin, want)
	}
	if body.IssuedAt < before || body.IssuedAt > time.Now().Unix() {
		t.Fatalf("issued_at %d is not now", body.IssuedAt)
	}
	sig, _ := base64.StdEncoding.DecodeString(body.Signature)
	pub, _ := base64.StdEncoding.DecodeString(s.identityKey())
	if !ed25519.Verify(pub, tlsPinStatement(body.Host, body.Pin, body.IssuedAt), sig) {
		t.Fatal("signature does not verify against identity_key")
	}
}

func TestTLSPinRefusals(t *testing.T) {
	base, _, roots := tlsOrigin(t)
	for name, setup := range map[string]func(*Server){
		"no identity key":                         func(s *Server) { s.pinProbeRoots, s.config.Security.IdentityKey = roots, nil },
		"no certificate file and no probe opt-in": func(s *Server) { s.pinProbeRoots, s.config.Security.TLSPinProbe = roots, false },
		"http app url":                            func(s *Server) { s.config.Server.AppURL = "http://drive.example.com" },
		"no servable pin":                         func(s *Server) { s.config.Server.AppURL = closedHTTPSOrigin(t) },
		"untrusted leaf":                          func(s *Server) { s.pinProbeRoots = x509.NewCertPool() },
	} {
		t.Run(name, func(t *testing.T) {
			s := pinServer(t, base)
			s.config.Security.TLSPinProbe = true // each case removes one thing the probe path needs
			setup(s)
			if rec := getPin(s, "192.0.2.1"); rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status %d, want 503: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestTLSPinIsRateLimitedPerIP(t *testing.T) {
	s := pinServer(t, "http://drive.example.com") // 503 without dialling; still counted
	for i := range 10 {
		if rec := getPin(s, "192.0.2.1"); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	rec := getPin(s, "192.0.2.1")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th request: %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := getPin(s, "192.0.2.2"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("another client was limited: %d", rec.Code)
	}
}

// A failed probe is remembered, so a public caller cannot make the server dial a dead
// origin on every request.
func TestTLSPinCachesAFailedProbe(t *testing.T) {
	base, _, roots := tlsOrigin(t)
	s := pinServer(t, base)
	s.config.Security.TLSPinProbe = true
	if rec := getPin(s, "192.0.2.1"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("untrusted probe: %d", rec.Code)
	}
	s.pinProbeRoots = roots
	if rec := getPin(s, "192.0.2.1"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("the failure was not cached: %d", rec.Code)
	}
	s.pinProbeFailedAt = time.Time{}
	if rec := getPin(s, "192.0.2.1"); rec.Code != 200 {
		t.Fatalf("after the cache: %d", rec.Code)
	}
}

func TestIdentityKeyIsOmittedWithoutAKey(t *testing.T) {
	s := pinServer(t, "https://drive.example.com")
	pub := s.config.Security.IdentityKey.Public().(ed25519.PublicKey)
	if got := s.withIdentityKey(map[string]string{})["identity_key"]; got != base64.StdEncoding.EncodeToString(pub) {
		t.Fatalf("identity_key %q", got)
	}
	s.config.Security.IdentityKey = nil
	if _, ok := s.withIdentityKey(map[string]string{})["identity_key"]; ok {
		t.Fatal("no key must mean no field, not an empty one")
	}
}

// certFile writes a PEM certificate for host, valid from notBefore to notAfter, and returns its
// path and pin.
func certFile(t *testing.T, host string, notBefore, notAfter time.Time) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{host}, NotBefore: notBefore, NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	path := filepath.Join(t.TempDir(), "fullchain.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, spkiPin(leaf)
}

func TestTLSPinSignsOnlyTheOperatorsCertificate(t *testing.T) {
	// The probe path is intercepted: it presents a trusted, hostname-valid leaf of its own.
	base, intercepted, roots := tlsOrigin(t)
	s := pinServer(t, base)
	s.pinProbeRoots = roots
	s.config.Security.TLSPinProbe = true
	file, want := certFile(t, "localhost", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	s.config.Security.TLSCertFile = file
	rec := getPin(s, "192.0.2.5")
	var body struct {
		Host, Pin, Signature string
		IssuedAt             int64 `json:"issued_at"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	if body.Pin == intercepted || body.Pin != want {
		t.Fatalf("signed %q; the operator's certificate is %q, the intercepted one %q", body.Pin, want, intercepted)
	}
	sig, _ := base64.StdEncoding.DecodeString(body.Signature)
	pub, _ := base64.StdEncoding.DecodeString(s.identityKey())
	if !ed25519.Verify(pub, tlsPinStatement(body.Host, body.Pin, body.IssuedAt), sig) {
		t.Fatal("signature does not verify")
	}

	// A file that does not name the host, is outside its validity, or is gone is never signed.
	other, _ := certFile(t, "elsewhere.example", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	expired, _ := certFile(t, "localhost", time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour))
	for i, f := range []string{other, expired, filepath.Join(t.TempDir(), "missing.pem")} {
		s.config.Security.TLSCertFile = f
		if rec := getPin(s, fmt.Sprintf("192.0.2.%d", 10+i)); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("file %d: %d %s", i, rec.Code, rec.Body)
		}
	}
}

func TestTLSPinSignsNothingWithoutACertificateSource(t *testing.T) {
	// The probe would present a trusted, hostname-valid leaf, but nobody chose to trust a probe.
	base, _, roots := tlsOrigin(t)
	s := pinServer(t, base)
	s.pinProbeRoots = roots
	rec := getPin(s, "192.0.2.20")
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "signature") {
		t.Fatalf("signed without KYDRIVE_TLS_CERT_FILE or KYDRIVE_TLS_PIN_PROBE: %d %s", rec.Code, rec.Body)
	}
}
