package vaultclient

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"

	"filippo.io/age"
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

// RecoveryKey is the user's offline recovery key, shown once as text and QR code. Its text is
// an age ML-KEM-768 + X25519 identity (bech32, with a checksum); the hybrid signing key that
// authorises replacements is derived from that text, so the text is the whole key.
type RecoveryKey struct {
	member  *age.HybridIdentity
	signing *hybridsig.PrivateKey
}

const recoverySigningInfo = "kydrive-vault-recovery-signing-v1"

func GenerateRecoveryKey() (*RecoveryKey, error) {
	member, err := age.GenerateHybridIdentity()
	if err != nil {
		return nil, err
	}
	return recoveryFrom(member)
}

// ParseRecoveryKey accepts the shown text with any spacing, line breaks or case.
func ParseRecoveryKey(text string) (*RecoveryKey, error) {
	member, err := age.ParseHybridIdentity(strings.ToUpper(strings.Join(strings.Fields(text), "")))
	if err != nil {
		return nil, errors.New("That is not a KyDrive recovery key")
	}
	return recoveryFrom(member)
}

func recoveryFrom(member *age.HybridIdentity) (*RecoveryKey, error) {
	seeds, err := hkdf.Key(sha256.New, []byte(member.String()), nil, recoverySigningInfo, 2*hybridsig.SeedBytes)
	if err != nil {
		return nil, err
	}
	defer clear(seeds)
	signing, err := hybridsig.FromSeeds(seeds[:hybridsig.SeedBytes], seeds[hybridsig.SeedBytes:])
	if err != nil {
		return nil, err
	}
	return &RecoveryKey{member, signing}, nil
}

// String is the text to show once. Never send it to a server.
func (k *RecoveryKey) String() string { return k.member.String() }

func (k *RecoveryKey) PublicKey() MemberKey {
	return MemberKey{k.member.Recipient().String(), k.signing.Public().Bytes()}
}

// RecoveryUpload is the body of POST /api/vault-recovery and the reply of GET.
type RecoveryUpload struct {
	Record RecoveryRecord `json:"record"`
	Copy   []byte         `json:"copy"`
}

type sealedIdentity struct {
	Recipient string `json:"recipient"`
	ML        []byte `json:"ml"`
	Ed        []byte `json:"ed"`
}

func recoveryUpload(record RecoveryRecord, identity *Identity, recovery, authority *RecoveryKey) (RecoveryUpload, error) {
	recipient, ml, ed := identity.Secrets()
	secrets, err := json.Marshal(sealedIdentity{recipient, ml, ed})
	clear(ml)
	clear(ed)
	if err != nil {
		return RecoveryUpload{}, err
	}
	sealed, err := encrypt(secrets, recovery.member.Recipient())
	clear(secrets)
	if err != nil {
		return RecoveryUpload{}, err
	}
	record.Key, record.Recovery, record.Copy = vault.Key(identity.PublicKey()), vault.Key(recovery.PublicKey()), vault.Digest(sealed)
	var by *hybridsig.PrivateKey
	if authority != nil {
		by = authority.signing
	}
	if record, err = record.Sign(identity.signing, recovery.signing, by); err != nil {
		return RecoveryUpload{}, err
	}
	return RecoveryUpload{record, sealed}, nil
}

func next(prev RecoveryRecord, change string) RecoveryRecord {
	return RecoveryRecord{Org: prev.Org, User: prev.User, Sequence: prev.Sequence + 1, Previous: prev.Hash(), Change: change}
}

// RegisterRecovery seals the enrolled identity to a new recovery key.
func RegisterRecovery(org, user string, identity *Identity, recovery *RecoveryKey) (RecoveryUpload, error) {
	return recoveryUpload(RecoveryRecord{Org: org, User: user, Sequence: 1, Change: "register"}, identity, recovery, nil)
}

// ReplaceIdentity moves the account to a new identity, authorised by the recovery key,
// which keeps holding the new copy. prev is the account's newest verified record.
func ReplaceIdentity(prev RecoveryRecord, recovery *RecoveryKey, replacement *Identity) (RecoveryUpload, error) {
	return recoveryUpload(next(prev, "replace"), replacement, recovery, recovery)
}

