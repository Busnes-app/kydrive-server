package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Busnes-app/ky-primitives/pqtls"
)

// pqtlsLoopbackCert returns a P-256 self-signed loopback certificate (valid under both default
// and GODEBUG=fips140=on) along with its parsed leaf and trusting root pool.
func pqtlsLoopbackCert(t *testing.T) (tls.Certificate, *x509.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return cert, leaf, roots
}

// TestPQTLSHandshake verifies that KyDrive-server's TLS-pin probe and default outbound HTTPS
// client negotiate hybrid post-quantum key exchange (X25519MLKEM768) with a TLS 1.3 peer,
// both normally and under GODEBUG=fips140=on.
func TestPQTLSHandshake(t *testing.T) {
	cert, leaf, roots := pqtlsLoopbackCert(t)

	t.Run("tls_pin_probe", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		type handshakeResult struct {
			state tls.ConnectionState
			err   error
		}
		serverCh := make(chan handshakeResult, 1)
		serverCfg := &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{cert},
		}
		go func() {
			raw, err := ln.Accept()
			if err != nil {
				serverCh <- handshakeResult{err: err}
				return
			}
			defer raw.Close()
			tlsConn := tls.Server(raw, serverCfg)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				serverCh <- handshakeResult{err: err}
				return
			}
			serverCh <- handshakeResult{state: tlsConn.ConnectionState()}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		base := &url.URL{Scheme: "https", Host: ln.Addr().String()}
		pin, err := probePin(ctx, base, roots)
		if err != nil {
			t.Fatalf("probePin: %v", err)
		}
		if want := spkiPin(leaf); pin != want {
			t.Fatalf("probePin = %q, want %q", pin, want)
		}

		res := <-serverCh
		if res.err != nil {
			t.Fatalf("server handshake: %v", res.err)
		}
		pqtls.RequireHybrid(t, res.state)
	})

	t.Run("outbound_http_client", func(t *testing.T) {
		serverStateCh := make(chan tls.ConnectionState, 1)
		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS != nil {
				select {
				case serverStateCh <- *r.TLS:
				default:
				}
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		ts.TLS = &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{cert},
		}
		ts.StartTLS()
		defer ts.Close()

		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
		defer tr.CloseIdleConnections()

		client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
		resp, err := client.Get(ts.URL)
		if err != nil {
			t.Fatalf("GET %s: %v", ts.URL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status %d, want %d", resp.StatusCode, http.StatusNoContent)
		}
		if resp.TLS == nil {
			t.Fatal("missing client TLS connection state")
		}
		pqtls.RequireHybrid(t, *resp.TLS)
		pqtls.RequireHybrid(t, <-serverStateCh)
	})
}
