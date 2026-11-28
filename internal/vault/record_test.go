package vault

import (
	"testing"

	"filippo.io/age"
)

// Every vault wrap must stay post-quantum hybrid: a classical X25519 recipient is refused.
func TestValidRecipientRequiresHybrid(t *testing.T) {
	hybrid, err := age.GenerateHybridIdentity()
	if err != nil {
		t.Fatal(err)
	}
	classical, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidRecipient(hybrid.Recipient().String()) {
		t.Fatal("hybrid mlkem768x25519 recipient refused")
	}
	if ValidRecipient(classical.Recipient().String()) {
		t.Fatal("classical X25519 recipient accepted")
	}
}
