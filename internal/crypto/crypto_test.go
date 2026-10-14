package crypto_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/Busnes-app/kydrive-server/internal/crypto"
)

func TestAESGCMEncryption(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plaintext := []byte("totp secret")

	enc, err := crypto.EncryptAESGCM(plaintext, key)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	dec, err := crypto.DecryptAESGCM(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(dec, plaintext) {
		t.Fatalf("round trip: got %q", dec)
	}

	other := bytes.Repeat([]byte{0x43}, 32)
	if _, err := crypto.DecryptAESGCM(enc, other); err == nil {
		t.Fatal("wrong key decrypted")
	}
}

func TestAESGCMBindsAAD(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	enc, err := crypto.EncryptAESGCMAAD([]byte("name"), key, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if dec, err := crypto.DecryptAESGCMAAD(enc, key, []byte("row-1")); err != nil || string(dec) != "name" {
		t.Fatalf("round trip: %q %v", dec, err)
	}
	for _, aad := range [][]byte{nil, []byte("row-2")} {
		if _, err := crypto.DecryptAESGCMAAD(enc, key, aad); err == nil {
			t.Fatalf("decrypted under aad %q", aad)
		}
	}
}

func TestAESGCMRefusesShortKey(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		_, err := crypto.EncryptAESGCM([]byte("x"), make([]byte, n))
		if !errors.Is(err, crypto.ErrKeyLength) {
			t.Errorf("len %d: got %v, want ErrKeyLength", n, err)
		}
	}
}

func TestHMACSHA256(t *testing.T) {
	secret := "shared-sync-webhook-secret"
	payload := []byte(`{"event":"user.updated","username":"alice"}`)

	sig := crypto.ComputeHMACSHA256(payload, secret)
	if !crypto.VerifyHMACSHA256(payload, secret, sig) {
		t.Errorf("expected HMAC verification to succeed")
	}

	if crypto.VerifyHMACSHA256(payload, "wrong-secret", sig) {
		t.Errorf("expected HMAC verification to fail with wrong secret")
	}
}

func TestPKCEGeneration(t *testing.T) {
	verifier, challenge, err := crypto.GeneratePKCE()
	if err != nil {
		t.Fatalf("failed to generate PKCE: %v", err)
	}
	if len(verifier) == 0 || len(challenge) == 0 {
		t.Errorf("expected non-empty verifier and challenge")
	}
	if verifier == challenge {
		t.Errorf("verifier and challenge should not be identical")
	}
}

func TestDeriveKey(t *testing.T) {
	master := bytes.Repeat([]byte{0x01}, 32)
	derive := func(label string) []byte {
		k, err := crypto.DeriveKey(master, label)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	a, b := derive("app:setting:one"), derive("app:setting:two")
	if len(a) != 32 || bytes.Equal(a, b) || !bytes.Equal(a, derive("app:setting:one")) {
		t.Fatal("derived keys must be 32 bytes, distinct per label and stable")
	}
	// Restic repositories and TOTP rows are sealed under these; the output is a stored format.
	if got := hex.EncodeToString(derive("kydrive:restic")); got != "8f9675f4176dc6ba3055bf1746d3dca7f04341318b53a7694523d88597299edd" {
		t.Fatalf("kydrive:restic derivation changed: %s", got)
	}
}