// RotateRecovery re-seals the identity to a new recovery key, authorised by the old one.
func RotateRecovery(prev RecoveryRecord, identity *Identity, old, replacement *RecoveryKey) (RecoveryUpload, error) {
	return recoveryUpload(next(prev, "rotate"), identity, replacement, old)
}

// OpenRecovery opens the server's copy with the recovery key and checks it is the identity
// the signed record names.
func OpenRecovery(in RecoveryUpload, recovery *RecoveryKey) (*Identity, error) {
	refusal := errors.New("The recovery copy does not match this recovery key")
	if !vault.ValidRecoveryCopy(in.Copy, in.Record.Copy) || in.Record.Recovery.Fingerprint() != recovery.PublicKey().fingerprint() {
		return nil, refusal
	}
	secrets, err := decrypt(in.Copy, recovery.member, vault.MaxRecoveryCopy)
	if err != nil {
		return nil, refusal
	}
	defer clear(secrets)
	var s sealedIdentity
	if json.Unmarshal(secrets, &s) != nil {
		return nil, refusal
	}
	identity, err := RestoreIdentity(s.Recipient, s.ML, s.Ed)
	clear(s.ML)
	clear(s.Ed)
	if err != nil || identity.PublicKey().Fingerprint() != in.Record.Key.Fingerprint() {
		return nil, refusal
	}
	return identity, nil
}

func (k MemberKey) fingerprint() string { return vault.Key(k).Fingerprint() }

// VerifyRecoveryChain checks an account's records from registration and returns the key the
// chain ends at. The pin, a fingerprint the caller already trusts for that account, must be one
// of the chain's keys, so only that key's recovery key can have moved it.
func VerifyRecoveryChain(records []RecoveryRecord, pin MemberPin) (MemberKey, error) {
	refusal := errors.New("The account's key replacement did not verify")
	var before *RecoveryRecord
	pinned := false
	for i := range records {
		if records[i].User != pin.User || vault.VerifyRecovery(before, records[i]) != nil {
			return MemberKey{}, refusal
		}
		pinned = pinned || records[i].Key.Fingerprint() == pin.Fingerprint
		before = &records[i]
	}
	if before == nil || !pinned {
		return MemberKey{}, refusal
	}
	return MemberKey(before.Key), nil
}

// Rekey re-adds a member under a replacement key the caller verified with
// VerifyRecoveryChain, rotating the vault key. Later opens pass that chain in
// HistoryInput.Recovery. For the caller's own replacement, pass the new
// identity: it signs, and the session must have opened the vault with the replaced one.
func (b *Session) Rekey(target MemberPin, key MemberKey, replacement *Identity) (vault.Record, error) {
	if b.active == nil || key.fingerprint() != target.Fingerprint {
		return vault.Record{}, errors.New("Open a vault and choose a verified replacement key")
	}
	signer := b.signing
	if target.User == b.user {
		if replacement == nil || replacement.PublicKey().Fingerprint() != target.Fingerprint {
			return vault.Record{}, errors.New("Sign your own re-key with the replacement identity")
		}
		signer = replacement.signing
	}
	before := b.active.records[len(b.active.records)-1]
	old, found := before.State.Member(target.User)
	if !found || old.Fingerprint == target.Fingerprint {
		return vault.Record{}, errors.New("Choose a current vault member with a new key")
	}
	record := before
	record.Sequence, record.Previous, record.Signer, record.Change, record.Target = before.Sequence+1, b.active.head.Hash, b.user, "rekey", target.User
	record.State.Members = slices.Clone(before.State.Members)
	for i := range record.State.Members {
		if record.State.Members[i].User == target.User {
			record.State.Members[i].Fingerprint = target.Fingerprint
		}
	}
	keys := b.active.keys
	b.active.keys = maps.Clone(keys)
	b.active.keys[target.User] = key
	err := b.rotate(&record.State)
	b.active.keys = keys
	if err != nil {
		return vault.Record{}, err
	}
	signed, err := record.Sign(signer)
	if err != nil {
		return vault.Record{}, err
	}
	if err := vault.VerifyRecord(&before, signed, signer.Public()); err != nil {
		return vault.Record{}, errors.New("The proposed re-key did not verify")
	}
	return signed, nil
}
