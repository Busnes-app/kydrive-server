package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"testing"
	"time"
)

func TestValidateTOTPKeyStrength(t *testing.T) {
	for _, size := range []int{1, 14, 15, 16, 20} {
		t.Run(fmt.Sprintf("%d_bytes", size), func(t *testing.T) {
			key := bytes.Repeat([]byte{0x42}, size)
			secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(key)
			code := codeForStrengthTest(key)

			_, valid := ValidateTOTP(secret, code)
			if want := size >= 16; valid != want {
				t.Fatalf("ValidateTOTP accepted=%v, want %v for %d decoded bytes", valid, want, size)
			}
		})
	}
}

// Authentication refuses a legacy short key without destroying the sealed record
// needed for deliberate administrator recovery and issuer re-enrollment.
func TestShortTOTPRecordRemainsReadable(t *testing.T) {
	deploymentKey := bytes.Repeat([]byte{0x27}, 32)
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("old-short-key"))
	sealed, err := SealTOTPSecret(deploymentKey, "existing-user", secret)
	if err != nil {
		t.Fatal(err)
	}
	opened, legacy, err := OpenTOTPSecret(deploymentKey, "existing-user", sealed)
	if err != nil || legacy || opened != secret {
		t.Fatalf("stored record changed: secret matches=%v legacy=%v err=%v", opened == secret, legacy, err)
	}
	if _, valid := ValidateTOTP(opened, codeForStrengthTest([]byte("old-short-key"))); valid {
		t.Fatal("short stored key authenticated")
	}
}

// Independent RFC 4226 truncation: the repaired library refuses to generate
// short-key codes, so it cannot construct the negative regression inputs.
func codeForStrengthTest(key []byte) string {
	counter := uint64(time.Now().Unix() / 30)
	var input [8]byte
	binary.BigEndian.PutUint64(input[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(input[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 15
	value := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1_000_000)
}
