package vaultclient

import (
	"errors"
	"slices"
	"time"

	"filippo.io/age"
	"github.com/Busnes-app/ky-primitives/hybridsig"
	"github.com/Busnes-app/kydrive-server/internal/vault"
)

// Leave room for age's 16-byte tag per 64 KiB chunk and the hybrid
// recipient header, authentication data, and payload nonce.
const RevisionPlaintextLimit = vault.MaxDocumentBytes - (16 << 10)

type RevisionInput struct {
	File    string
	Number  int64
	Body    []byte
	Binding string
}

type RevisionRead struct {
	File     string
	Number   int64
	Manifest vault.Revision
	Body     []byte
}

type SealedRevision struct {
	Manifest vault.Revision `json:"manifest"`
	Body     []byte         `json:"body"`
}

func (b *Session) currentDocument(file string, edit bool) (vault.Document, error) {
	if b.active == nil || !vault.ID(file) {
		return vault.Document{}, errors.New("Open a verified vault and choose a file")
	}
	active := b.active
	age := time.Since(time.UnixMilli(active.checkpoint.IssuedMS))
	if age < 0 || age >= vault.MaxCheckpointAge {
		return vault.Document{}, errors.New("Refresh the vault checkpoint")
	}
	last := active.records[len(active.records)-1]
	member, ok := last.State.Member(b.user)
	if !ok || edit && member.Role == vault.Reader {
		return vault.Document{}, errors.New("Vault access refused")
	}
	return vault.Document{Org: last.Org, Vault: last.Vault, File: file, Generation: last.State.Generation, Head: active.head, Signer: b.user}, nil
}

func (b *Session) SealRevision(in RevisionInput) (SealedRevision, error) {
	document, err := b.currentDocument(in.File, true)
	if err != nil {
		return SealedRevision{}, err
	}
	if in.Number < 1 || len(in.Body) > RevisionPlaintextLimit {
		return SealedRevision{}, errors.New("Invalid file revision or size")
	}
	encrypted, err := encrypt(in.Body, b.active.identity.Recipient())
	if err != nil {
		return SealedRevision{}, err
	}
	if len(encrypted) > vault.MaxDocumentBytes {
		return SealedRevision{}, errors.New("Encrypted vault revision exceeds the size limit")
	}
	manifest, err := (vault.Revision{Document: document, Number: in.Number, Digest: vault.Digest(encrypted), Size: int64(len(encrypted)), Binding: in.Binding}).Sign(b.signing)
	return SealedRevision{manifest, encrypted}, err
}

func (b *Session) ReadRevision(in RevisionRead) ([]byte, error) {
	doc, err := b.currentDocument(in.File, false)
	if err != nil {
		return nil, err
	}
	revision := in.Manifest
	if revision.File != doc.File || revision.Org != doc.Org || revision.Vault != doc.Vault || revision.Head.Log != doc.Head.Log ||
		revision.Head.Sequence < 1 || revision.Head.Sequence > int64(len(b.active.records)) || revision.Number != in.Number ||
		revision.Size != int64(len(in.Body)) || revision.Digest != vault.Digest(in.Body) {
		return nil, errors.New("The requested vault revision did not verify")
	}
	record := b.active.records[revision.Head.Sequence-1]
	member, found := record.State.Member(revision.Signer)
	public, err := hybridsig.ParsePublicKey(b.active.keys[revision.Signer].Signing)
	if !found || member.Role == vault.Reader || record.Link().Hash() != revision.Head.Hash || record.State.Generation != revision.Generation ||
		err != nil || revision.Verify(public) != nil {
		return nil, errors.New("The vault revision signer or head did not verify")
	}
	key := b.active.identity
	for index := len(b.active.records) - 1; index > 0 && b.active.records[index].State.Generation > revision.Generation; index-- {
		state, previous := b.active.records[index].State, b.active.records[index-1].State
		if previous.Generation == state.Generation {
			continue
		}
		key, err = unwrapIdentity(state.Previous, key, previous.Recipient)
		if err != nil {
			return nil, err
		}
	}
	return decrypt(in.Body, key, vault.MaxDocumentBytes)
}

