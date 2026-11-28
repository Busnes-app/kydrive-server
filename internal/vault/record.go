package vault

import (
	"bytes"
	"cmp"
	"encoding/json"
	"slices"
	"strings"

	"filippo.io/age"
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/google/uuid"
)

const (
	recordDomain = "kydrive-vault-member-v1"
	Manager      = "manager"
	Editor       = "editor"
	Reader       = "reader"
	MaxMembers   = 64
)

// Member holds a public-key fingerprint and ciphertext for one user.
type Member struct {
	User        string `json:"user"`
	Fingerprint string `json:"fingerprint"`
	Role        string `json:"role"`
	Wrap        []byte `json:"wrap"`
}

// State is the resulting membership and key generation after a record. Members
// are sorted by user ID. Recipient is public. Previous and NameIndex are encrypted.
type State struct {
	Generation int64    `json:"generation"`
	Recipient  string   `json:"recipient"`
	Previous   []byte   `json:"previous"`
	NameIndex  []byte   `json:"name_index"`
	Members    []Member `json:"members"`
}

type Record struct {
	Org       string `json:"org"`
	Vault     string `json:"vault"`
	Sequence  int64  `json:"sequence"`
	Previous  string `json:"previous"`
	Signer    string `json:"signer"`
	Change    string `json:"change"`
	Target    string `json:"target"`
	State     State  `json:"state"`
	Signature []byte `json:"signature"`
}

func ID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id != uuid.Nil && id.String() == s
}

// ValidUserID admits KyDrive's opaque account IDs, including existing usr_ IDs.
func ValidUserID(s string) bool {
	return s != "" && len(s) <= 128 && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) == -1
}

func ValidRecipient(s string) bool {
	if len(s) > 4096 {
		return false
	}
	recipient, err := age.ParseHybridRecipient(s)
	return err == nil && recipient.String() == s
}

func (s State) Member(user string) (Member, bool) {
	index, found := slices.BinarySearchFunc(s.Members, user, func(m Member, u string) int {
		return cmp.Compare(m.User, u)
	})
	if !found {
		return Member{}, false
	}
	return s.Members[index], true
}

func (r Record) message() []byte {
	r.Signature = nil
	body, _ := json.Marshal(r)
	return body
}

func (r Record) Sign(key *hybridsig.PrivateKey) (Record, error) {
	var err error
	r.Signature, err = key.Sign(recordDomain, r.message())
	return r, err
}

func (r Record) Link() Link {
	body, _ := json.Marshal(r)
	return Link{Sequence: r.Sequence, Previous: r.Previous, Record: Digest(body)}
}

func (r Record) valid() bool {
	if !ID(r.Org) || !ID(r.Vault) || !ValidUserID(r.Signer) || !ValidRecipient(r.State.Recipient) ||
		r.State.Generation < 1 || len(r.State.NameIndex) == 0 || len(r.State.NameIndex) > 8192 ||
		len(r.State.Previous) > 8192 || len(r.State.Members) == 0 || len(r.State.Members) > MaxMembers ||
		len(r.Signature) != hybridsig.SignatureBytes || (r.Sequence == 1 && r.Previous != "") ||
		(r.Sequence != 1 && (r.Sequence < 2 || !validDigest(r.Previous))) {
		return false
	}
	last, managers := "", 0
	for _, m := range r.State.Members {
		if !ValidUserID(m.User) || m.User <= last || !validDigest(m.Fingerprint) ||
			len(m.Wrap) == 0 || len(m.Wrap) > 8192 ||
			(m.Role != Reader && m.Role != Editor && m.Role != Manager) {
			return false
		}
		last = m.User
		if m.Role == Manager {
			managers++
		}
	}
	return managers > 0
}

// VerifyRecord checks a signed transition. Its caller supplies the signer's pinned key,
// and checks fingerprints against verified recipient keys before using a wrap.
// A replaying client also checks every new generation recipient against all recipients it
// verified earlier. The database prevents reuse across committed vault generations.
func VerifyRecord(before *Record, after Record, signer *hybridsig.PublicKey) error {
	if !after.valid() || signer == nil || signer.Verify(recordDomain, after.message(), after.Signature) != nil {
		return ErrInvalid
	}
	if before == nil {
		if after.Change != "create" || after.Target != "" || after.Sequence != 1 ||
			after.State.Generation != 1 || len(after.State.Previous) != 0 ||
			len(after.State.Members) != 1 || after.State.Members[0].User != after.Signer {
			return ErrConflict
		}
		return nil
	}
	if before.Org != after.Org || before.Vault != after.Vault || before.Sequence+1 != after.Sequence ||
		before.Link().Hash() != after.Previous {
		return ErrConflict
	}
	actor, found := before.State.Member(after.Signer)
	if !found || actor.Role != Manager {
		return ErrDenied
	}
	old, existed := before.State.Member(after.Target)
	next, remains := after.State.Member(after.Target)
	delta := 0
	switch after.Change {
	case "add":
		if existed || !remains {
			return ErrConflict
		}
		delta = 1
	case "remove":
		if !existed || remains {
			return ErrConflict
		}
		delta = -1
	case "role":
		if !existed || !remains || old.Role == next.Role {
			return ErrConflict
		}
	case "rekey":
		// Re-adds a member under their replacement key (recovery-signed, RecoveryRecord).
		if !existed || !remains || old.Fingerprint == next.Fingerprint || old.Role != next.Role {
			return ErrConflict
		}
	case "rotate":
		if after.Target != "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if len(after.State.Members) != len(before.State.Members)+delta {
		return ErrConflict
	}
	rotation := after.Change != "add"
	if rotation {
		if after.State.Generation != before.State.Generation+1 || after.State.Recipient == before.State.Recipient ||
			len(after.State.Previous) == 0 || bytes.Equal(after.State.Previous, before.State.Previous) ||
			bytes.Equal(after.State.NameIndex, before.State.NameIndex) {
			return ErrConflict
		}
	} else if after.State.Generation != before.State.Generation || after.State.Recipient != before.State.Recipient ||
		!bytes.Equal(after.State.Previous, before.State.Previous) || !bytes.Equal(after.State.NameIndex, before.State.NameIndex) {
		return ErrConflict
	}
	for _, m := range after.State.Members {
		if after.Change == "add" && m.User == after.Target {
			continue
		}
		p, present := before.State.Member(m.User)
		sameWrap := bytes.Equal(p.Wrap, m.Wrap)
		rekeyed := after.Change == "rekey" && m.User == after.Target
		if !present || (p.Fingerprint != m.Fingerprint) != rekeyed || sameWrap == rotation ||
			(p.Role != m.Role && (after.Change != "role" || m.User != after.Target)) {
			return ErrConflict
		}
	}
	return nil
}
