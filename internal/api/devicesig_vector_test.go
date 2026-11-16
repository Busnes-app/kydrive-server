package api

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Busnes-app/kydrive-server/internal/config"
	"github.com/Busnes-app/kydrive-server/internal/drive"
)

// The golden vector the clients verify too (KyDrive-for-Android keeps a copy). Its signature
// was made once and checked with `openssl dgst -sha256 -verify`; ECDSA is randomized, so tests
// verify it and never compare a fresh one.
func TestDeviceSigningGoldenVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/device-signing-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		AppURL        string            `json:"app_url"`
		Origin        string            `json:"origin"`
		Method        string            `json:"method"`
		RequestURI    string            `json:"request_uri"`
		Date          string            `json:"date"`
		Nonce         string            `json:"nonce"`
		Headers       map[string]string `json:"headers"`
		SignedHeaders string            `json:"signed_headers"`
		Body          string            `json:"body"`
		Message       string            `json:"message"`
		SPKI          string            `json:"public_key_spki_b64"`
		Signature     string            `json:"signature_b64"`
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if got := canonicalOrigin(v.AppURL); got != v.Origin {
		t.Fatalf("origin %q, want %q", got, v.Origin)
	}
	r := httptest.NewRequest(v.Method, v.RequestURI, strings.NewReader(v.Body))
	for name, value := range v.Headers {
		r.Header.Set(name, value)
	}
	wantHeaders := hexSHA(v.SignedHeaders)
	if got, ok := headerDigest(r.Header); !ok || got != wantHeaders {
		t.Fatalf("header digest %s, want %s", got, wantHeaders)
	}
	msg, ok := deviceMessage(v.Origin, v.Method, v.RequestURI, v.Date, v.Nonce, wantHeaders, hexSHA(v.Body))
	if !ok || msg != v.Message {
		t.Fatalf("message\n%q\nwant\n%q", msg, v.Message)
	}

	// The whole check, as a request arrives.
	der, _ := base64.StdEncoding.DecodeString(v.SPKI)
	key, err := drive.ParseDeviceKey(der)
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := strconv.ParseInt(v.Date, 10, 64)
	cfg := &config.Config{}
	cfg.Server.AppURL = v.AppURL
	s := &Server{config: cfg, now: func() time.Time { return time.UnixMilli(ms).Add(time.Minute) }}
	r.Header.Set("KyDrive-Date", v.Date)
	r.Header.Set("KyDrive-Nonce", v.Nonce)
	r.Header.Set("KyDrive-Content-SHA256", hexSHA(v.Body))
	r.Header.Set("KyDrive-Signature", v.Signature)
	if err = s.verifyDeviceRequest(r, "dev1", key); err != nil {
		t.Fatalf("vector refused: %v", err)
	}
	if err = s.verifyDeviceRequest(r, "dev1", key); err != errDeviceSignature {
		t.Fatalf("replayed vector: %v", err)
	}
	// Another key of the right shape refuses it.
	other, _ := ecdsa.GenerateKey(key.Curve, strings.NewReader(strings.Repeat("x", 128)))
	r.Header.Set("KyDrive-Nonce", "BBECAwQFBgcICQoLDA0ODw")
	if err = s.verifyDeviceRequest(r, "dev2", &other.PublicKey); err != errDeviceSignature {
		t.Fatalf("other key: %v", err)
	}
}

func hexSHA(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
