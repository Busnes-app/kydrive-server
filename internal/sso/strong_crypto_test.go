package sso_test

import (
	"context"
	gocrypto "crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/fips140"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/crypto"
	"github.com/Busnes-app/kydrive-server/internal/sso"
	"github.com/coreos/go-oidc/v3/oidc"
)

func TestWebhookKeyFloorBeforeMutation(t *testing.T) {
	_, st := webhookClient(t)
	secret := strings.Repeat("s", crypto.MinHMACKeyBytes-1)
	client := sso.NewKySignOnClient(config.SSOConfig{KySignOnHMACSecret: secret}, st)
	body, _ := json.Marshal(map[string]any{"event": "user.created", "id": "short-key", "username": "short-key-user", "timestamp": time.Now().Unix()})
	if err := client.HandleSyncWebhook(context.Background(), body, crypto.ComputeHMACSHA256(body, secret)); !errors.Is(err, sso.ErrWebhookUnauthorized) {
		t.Fatalf("short-key webhook: %v", err)
	}
	if _, err := st.Users().GetUserBySSO(context.Background(), "kysignon", "short-key"); err == nil {
		t.Fatal("short-key webhook created a user")
	}
}

func discovery(issuer string) map[string]any {
	return map[string]any{
		"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
		"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}
}

// The real verifier must refuse sub-2048 RSA keys, including JWK member casing
// that go-jose ignores; transport rejection alone cannot prove that alignment.
func TestOIDCRSAKeyFloorMatchesVerifier(t *testing.T) {
	for _, bits := range []int{1024, 2047, 2048} {
		key, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			t.Fatal(err)
		}
		for _, variant := range []struct{ name, before, after, envelope string }{
			{name: "canonical", envelope: "keys"},
			{name: "uppercase type", after: `,"KTY":"EC"`, envelope: "keys"},
			{name: "uppercase modulus", after: `,"N":"` + base64.RawURLEncoding.EncodeToString(new(big.Int).Lsh(big.NewInt(1), 2047).Bytes()) + `"`, envelope: "keys"},
			{name: "aliases before canonical", before: `"KTY":"EC","N":"AQ",`, envelope: "keys"},
			{name: "uppercase envelope", envelope: "KEYS"},
		} {
			t.Run(fmt.Sprintf("RSA%d/%s", bits, variant.name), func(t *testing.T) {
				var issuer, token string
				var fetches atomic.Int32
				idp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/.well-known/openid-configuration":
						_ = json.NewEncoder(w).Encode(discovery(issuer))
					case "/keys":
						fetches.Add(1)
						_, _ = fmt.Fprintf(w, `{"%s":[{%s"kty":"RSA","kid":"fixture","alg":"RS256","e":"AQAB","n":"%s"%s}]}`,
							variant.envelope, variant.before, base64.RawURLEncoding.EncodeToString(key.N.Bytes()), variant.after)
					case "/token":
						_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fixture", "token_type": "Bearer", "id_token": token})
					default:
						http.NotFound(w, r)
					}
				}))
				defer idp.Close()
				issuer = idp.URL
				token = signedIDToken(t, key, issuer)
				ctx := oidc.ClientContext(context.Background(), idp.Client())
				client := sso.NewKySignOnClient(config.SSOConfig{KySignOnIssuer: issuer, KySignOnClientID: "client"}, nil)
				exchange := func(nonce string) (*sso.IdentityClaims, error) {
					return client.ExchangeCode(ctx, "code", "verifier", "https://app.example/callback", nonce)
				}
				claims, err := exchange("nonce")
				if bits < 2048 {
					if !errors.Is(err, sso.ErrInvalidIDToken) || claims != nil {
						t.Fatalf("accepted RSA-%d token: claims=%+v err=%v", bits, claims, err)
					}
					return
				}
				if err != nil || claims.Subject != "subject" {
					t.Fatalf("strong token: claims=%+v err=%v", claims, err)
				}
				if _, err := exchange("nonce"); err != nil || fetches.Load() != 1 {
					t.Fatalf("JWKS cache not retained: err=%v fetches=%d", err, fetches.Load())
				}
				if claims, err := exchange("wrong"); !errors.Is(err, sso.ErrInvalidIDToken) || claims != nil {
					t.Fatal("accepted the wrong nonce")
				}
			})
		}
	}
}

