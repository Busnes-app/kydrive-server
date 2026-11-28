// Package vaultclient verifies vault membership and keeps revision and editor cryptography on the client.
package vaultclient

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"time"

	"filippo.io/age"
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/vault"
	"github.com/google/uuid"
)

// Identity is an account's vault key pair. The caller owns durable secret storage.
type Identity struct {
	member  *age.HybridIdentity
	signing *hybridsig.PrivateKey
}

// MemberKey contains only the public account keys.
type MemberKey struct {
	Recipient string `json:"recipient"`
	Signing   []byte `json:"signing"`
}

func GenerateIdentity() (*Identity, error) {
	member, err := age.GenerateHybridIdentity()
	if err != nil {
		return nil, err
	}
	signing, err := hybridsig.Generate()
	if err != nil {
		return nil, err
	}
	return &Identity{member, signing}, nil
}

// RestoreIdentity opens credentials read from the client's own keystore.
func RestoreIdentity(recipient string, ml, ed []byte) (*Identity, error) {
	member, err := age.ParseHybridIdentity(recipient)
	if err != nil {
		return nil, errors.New("invalid stored vault identity")
	}
	signing, err := hybridsig.FromSeeds(ml, ed)
	if err != nil {
		return nil, errors.New("invalid stored vault signing key")
	}
	return &Identity{member, signing}, nil
}

// Secrets returns private key material for protected, device-local storage only.
func (i *Identity) Secrets() (recipient string, ml, ed []byte) {
	ml, ed = i.signing.Seeds()
	return i.member.String(), ml, ed
}
func (i *Identity) PublicKey() MemberKey {
	return MemberKey{i.member.Recipient().String(), i.signing.Public().Bytes()}
}

// Session holds one verified open vault. Its caller serializes operations.
type Session struct {
	user, org string
	member    *age.HybridIdentity
	signing   *hybridsig.PrivateKey
	active    *openVault
}

func NewSession(user, org string, identity *Identity) (*Session, error) {
	if !vault.ValidUserID(user) || !vault.ID(org) || identity == nil || identity.member == nil || identity.signing == nil {
		return nil, errors.New("invalid vault account or identity")
	}
	return &Session{user: user, org: org, member: identity.member, signing: identity.signing}, nil
}

func (b *Session) publicKey() MemberKey {
	return MemberKey{b.member.Recipient().String(), b.signing.Public().Bytes()}
}

// VerifyHistory identifies the exact head the caller must obtain from its pinned witness.
func (b *Session) VerifyHistory(in HistoryInput) (Head, error) {
	checked, err := b.verifyHistory(in)
	return checked.head, err
}

func (b *Session) RefreshCheckpoint(checkpoint Checkpoint) error {
	if b.active == nil || vault.VerifyCheckpoint(b.active.witness, checkpoint, b.active.head, time.Now()) != nil {
		return errors.New("The vault witness checkpoint did not verify")
	}
	b.active.checkpoint = checkpoint
	return nil
}

func (b *Session) Close() { b.active = nil }

func (b *Session) CreateVault() (vault.Record, error) {
	if b.member == nil {
		return vault.Record{}, errors.New("Unlock the vault key first")
	}
	key, err := age.GenerateHybridIdentity()
	if err != nil {
		return vault.Record{}, err
	}
	wrapped, err := encrypt([]byte(key.String()), b.member.Recipient())
	if err != nil {
		return vault.Record{}, err
	}
	indexKey := make([]byte, 32)
	if _, err := rand.Read(indexKey); err != nil {
		return vault.Record{}, err
	}
	index, err := encrypt(indexKey, key.Recipient())
	if err != nil {
		return vault.Record{}, err
	}
	record := vault.Record{
		Org: b.org, Vault: uuid.NewString(), Sequence: 1, Signer: b.user, Change: "create",
		State: vault.State{Generation: 1, Recipient: key.Recipient().String(), NameIndex: index,
			Members: []vault.Member{{User: b.user, Fingerprint: b.publicKey().Fingerprint(), Role: vault.Manager, Wrap: wrapped}}},
	}
	return record.Sign(b.signing)
}

func encrypt(data []byte, recipient age.Recipient) ([]byte, error) {
	var buffer bytes.Buffer
	w, err := age.Encrypt(&buffer, recipient)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func decrypt(data []byte, identity age.Identity, limit int64) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(data), identity)
	if err != nil {
		return nil, err
	}
	opened, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(opened)) > limit {
		return nil, errors.New("Invalid encrypted vault data")
	}
	return opened, nil
}
