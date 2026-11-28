package vaultclient

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRecoveryKeyText(t *testing.T) {
	key, err := GenerateRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	text := key.String()
	typed := strings.ToLower(text[:20]) + "\n  " + strings.ToLower(text[20:40]) + " " + strings.ToLower(text[40:])
	parsed, err := ParseRecoveryKey(typed)
	if err != nil || parsed.PublicKey().fingerprint() != key.PublicKey().fingerprint() {
		t.Fatalf("typed recovery key did not parse to the same key: %v", err)
	}
	swap := "Q"
	if strings.HasSuffix(text, swap) {
		swap = "P"
	}
	broken := text[:len(text)-1] + swap // bech32's checksum catches one wrong character
	if _, err := ParseRecoveryKey(broken); err == nil {
		t.Fatal("a mistyped recovery key parsed")
	}
}

func TestRecoveryChainRefusals(t *testing.T) {
	org, user := uuid.NewString(), "usr_chain"
	first, _ := GenerateIdentity()
	second, _ := GenerateIdentity()
	recovery, _ := GenerateRecoveryKey()
	other, _ := GenerateRecoveryKey()
	register, err := RegisterRecovery(org, user, first, recovery)
	if err != nil {
		t.Fatal(err)
	}
	replace, err := ReplaceIdentity(register.Record, recovery, second)
	if err != nil {
		t.Fatal(err)
	}
	from := MemberPin{User: user, Fingerprint: first.PublicKey().Fingerprint()}
	if got, err := VerifyRecoveryChain([]RecoveryRecord{register.Record, replace.Record}, from); err != nil || got.Fingerprint() != second.PublicKey().Fingerprint() {
		t.Fatalf("valid chain: %v", err)
	}
	if opened, err := OpenRecovery(replace, recovery); err != nil || opened.PublicKey().Fingerprint() != second.PublicKey().Fingerprint() {
		t.Fatalf("replacement copy: %v", err)
	}
	forged, _ := ReplaceIdentity(register.Record, other, second)
	tampered := replace.Record
	tampered.Key = register.Record.Key
	for name, chain := range map[string][]RecoveryRecord{
		"another recovery key": {register.Record, forged.Record},
		"tampered":             {register.Record, tampered},
		"no registration":      {replace.Record},
		"reordered":            {replace.Record, register.Record},
	} {
		if _, err := VerifyRecoveryChain(chain, from); err == nil {
			t.Errorf("%s: chain verified", name)
		}
	}
	if _, err := VerifyRecoveryChain([]RecoveryRecord{register.Record, replace.Record}, MemberPin{User: "usr_other", Fingerprint: from.Fingerprint}); err == nil {
		t.Error("chain verified for another account")
	}
}