func signedIDToken(t *testing.T, key *rsa.PrivateKey, issuer string) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "fixture"})
	claims, _ := json.Marshal(map[string]any{
		"iss": issuer, "aud": "client", "sub": "subject", "nonce": "nonce",
		"exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
	})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, gocrypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestOIDCStrongModeRequiresDirectHTTPS(t *testing.T) {
	for _, field := range []string{"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri", "redirect"} {
		t.Run(field, func(t *testing.T) {
			var issuer string
			var calls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if field == "redirect" && r.URL.Path != "/moved" {
					http.Redirect(w, r, "/moved", http.StatusFound)
					return
				}
				metadata := discovery(issuer)
				if field != "issuer" && field != "redirect" {
					metadata[field] = "http://id.example.invalid/endpoint"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(metadata)
			})
			makeServer := httptest.NewTLSServer
			if field == "issuer" {
				makeServer = httptest.NewServer
			}
			idp := makeServer(handler)
			defer idp.Close()
			issuer = idp.URL
			client := sso.NewKySignOnClient(config.SSOConfig{KySignOnIssuer: issuer, KySignOnClientID: "client"}, nil)
			_, err := client.BuildAuthURL(oidc.ClientContext(context.Background(), idp.Client()), "https://app.example/callback", "state", "verifier", "nonce")
			if (err != nil) != fips140.Enabled() {
				t.Fatalf("configured=%v, error=%v", fips140.Enabled(), err)
			}
			if field == "redirect" && fips140.Enabled() && calls.Load() != 1 {
				t.Fatalf("followed a discovery redirect in the strong profile: %d requests", calls.Load())
			}
			if field == "issuer" && fips140.Enabled() && calls.Load() != 0 {
				t.Fatal("contacted a plaintext issuer in the strong profile")
			}
		})
	}
}

// The peer serves a strong P-256 leaf; a control client trusting the leaf's
// chain through httptest succeeds. The product's OIDC discovery trusts only the
// RSA root, so a refusal proves client enforcement over the whole verified chain,
// including an explicitly trusted anchor.
func TestOIDCTLSChecksTrustedRootKeyFloor(t *testing.T) {
	for _, bits := range []int{1024, 2047, 2048} {
		t.Run(fmt.Sprint(bits), func(t *testing.T) {
			rootKey, err := rsa.GenerateKey(rand.Reader, bits)
			if err != nil {
				t.Fatal(err)
			}
			leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			root := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
			rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
			if err != nil {
				t.Fatal(err)
			}
			if root, err = x509.ParseCertificate(rootDER); err != nil {
				t.Fatal(err)
			}
			leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, rootKey)
			if err != nil {
				t.Fatal(err)
			}
			var issuer string
			var calls atomic.Int32
			idp := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(discovery(issuer))
			}))
			idp.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, rootDER}, PrivateKey: leafKey}}}
			idp.StartTLS()
			defer idp.Close()
			issuer = idp.URL
			leafCert, _ := x509.ParseCertificate(leafDER)
			leafOnly := x509.NewCertPool()
			leafOnly.AddCert(leafCert)
			control := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: leafOnly}}}
			resp, err := control.Get(idp.URL)
			if err != nil {
				t.Fatalf("strong-leaf control: %v", err)
			}
			resp.Body.Close()

			pool := x509.NewCertPool()
			pool.AddCert(root)
			trusting := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
			before := calls.Load()
			client := sso.NewKySignOnClient(config.SSOConfig{KySignOnIssuer: issuer, KySignOnClientID: "client"}, nil)
			_, err = client.BuildAuthURL(oidc.ClientContext(context.Background(), trusting), "https://app.example/callback", "state", "verifier", "nonce")
			refuse := bits < 2048 && fips140.Enabled()
			if (err != nil) != refuse {
				t.Fatalf("RSA-%d root, configured=%v: %v", bits, fips140.Enabled(), err)
			}
			if refuse && calls.Load() != before {
				t.Fatal("HTTP request crossed weak-root TLS")
			}
		})
	}
}