type EditorInput struct {
	File           string
	Source         int64
	Rooms          []string
	Blobs          []string
	EditCapability string
	ViewCapability string
}

type EditorOpen struct {
	File    string
	Binding vault.Binding
}

type EditorSession struct {
	Binding    string           `json:"binding"`
	Capability string           `json:"capability"`
	Rooms      []string         `json:"rooms"`
	Edit       bool             `json:"edit"`
	Checkpoint vault.Checkpoint `json:"checkpoint"`
}

func (b *Session) BindEditor(in EditorInput) (vault.Binding, error) {
	doc, err := b.currentDocument(in.File, true)
	if err != nil {
		return vault.Binding{}, err
	}
	if len(in.EditCapability) == 0 || len(in.EditCapability) > 4096 || in.ViewCapability == in.EditCapability || len(in.ViewCapability) > 4096 {
		return vault.Binding{}, errors.New("Supply a valid separate editor capability for each mode")
	}
	current := b.active.records[len(b.active.records)-1]
	binding := vault.Binding{Document: doc, Source: in.Source, Rooms: in.Rooms, Blobs: in.Blobs}
	slices.Sort(binding.Rooms)
	slices.Sort(binding.Blobs)
	for _, member := range current.State.Members {
		capability := in.EditCapability
		if member.Role == vault.Reader {
			capability = in.ViewCapability
		}
		if capability == "" {
			return vault.Binding{}, errors.New("The reader needs a view capability")
		}
		recipient, err := age.ParseHybridRecipient(b.active.keys[member.User].Recipient)
		if err != nil {
			return vault.Binding{}, err
		}
		encrypted, err := encrypt([]byte(capability), recipient)
		if err != nil {
			return vault.Binding{}, err
		}
		binding.Grants = append(binding.Grants, vault.EditorGrant{User: member.User, Role: member.Role, Wrapped: encrypted})
	}
	signed, err := binding.Sign(b.signing)
	if err != nil {
		return vault.Binding{}, err
	}
	if err := signed.Verify(b.signing.Public()); err != nil {
		return vault.Binding{}, errors.New("The editor binding is invalid")
	}
	return signed, nil
}

func (b *Session) OpenEditor(in EditorOpen) (EditorSession, error) {
	doc, err := b.currentDocument(in.File, false)
	if err != nil {
		return EditorSession{}, err
	}
	binding := in.Binding
	if binding.Org != doc.Org || binding.Vault != doc.Vault || binding.File != doc.File || binding.Generation != doc.Generation || binding.Head != doc.Head {
		return EditorSession{}, errors.New("The editor binding does not match the verified vault head")
	}
	state := b.active.records[len(b.active.records)-1].State
	signer, ok := state.Member(binding.Signer)
	key, err := hybridsig.ParsePublicKey(b.active.keys[binding.Signer].Signing)
	if !ok || signer.Role == vault.Reader || err != nil || binding.Verify(key) != nil || len(binding.Grants) != len(state.Members) {
		return EditorSession{}, errors.New("The editor binding did not verify")
	}
	var grant vault.EditorGrant
	for i, candidate := range binding.Grants {
		member := state.Members[i]
		if candidate.User != member.User || candidate.Role != member.Role {
			return EditorSession{}, errors.New("The editor grants do not match current membership")
		}
		if candidate.User == doc.Signer {
			grant = candidate
		}
	}
	capability, err := decrypt(grant.Wrapped, b.member, 4096)
	if err != nil || len(capability) == 0 {
		return EditorSession{}, errors.New("The editor capability did not open")
	}
	return EditorSession{binding.ID(), string(capability), binding.Rooms, grant.Role != vault.Reader, b.active.checkpoint}, nil
}
