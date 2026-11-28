package vault

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/Busnes-app/ky-primitives/hybridsig"
)

// Each signer role has its own domain, so one key's signature never stands in for another role.
const (
	recoveryUserDomain      = "kydrive-vault-recovery-user-v1"
	recoveryHolderDomain    = "kydrive-vault-recovery-key-v1"
	recoveryAuthorityDomain = "kydrive-vault-recovery-authority-v1"
	// MaxRecoveryCopy bounds the age file holding an account key's secrets.
	MaxRecoveryCopy    = 16 << 10
	recoveryCopyHeader = "age-encryption.org/v1\n-> mlkem768x25519 "
)

// Key is an account's or recovery key's public half. Its JSON is drive.VaultKey's.
type Key struct {
	Recipient string `json:"recipient"`
	Signing   []byte `json:"signing"`
}

func (k Key) Fingerprint() string {
	data, _ := json.Marshal(k)
	return Digest(data)
}

func (k Key) valid() bool {
	_, err := hybridsig.ParsePublicKey(k.Signing)
	return err == nil && ValidRecipient(k.Recipient)
}

// RecoveryRecord is one link of an account's recovery chain. Copy is the digest of the
// account key's secrets encrypted to Recovery; the server keeps only the newest copy.
//   - register (sequence 1): signed by Key and Recovery.
//   - replace: a new Key, authorised by the previous Recovery; Recovery may change.
//   - rotate: the same Key, a new Recovery, authorised by the previous Recovery.
type RecoveryRecord struct {
	Org       string `json:"org"`
	User      string `json:"user"`
	Sequence  int64  `json:"sequence"`
	Previous  string `json:"previous"`
	Change    string `json:"change"`
	Key       Key    `json:"key"`
	Recovery  Key    `json:"recovery"`
	Copy      string `json:"copy"`
	UserSig   []byte `json:"user_signature"`
	HolderSig []byte `json:"recovery_signature"`
	Authority []byte `json:"authority_signature,omitempty"`
}

func (r RecoveryRecord) message() []byte {
	r.UserSig, r.HolderSig, r.Authority = nil, nil, nil
	data, _ := json.Marshal(r)
	return data
}

// Hash commits to the whole signed record; the next record names it as Previous.
func (r RecoveryRecord) Hash() string {
	data, _ := json.Marshal(r)
	return Digest(data)
}

// Sign signs as the new account key and the new recovery key, and with authority (the
// previous recovery key) after registration.
func (r RecoveryRecord) Sign(user, recovery, authority *hybridsig.PrivateKey) (RecoveryRecord, error) {
	msg := r.message()
	var err error
	if r.UserSig, err = user.Sign(recoveryUserDomain, msg); err != nil {
		return r, err
	}
	if r.HolderSig, err = recovery.Sign(recoveryHolderDomain, msg); err != nil {
		return r, err
	}
	r.Authority = nil
	if authority != nil {
		r.Authority, err = authority.Sign(recoveryAuthorityDomain, msg)
	}
	return r, err
}

func verify(key Key, domain string, msg, sig []byte) bool {
	pub, err := hybridsig.ParsePublicKey(key.Signing)
	return err == nil && len(sig) == hybridsig.SignatureBytes && pub.Verify(domain, msg, sig) == nil
}

func distinct(a, b Key) bool {
	return a.Recipient != b.Recipient && !bytes.Equal(a.Signing, b.Signing)
}

// VerifyRecovery checks one transition of the chain. before is nil for registration.
// Possession of the new keys is proven by their signatures; authority by the old recovery key.
func VerifyRecovery(before *RecoveryRecord, after RecoveryRecord) error {
	if !ID(after.Org) || !ValidUserID(after.User) || !after.Key.valid() || !after.Recovery.valid() ||
		!validDigest(after.Copy) || !distinct(after.Key, after.Recovery) {
		return ErrInvalid
	}
	msg := after.message()
	if !verify(after.Key, recoveryUserDomain, msg, after.UserSig) || !verify(after.Recovery, recoveryHolderDomain, msg, after.HolderSig) {
		return ErrInvalid
	}
	if before == nil {
		if after.Change != "register" || after.Sequence != 1 || after.Previous != "" || len(after.Authority) != 0 {
			return ErrConflict
		}
		return nil
	}
	if after.Org != before.Org || after.User != before.User || after.Sequence != before.Sequence+1 || after.Previous != before.Hash() {
		return ErrConflict
	}
	if !verify(before.Recovery, recoveryAuthorityDomain, msg, after.Authority) {
		return ErrDenied
	}
	switch after.Change {
	case "replace":
		if !distinct(after.Key, before.Key) {
			return ErrConflict
		}
	case "rotate":
		if after.Key.Fingerprint() != before.Key.Fingerprint() || !distinct(after.Recovery, before.Recovery) {
			return ErrConflict
		}
	default:
		return ErrInvalid
	}
	return nil
}

// ValidRecoveryCopy admits only an age file with one ML-KEM-768 + X25519 stanza that
// matches the record's digest. The server cannot open it.
func ValidRecoveryCopy(sealed []byte, digest string) bool {
	if len(sealed) > MaxRecoveryCopy || !bytes.HasPrefix(sealed, []byte(recoveryCopyHeader)) || Digest(sealed) != digest {
		return false
	}
	header, _, found := strings.Cut(string(sealed), "\n--- ")
	return found && strings.Count(header, "\n-> ") == 1
}
