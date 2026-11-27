// Signed TLS pin rollover for linked devices.
//
// A device pins the TLS leaf served at KY_APP_URL. Certificates renew about every 30 days, so
// instead of a re-link the server holds a long-term Ed25519 identity key whose public half the
// device learned over the pinned channel (POST /api/devices/token, GET /api/drive/device). After
// a renewal the device fetches GET /api/devices/tls-pin over system trust, with no credential,
// and repins only if this key signed the exact leaf it saw for this host. An interceptor with a
// locally trusted CA can present its own leaf but cannot produce the signature.

package api

import (
	"context"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Busnes-app/ky-primitives/hybridsig"
)

// tlsPinContext domain-separates the signed bytes. Clients build the same string; change both
// together (testdata/tls-pin-v1.json).
const (
	tlsPinContext   = "kydrive-tls-pin/1"
	tlsPinContextV2 = "kydrive-tls-pin/2"
	// identityHybridHKDFInfo derives the deterministic ML-DSA-65 companion seed from the
	// persisted 32-byte Ed25519 identity key seed so existing backups cover both keys.
	identityHybridHKDFInfo = "kydrive-identity-mldsa65/1"
)

const (
	// pinProbeTimeout bounds the handshake with KY_APP_URL.
	pinProbeTimeout = 2 * time.Second
	// pinProbeFailureTTL caches only failures: a success is one fast handshake and must stay
	// live so a renewal is signed at once, while a failure (no hairpin NAT) hangs until the timeout.
	pinProbeFailureTTL = 5 * time.Minute
)

// tlsPinStatement is the exact v1 byte string signed and verified.
func tlsPinStatement(host, pin string, issuedAt int64) []byte {
	return []byte(tlsPinContext + "\n" + host + "\n" + pin + "\n" + strconv.FormatInt(issuedAt, 10))
}

// tlsPinStatementBody is the v2 message passed to hybridsig under tlsPinContextV2.
func tlsPinStatementBody(host, pin string, issuedAt int64) []byte {
	return []byte(host + "\n" + pin + "\n" + strconv.FormatInt(issuedAt, 10))
}

// identityHybridKey derives the deterministic ML-DSA-65+Ed25519 hybridsig private key paired
// with the server's persisted Ed25519 identity key. The Ed25519 half is identical.
func identityHybridKey(edKey ed25519.PrivateKey) (*hybridsig.PrivateKey, error) {
	if edKey == nil {
		return nil, errors.New("identity key unavailable")
	}
	edSeed := edKey.Seed()
	mldsaSeed, err := hkdf.Key(sha256.New, edSeed, nil, identityHybridHKDFInfo, hybridsig.SeedBytes)
	if err != nil {
		return nil, err
	}
	return hybridsig.FromSeeds(mldsaSeed, edSeed)
}

// spkiPin is sha256/ plus std base64 of the SHA-256 of the leaf's SubjectPublicKeyInfo DER,
// the format OkHttp's CertificatePinner uses.
func spkiPin(leaf *x509.Certificate) string {
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	return "sha256/" + base64.StdEncoding.EncodeToString(sum[:])
}

// identityKey is the std-base64 raw Ed25519 public key, or "" without one.
func (s *Server) identityKey() string {
	if s.config.Security.IdentityKey == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(s.config.Security.IdentityKey.Public().(ed25519.PublicKey))
}

// identityKeyHybrid is the std-base64 hybridsig (ML-DSA-65+Ed25519) public key, or "" without one.
func (s *Server) identityKeyHybrid() string {
	hk, err := identityHybridKey(s.config.Security.IdentityKey)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(hk.Public().Bytes())
}

// withIdentityKey adds identity_key and identity_key_hybrid to a device-facing reply when a key exists.
func (s *Server) withIdentityKey(resp map[string]string) map[string]string {
	if key := s.identityKey(); key != "" {
		resp["identity_key"] = key
	}
	if hkey := s.identityKeyHybrid(); hkey != "" {
		resp["identity_key_hybrid"] = hkey
	}
	return resp
}

