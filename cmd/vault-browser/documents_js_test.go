package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/vault"
	"github.com/google/uuid"
)

func TestSealRevisionSizeBoundary(t *testing.T) {
	identity, err := age.GenerateHybridIdentity()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := hybridsig.Generate()
	if err != nil {
		t.Fatal(err)
	}
	b := browser{
		account: account{User: "usr_owner"}, signing: signer,
		active: &openVault{
			identity: identity, checkpoint: vault.Checkpoint{IssuedMS: time.Now().UnixMilli()},
			verifiedHistory: verifiedHistory{
				head: vault.Head{Log: strings.Repeat("a", 64), Sequence: 1, Hash: strings.Repeat("b", 64)},
				records: []vault.Record{{
					Org: uuid.NewString(), Vault: uuid.NewString(),
					State: vault.State{Generation: 1, Members: []vault.Member{{User: "usr_owner", Role: vault.Manager}}},
				}},
			},
		},
	}
	const plaintextLimit = vault.MaxDocumentBytes - (16 << 10)
	file := uuid.NewString()
	for _, size := range []int{plaintextLimit, plaintextLimit + 1, vault.MaxDocumentBytes - 8192} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			sealed, err := b.sealRevision(revisionInput{File: file, Number: 1, Body: make([]byte, size)})
			if size > plaintextLimit {
				if err == nil || len(sealed.Body) != 0 || len(sealed.Manifest.Signature) != 0 {
					t.Fatal("an oversized plaintext produced a signed or encrypted result")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(sealed.Body)) != sealed.Manifest.Size || sealed.Manifest.Size > vault.MaxDocumentBytes {
				t.Fatalf("ciphertext length %d, manifest length %d, wire limit %d",
					len(sealed.Body), sealed.Manifest.Size, vault.MaxDocumentBytes)
			}
			if err := sealed.Manifest.Verify(signer.Public()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
