package vault

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Busnes-app/ky-primitives/hybridsig"
)

const (
	MaxDocumentBytes = 32 << 20
	MaxVaultBytes    = 1 << 30
	MaxRooms         = 8
	MaxEditorBlobs   = 64
	revisionDomain   = "kydrive-vault-file-revision-v1"
	bindingDomain    = "kydrive-vault-editor-binding-v1"
)

// Document identifies the file and exact membership state for a signed operation.
type Document struct {
	Org        string `json:"org"`
	Vault      string `json:"vault"`
	File       string `json:"file"`
	Generation int64  `json:"generation"`
	Head       Head   `json:"head"`
	Signer     string `json:"signer"`
}

func (d Document) valid() bool {
	return ID(d.Org) && ID(d.Vault) && ID(d.File) && d.Generation > 0 &&
		validDigest(d.Head.Log) && validDigest(d.Head.Hash) && d.Head.Sequence > 0 &&
		ValidUserID(d.Signer)
}

type Revision struct {
	Document
	Number    int64  `json:"number"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Binding   string `json:"binding,omitempty"`
	Signature []byte `json:"signature"`
}

func (r Revision) Unsigned() []byte {
	r.Signature = nil
	data, _ := json.Marshal(r)
	return data
}

func (r Revision) Verify(key *hybridsig.PublicKey) error {
	if !r.Document.valid() || r.Number < 1 || !validDigest(r.Digest) || r.Size < 1 ||
		r.Size > MaxDocumentBytes || (r.Binding != "" && !validDigest(r.Binding)) ||
		key == nil || key.Verify(revisionDomain, r.Unsigned(), r.Signature) != nil {
		return ErrInvalid
	}
	return nil
}

func (r Revision) Sign(key *hybridsig.PrivateKey) (Revision, error) {
	var err error
	r.Signature, err = key.Sign(revisionDomain, r.Unsigned())
	return r, err
}

// Binding reserves fresh Netflux rooms for one version and membership generation.
// Room identifiers are public routing metadata. An editor capability is encrypted
// to an individual recipient so readers can receive only their view capability.
type Binding struct {
	Document
	Source    int64         `json:"source"`
	Rooms     []string      `json:"rooms"`
	Grants    []EditorGrant `json:"grants"`
	Blobs     []string      `json:"blobs,omitempty"`
	Signature []byte        `json:"signature"`
}

type EditorGrant struct {
	User    string `json:"user"`
	Role    string `json:"role"`
	Wrapped []byte `json:"wrapped"`
}

func (b Binding) unsigned() []byte {
	b.Signature = nil
	data, _ := json.Marshal(b)
	return data
}

func (b Binding) ID() string {
	data, _ := json.Marshal(b)
	return Digest(data)
}

func (b Binding) Sign(key *hybridsig.PrivateKey) (Binding, error) {
	var err error
	b.Signature, err = key.Sign(bindingDomain, b.unsigned())
	return b, err
}

func (b Binding) Verify(key *hybridsig.PublicKey) error {
	if !b.Document.valid() || b.Source < 1 || len(b.Rooms) < 1 || len(b.Rooms) > MaxRooms ||
		len(b.Blobs) > MaxEditorBlobs || !slices.IsSorted(b.Blobs) || len(b.Grants) == 0 || len(b.Grants) > MaxMembers || !slices.IsSorted(b.Rooms) ||
		key == nil || key.Verify(bindingDomain, b.unsigned(), b.Signature) != nil {
		return ErrInvalid
	}
	for _, g := range b.Grants {
		if !ValidUserID(g.User) || (g.Role != Reader && g.Role != Editor && g.Role != Manager) ||
			len(g.Wrapped) == 0 || len(g.Wrapped) > 8192 {
			return ErrInvalid
		}
	}
	for i, blob := range b.Blobs {
		if !ValidEditorBlob(blob) || (i > 0 && b.Blobs[i-1] == blob) {
			return ErrInvalid
		}
	}
	for i, room := range b.Rooms {
		if !ValidRoom(room) || (i != 0 && b.Rooms[i-1] == room) {
			return ErrInvalid
		}
	}
	return nil
}

func ValidRoom(room string) bool {
	return (len(room) == 32 || len(room) == 34) && lowerHex(room)
}

func ValidEditorBlob(blob string) bool {
	return len(blob) == 48 && lowerHex(blob)
}

func lowerHex(value string) bool {
	return strings.IndexFunc(value, func(c rune) bool {
		return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f')
	}) == -1
}