// handleTLSPin signs the leaf KY_APP_URL serves right now. Public: the statement names the
// server's own certificate, never caller input, and is useful only to a holder of the public key.
// KyDrive never terminates TLS itself, so the probe is the only source of the pin.
//
// Older desktop and Android clients read this body with a 4 KiB cap, so the default response
// stays compact (v1 Ed25519 only). Callers that pass ?v=2 receive signature_v2 (hybridsig
// ML-DSA-65+Ed25519 under kydrive-tls-pin/2) and identity_key_hybrid alongside the v1 fields.
func (s *Server) handleTLSPin(w http.ResponseWriter, r *http.Request) {
	if !s.allowAttempt("tls-pin:"+s.requestIP(r), 10, time.Minute) {
		w.Header().Set("Retry-After", "60")
		s.writeError(w, http.StatusTooManyRequests, "Too many attempts")
		return
	}
	key := s.config.Security.IdentityKey
	base, err := url.Parse(s.config.Server.AppURL)
	if key == nil || err != nil || base.Scheme != "https" || base.Hostname() == "" {
		s.writeError(w, http.StatusServiceUnavailable, "TLS pin rollover is not available")
		return
	}
	host := strings.ToLower(base.Hostname())
	var pin string
	if file := s.config.Security.TLSCertFile; file != "" {
		// The operator's file is the authority: a probe would sign whatever certificate its
		// network path presents, an interceptor's included.
		var err error
		if pin, err = filePin(file, host, s.now()); err != nil {
			log.Printf("tls pin: KYDRIVE_TLS_CERT_FILE: %v", err)
		}
	} else if s.config.Security.TLSPinProbe {
		pin = s.probedPin(base)
	}
	if pin == "" {
		s.writeError(w, http.StatusServiceUnavailable, "The serving certificate could not be read")
		return
	}
	issuedAt := s.now().Unix()
	resp := map[string]any{
		"host":      host,
		"pin":       pin,
		"issued_at": issuedAt,
		"signature": base64.StdEncoding.EncodeToString(ed25519.Sign(key, tlsPinStatement(host, pin, issuedAt))),
	}
	if r.URL.Query().Get("v") == "2" {
		hk, err := identityHybridKey(key)
		if err != nil {
			s.writeError(w, http.StatusServiceUnavailable, "TLS pin rollover is not available")
			return
		}
		sigV2, err := hk.Sign(tlsPinContextV2, tlsPinStatementBody(host, pin, issuedAt))
		if err != nil {
			s.writeError(w, http.StatusServiceUnavailable, "TLS pin rollover is not available")
			return
		}
		resp["signature_v2"] = base64.StdEncoding.EncodeToString(sigV2)
		resp["identity_key_hybrid"] = base64.StdEncoding.EncodeToString(hk.Public().Bytes())
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// filePin is the pin of the leaf in the operator's PEM certificate file, re-read on each call so
// a renewal is signed at once. The leaf must name host and be within its validity at now: a stale
// or wrong file is refused rather than signed.
func filePin(path, host string, now time.Time) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for rest := raw; ; {
		var block *pem.Block
		if block, rest = pem.Decode(rest); block == nil {
			return "", errors.New("no certificate in the file")
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		leaf, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return "", err
		}
		if err := leaf.VerifyHostname(host); err != nil {
			return "", err
		}
		if now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
			return "", errors.New("the certificate is not currently valid")
		}
		return spkiPin(leaf), nil
	}
}

// probedPin handshakes with base and returns its leaf pin, or "" (failures cached).
func (s *Server) probedPin(base *url.URL) string {
	s.pinProbeMu.Lock()
	failed := time.Since(s.pinProbeFailedAt) < pinProbeFailureTTL
	s.pinProbeMu.Unlock()
	if failed {
		return ""
	}
	// Not the request's context: a caller hanging up must not be cached as a failure.
	ctx, cancel := context.WithTimeout(context.Background(), pinProbeTimeout)
	defer cancel()
	pin, err := probePin(ctx, base, s.pinProbeRoots)
	if err != nil {
		s.pinProbeMu.Lock()
		s.pinProbeFailedAt = time.Now()
		s.pinProbeMu.Unlock()
		log.Printf("tls pin: could not read the certificate served at %s: %v", base.Host, err)
		return ""
	}
	return pin
}

// probePin verifies the chain as any client would (roots nil means the system pool): signing a
// pin asserts "this key and no other", which unauthenticated bytes do not earn.
func probePin(ctx context.Context, base *url.URL, roots *x509.CertPool) (string, error) {
	port := base.Port()
	if port == "" {
		port = "443"
	}
	dialer := &tls.Dialer{Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: base.Hostname(), RootCAs: roots}}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(base.Hostname(), port))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	chain := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(chain) == 0 {
		return "", errors.New("peer presented no certificate")
	}
	return spkiPin(chain[0]), nil
}
